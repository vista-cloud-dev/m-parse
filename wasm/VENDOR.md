# Vendored sources for the grammar WASM

`parse/tree-sitter-m.wasm` is compiled from the C sources under `vendor/` plus
`shim.c`, by `build.sh`, using the `emscripten/emsdk` Docker image. The output
is **committed** to the repo (it is `go:embed`-ed by the `parse` package);
rebuild it with `make wasm` only when one of these inputs changes.

## What is vendored

| Path | Upstream | Version / commit | License |
|------|----------|------------------|---------|
| `vendor/runtime/` | [tree-sitter/tree-sitter](https://github.com/tree-sitter/tree-sitter) | `0.25.10` (from the `tree-sitter` crate `src/` + `include/`) | **MIT** |
| `vendor/grammar/src/{parser.c,scanner.c,tree_sitter/*.h}` | [m-dev-tools/tree-sitter-m](https://github.com/m-dev-tools/tree-sitter-m) | **v0.1.2**, commit `c796342` (grammar ABI **15**) | **AGPL-3.0** |
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

## Revendor history

| Grammar | tree-sitter-m | Notes |
|---------|---------------|-------|
| v0.1.2 | commit `c796342` (PR #7) | YottaDB m-modern-corpus coverage: `&`/`$&` external C call-outs, `WRITE`/`USE` `/mnemonic` device controls, `TSTART`/`LOCK` argument-list `:timeout`, `KILL *`/`TSTART *`, extended reference `^\|env\|gvn` / `^[env]gvn`. Additive grammar.js only — `scanner.c` unchanged. ABI unchanged (15). |
| v0.1.1 | commit `94eb80d` | prior baseline. |

**Parse-coverage impact of the v0.1.2 grammar** (files with a literal
`(ERROR)` node, full corpora):

| Corpus | before (v0.1.1) | after (v0.1.2) |
|--------|-----------------|----------------|
| m-modern-corpus (4,215 YDB routines) | 451 err / 89.30 % clean | **230 err / 94.54 % clean** |
| VistA (39,330 routines) | 363 err / 99.08 % clean | **336 err / 99.15 % clean** (held; slightly improved) |

The VistA corpus did not regress. **Follow-up:** m-cli must bump its
m-parse dependency to pick up this grammar.

## Licensing note (deferred to project completion)

The compiled `tree-sitter-m.wasm` embeds the currently **AGPL-3.0** grammar,
while the Go code in this repo is Apache-2.0.

**Project policy: all licensing reconciliation is deferred to project
completion.** At that point everything will be reconciled to a single end-state —
**Apache-2.0 for every artifact except the VS Code extensions, which will be
MIT** (spec §10). The grammar's interim AGPL status is intentionally **not** a
blocker and does not affect the parse substrate's technical behavior; it will be
relicensed/reconciled to fit the Apache-2.0 end-state at completion. See the repo
`NOTICE` and the spec.
