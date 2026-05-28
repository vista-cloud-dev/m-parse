# m-parse

**The engine-neutral M parse substrate** for the m-cli Go toolchain — the one
foundational library that `fmt`, `lint`, and `lsp` all sit on (spec §4). It runs
the [`tree-sitter-m`](https://github.com/m-dev-tools/tree-sitter-m) grammar
through [**wazero**](https://github.com/tetratelabs/wazero), a pure-Go WASM
runtime, so the whole toolchain stays **static (`CGO_ENABLED=0`)** — no libc, no
C toolchain at run time — while reusing the exact same 99%-on-VistA grammar the
editor (TypeScript / `web-tree-sitter`) uses. One grammar artifact, two consumers.

> **Why not the normal Go binding?** The mainstream Go tree-sitter binding is
> CGO, which kills `CGO_ENABLED=0` and the minimal-SBOM, single-static-binary
> premise that justifies Go for the VA FedRAMP boundary. Embedding the grammar
> **as WASM** and driving it via wazero is the way to keep the static guarantee
> *and* avoid forking a second M parser into Go (spec §4; ADR §3.1). This repo
> is the P0 spike that proved that path works end-to-end.

```sh
go test ./parse/                       # parse real M, fully static, no CGO
go run . parse parse/testdata/hello.m  # styled tree summary + s-expression
go run . parse routine.m -o json       # machine surface: counts + error spans
go run . version                       # build info + embedded grammar hash + ABI
```

---

## Contents

- [What it is](#what-it-is)
- [Architecture](#architecture)
- [The `parse` package API](#the-parse-package-api)
- [The `m-parse` CLI](#the-m-parse-cli)
- [Rebuilding the grammar WASM](#rebuilding-the-grammar-wasm)
- [Repository layout](#repository-layout)
- [Build, test, CI](#build-test-ci)
- [Licensing](#licensing)

---

## What it is

Two things in one repo:

1. **`parse/` — the library** (the real deliverable). Import
   `github.com/vista-cloud-dev/m-parse/parse` and you get a `Parser` that turns M
   source into a `Tree` of `Node`s with types, byte/point spans, named-child
   navigation, field names, error/missing detection, and s-expressions — the
   surface `fmt`/`lint`/`lsp` need. No CGO; the grammar WASM is embedded.

2. **`m-parse` — a thin CLI** that exercises the library end-to-end and inherits
   the shared `clikit` conventions (`--output text|json|auto`, the JSON
   envelope, `schema`, deterministic exit codes). It's the spike's smoke test and
   a handy `parse a file, show me the tree` tool.

`.mac` is parsed **as M**, mechanically the same as `.m`. Non-MUMPS `.mac`
constructs (embedded SQL, preprocessor directives, ObjectScript) are out of
scope (spec §4/§13).

## Architecture

```
   source bytes (.m / .mac)
            │
            ▼
   parse.Parser ──────────────────────────────────────────────┐
            │  wazero (pure-Go WASM runtime, CGO_ENABLED=0)     │
            ▼                                                   │
   parse/tree-sitter-m.wasm  (go:embed, ~415 KB)               │
   ┌──────────────────────────────────────────────────┐       │
   │  ONE standalone WASI module, built from:           │       │
   │   • tree-sitter runtime  (lib.c, v0.25.10)         │       │
   │   • the M grammar        (parser.c + scanner.c)    │       │
   │   • wasm/shim.c — a flat numeric ABI (tsm_*)       │◄──────┘
   └──────────────────────────────────────────────────┘
            │  tsm_parse / tsm_root_node / tsm_node_* (i32 handles)
            ▼
   Tree → RootNode() → Node{ Type, Start/EndByte, Start/EndPoint,
                             Child/NamedChild, FieldNameForChild,
                             IsNamed/IsMissing/IsError/HasError, Text, SExpr }
```

The shim exists because wazero can only pass WASM scalars across the boundary,
but the tree-sitter C API passes `TSNode`/`TSPoint` **by value**. `wasm/shim.c`
heap-boxes each node and hands Go an `i32` handle; accessors take the handle and
return scalars. See [`wasm/shim.c`](wasm/shim.c) for the full ABI.

## The `parse` package API

```go
p, err := parse.New(ctx)          // instantiate the embedded grammar in wazero
defer p.Close(ctx)

tree, err := p.Parse(ctx, src)    // a tree with error nodes is returned normally
defer tree.Close()

root := tree.RootNode()           // → "source_file"
root.HasError()                   // any syntax error anywhere in the subtree?
for i := uint32(0); i < root.NamedChildCount(); i++ {
    n := root.NamedChild(i)
    n.Type()                      // grammar rule name, e.g. "line"
    n.StartByte(); n.EndByte()    // byte span
    n.StartPoint()                // {Row, Column} (zero-based)
    n.Text()                      // source slice for the span
    n.FieldNameForChild(0)        // "name" / "" — grammar field
}
root.SExpr()                      // (source_file (line ...) ...) — great for tests

parse.GrammarHash()               // sha256:… of the embedded WASM (--version audit)
p.GrammarABIVersion()             // 15
```

A `Parser` is reusable for many sequential parses and guards WASM access with a
mutex (wazero modules are not reentrant). `Node` handles are valid until their
`Tree` is closed.

## The `m-parse` CLI

| Command | What it does |
|---------|--------------|
| `m-parse parse <file>` | Parse an M file; styled tree summary + s-expression (text) or counts + error spans (JSON). `--check` exits 3 on syntax errors; `--sexpr` forces the s-expression. |
| `m-parse version` | Version/commit/date, Go toolchain, **embedded grammar hash + ABI version**. |
| `m-parse schema` | The reflected command/flag tree as JSON (agent discovery). |

`--output auto` (default) styles on a TTY and emits the JSON envelope when piped,
so scripts and agents never scrape colored prose.

## Rebuilding the grammar WASM

The embedded `parse/tree-sitter-m.wasm` is **committed** (it must be, for
`go:embed`). It's reproducible from vendored C sources — you only rebuild it when
the grammar or runtime version changes:

```sh
make wasm        # = ./wasm/build.sh
```

`wasm/build.sh` compiles `wasm/vendor/runtime` (tree-sitter 0.25.10) +
`wasm/vendor/grammar` (the M grammar's `parser.c`/`scanner.c`) + `wasm/shim.c`
into one standalone WASI module via the `emscripten/emsdk` Docker image. The
image is pulled once and cached, so subsequent builds are **offline**. Provenance
and licensing of the vendored sources are documented in
[`wasm/VENDOR.md`](wasm/VENDOR.md).

## Repository layout

```
m-parse/
├── parse/                       # THE LIBRARY (import this)
│   ├── parse.go                 #   Tree / Node / Point public API + GrammarHash
│   ├── wasm.go                  #   wazero runtime + the tsm_* C-ABI binding
│   ├── tree-sitter-m.wasm       #   embedded grammar artifact (committed; go:embed)
│   ├── parse_test.go            #   end-to-end tests (parse real M, walk the tree)
│   └── testdata/hello.m
├── wasm/                        # the WASM build pipeline (build-time only)
│   ├── shim.c                   #   flat numeric ABI over the tree-sitter C API
│   ├── build.sh                 #   emcc via the cached emscripten image (offline)
│   ├── VENDOR.md                #   provenance + licenses of vendored C sources
│   └── vendor/{runtime,grammar} #   tree-sitter 0.25.10 + the M grammar sources
├── main.go                      # the m-parse CLI (parse / version / schema)
├── clikit/                      # shared CLI conventions (from go-cli-template)
├── Makefile · .golangci.yml · .github/workflows/ci.yml
└── LICENSE · NOTICE             # Apache-2.0 (Go); see Licensing for the grammar
```

## Build, test, CI

| Target | What it does |
|--------|--------------|
| `make build` | `dist/m-parse`, static (`CGO_ENABLED=0`), `-trimpath`, version-stamped |
| `make test`  | `go test -race -cover ./...` (race needs CGO; the rest is CGO-free) |
| `make lint`  | `golangci-lint run ./...` |
| `make schema`| build + emit the JSON schema (CI conformance artifact) |
| `make wasm`  | rebuild `parse/tree-sitter-m.wasm` from `wasm/vendor` |
| `make dist`  | cross-compile `linux/{amd64,arm64}`, `darwin/arm64`, `windows/amd64` |

CI (`.github/workflows/ci.yml`, via the org's reusable `go-ci` workflow) runs
golangci-lint, race tests, the `schema` contract, and a static `CGO_ENABLED=0`
cross-compile matrix — gate **G5** (static / offline) for the parse substrate.

## Licensing

Licenses differ **by artifact**:

- **The Go code** in this repo (`parse/`, `main.go`, `clikit/`, `wasm/shim.c`) is
  **Apache-2.0** — see [`LICENSE`](LICENSE) / [`NOTICE`](NOTICE).
- **The embedded `tree-sitter-m.wasm`** is built from the `tree-sitter-m` grammar,
  which is **AGPL-3.0**, plus the tree-sitter runtime (**MIT**). The compiled
  WASM is therefore an AGPL-derived artifact embedded in the binary. The spec
  wants the Go binaries Apache-2.0 (spec §10); reconciling the grammar's license
  with that goal is an **open item** flagged for the toolchain owners — it does
  not affect the spike's technical result. See [`wasm/VENDOR.md`](wasm/VENDOR.md).
