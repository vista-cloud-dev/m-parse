# Vendored sources for the grammar WASM

`parse/tree-sitter-m.wasm` is compiled from the C sources under `vendor/` plus
`shim.c`, by `build.sh`, using the `emscripten/emsdk` Docker image. The output
is **committed** to the repo (it is `go:embed`-ed by the `parse` package);
rebuild it with `make wasm` only when one of these inputs changes.

## What is vendored

| Path | Upstream | Version / commit | License |
|------|----------|------------------|---------|
| `vendor/runtime/` | [tree-sitter/tree-sitter](https://github.com/tree-sitter/tree-sitter) | `0.25.10` (from the `tree-sitter` crate `src/` + `include/`) | **MIT** |
| `vendor/grammar/src/{parser.c,scanner.c,tree_sitter/*.h}` | [m-dev-tools/tree-sitter-m](https://github.com/m-dev-tools/tree-sitter-m) | commit `94eb80d` (grammar ABI **15**) | **AGPL-3.0** |
| `shim.c` | this repo | — | Apache-2.0 |

The runtime amalgamation (`vendor/runtime/src/lib.c`) `#include`s the rest of the
runtime `src/`. Its `wasm_store.c` is gated behind `TREE_SITTER_FEATURE_WASM`,
which `build.sh` does **not** define — so no wasmtime dependency is pulled in and
the runtime compiles cleanly to WASM.

## How the build is wired

`build.sh` runs one `emcc` invocation over:

```
shim.c
vendor/runtime/src/lib.c            (-Ivendor/runtime/include -Ivendor/runtime/src)
vendor/grammar/src/parser.c         (-Ivendor/grammar/src → tree_sitter/parser.h)
vendor/grammar/src/scanner.c
```

with `-sSTANDALONE_WASM=1 --no-entry` (an emscripten *reactor*: exports
`_initialize`, imports `wasi_snapshot_preview1` + `env.emscripten_notify_memory_growth`,
both satisfied by the wazero host in `parse/wasm.go`). `-sEXPORTED_FUNCTIONS`
lists the flat `tsm_*` ABI from `shim.c`.

Grammar ABI 15 is within the runtime's supported range
(`TREE_SITTER_MIN_COMPATIBLE_LANGUAGE_VERSION` 13 … `TREE_SITTER_LANGUAGE_VERSION` 15).

## Licensing note (open item)

The compiled `tree-sitter-m.wasm` embeds the **AGPL-3.0** grammar. The Go code in
this repo is Apache-2.0, and the toolchain spec (§10) wants the Go binaries
Apache-2.0. Reconciling the grammar's AGPL with that goal — relicense the
grammar, dual-license, or accept AGPL for the embedded artifact — is a decision
for the toolchain owners. It does not affect the parse substrate's technical
behavior. See the repo `NOTICE` and the spec.
