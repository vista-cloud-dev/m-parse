# m-parse — the engine-neutral M parse substrate (tree-sitter-m via wazero).
# Inherits the shared toolchain conventions from go-cli-template: static
# (CGO_ENABLED=0), -trimpath, version stamped via -ldflags, cross-compile
# matrix, lint, test, schema. Adds the `wasm` target that (re)builds the
# embedded grammar artifact (parse/tree-sitter-m.wasm).

BIN     ?= m-parse
PKG     := github.com/vista-cloud-dev/m-parse
LDPKG   := $(PKG)/clikit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w -X $(LDPKG).Version=$(VERSION) -X $(LDPKG).Commit=$(COMMIT) -X $(LDPKG).Date=$(DATE)

# Static, no-libc, reproducible (spec §10). The wazero parser path keeps this
# true — no CGO anywhere in the Go build.
GOFLAGS := -trimpath
export CGO_ENABLED := 0

PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 windows/amd64

.PHONY: all build run lint test tidy schema wasm dist clean

all: lint test build

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BIN) .

run: build
	./dist/$(BIN) $(ARGS)

lint:
	golangci-lint run ./...

# The race detector needs CGO; the rest of the build is CGO-free (the wazero
# parser path is pure Go). Override the file-level CGO_ENABLED=0 just here.
test:
	CGO_ENABLED=1 go test $(GOFLAGS) -race -cover ./...

tidy:
	go mod tidy

# Emit the machine schema (the §5.5 contract) — also a CI conformance artifact.
schema: build
	./dist/$(BIN) schema

# Rebuild the embedded grammar WASM from wasm/vendor (tree-sitter runtime + the
# M grammar + shim.c) via the cached emscripten image. Commit the result.
wasm:
	./wasm/build.sh

# Cross-compile the pinned matrix into dist/.
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
			-o dist/$(BIN)-$$os-$$arch$$ext . ; \
	done

clean:
	rm -rf dist
