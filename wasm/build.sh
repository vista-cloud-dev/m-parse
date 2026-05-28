#!/usr/bin/env bash
# Build parse/tree-sitter-m.wasm — the tree-sitter runtime + the M grammar +
# shim.c compiled into ONE standalone WASI wasm module, driven from Go via
# wazero with no CGO (spec §4).
#
# Toolchain: emscripten, run from the emscripten/emsdk Docker image. The image
# is pulled once and cached, so subsequent builds are offline. STANDALONE_WASM
# emits a WASI reactor module (exports _initialize, imports wasi_snapshot_
# preview1) that wazero runs natively.
#
# Usage:  ./build.sh            # writes ../parse/tree-sitter-m.wasm
#         EMSDK_IMAGE=… ./build.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
img="${EMSDK_IMAGE:-emscripten/emsdk:4.0.4}"
out="parse/tree-sitter-m.wasm"

# The flat ABI surface (see shim.c). emscripten wants leading underscores.
exports='[
"_tsm_malloc","_tsm_free",
"_tsm_parser_new","_tsm_parser_delete","_tsm_parse","_tsm_tree_delete",
"_tsm_root_node","_tsm_node_delete",
"_tsm_node_type","_tsm_node_symbol",
"_tsm_node_start_byte","_tsm_node_end_byte",
"_tsm_node_start_row","_tsm_node_start_col","_tsm_node_end_row","_tsm_node_end_col",
"_tsm_node_child_count","_tsm_node_named_child_count",
"_tsm_node_child","_tsm_node_named_child","_tsm_node_field_name_for_child",
"_tsm_node_is_named","_tsm_node_is_missing","_tsm_node_is_error",
"_tsm_node_has_error","_tsm_node_is_null","_tsm_node_string",
"_tsm_language_abi_version","_tsm_language_symbol_count",
"_tsm_query_new","_tsm_query_delete","_tsm_query_capture_name",
"_tsm_query_cursor_new","_tsm_query_cursor_delete",
"_tsm_query_cursor_exec","_tsm_query_cursor_next_capture"
]'

echo "building $out via $img (offline if cached)…"
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -e EM_CACHE=/tmp/emcache \
  -v "$root:/work" -w /work/wasm \
  "$img" \
  emcc -O2 \
    -sSTANDALONE_WASM=1 \
    -sALLOW_MEMORY_GROWTH=1 \
    -sERROR_ON_UNDEFINED_SYMBOLS=1 \
    -sEXPORTED_FUNCTIONS="$exports" \
    --no-entry \
    -Ivendor/runtime/include -Ivendor/runtime/src -Ivendor/grammar/src \
    shim.c \
    vendor/runtime/src/lib.c \
    vendor/grammar/src/parser.c \
    vendor/grammar/src/scanner.c \
    -o "/work/$out"

echo "wrote $root/$out ($(wc -c <"$root/$out") bytes)"
