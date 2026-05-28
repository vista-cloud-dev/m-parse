// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 vista-cloud-dev contributors.
//
// shim.c — a flat numeric ABI over the tree-sitter C API, so the parse
// substrate can be driven from Go through wazero with no CGO.
//
// Why a shim: wazero can only call exported wasm functions whose params and
// results are wasm scalars (i32/i64/f32/f64). The tree-sitter API passes
// TSNode/TSPoint *by value* (TSNode is {uint32 context[4]; ptr id; ptr tree}),
// which cannot cross that boundary. So every node here is heap-boxed and
// handed back as an i32 pointer (an opaque handle into wasm linear memory);
// accessors take that handle and return scalars. Strings (node "type",
// s-expressions) are returned as pointers into wasm memory that the Go side
// reads until the terminating NUL.
//
// Ownership: handles returned by tsm_* are owned by the caller. Free a node
// handle with tsm_node_delete, a tree with tsm_tree_delete, a parser with
// tsm_parser_delete, and any tsm_malloc/ts_node_string buffer with tsm_free.

#include "tree_sitter/api.h"
#include <stdlib.h>

// The grammar's exported entry point (defined in the vendored parser.c).
extern const TSLanguage *tree_sitter_m(void);

// --- raw memory (Go writes source bytes here; named so Go need not depend on
// emscripten's malloc/free export names) --------------------------------------

void *tsm_malloc(uint32_t n) { return malloc((size_t)n); }
void tsm_free(void *p) { free(p); }

// --- parser lifecycle --------------------------------------------------------

TSParser *tsm_parser_new(void) {
  TSParser *p = ts_parser_new();
  if (p == NULL) return NULL;
  if (!ts_parser_set_language(p, tree_sitter_m())) {
    ts_parser_delete(p);
    return NULL;
  }
  return p;
}

void tsm_parser_delete(TSParser *p) { ts_parser_delete(p); }

// --- parse -------------------------------------------------------------------

// tsm_parse parses src[0..len) as a fresh document (no incremental reuse).
// Returns a tree handle, or NULL on failure.
TSTree *tsm_parse(TSParser *p, const char *src, uint32_t len) {
  return ts_parser_parse_string(p, NULL, src, len);
}

void tsm_tree_delete(TSTree *t) { ts_tree_delete(t); }

// --- node handles (heap-boxed TSNode) ----------------------------------------

static TSNode *box(TSNode n) {
  TSNode *h = (TSNode *)malloc(sizeof(TSNode));
  if (h != NULL) *h = n;
  return h;
}

TSNode *tsm_root_node(TSTree *t) { return box(ts_tree_root_node(t)); }
void tsm_node_delete(TSNode *n) { free(n); }

// Identity/type.
const char *tsm_node_type(TSNode *n) { return ts_node_type(*n); }
uint16_t tsm_node_symbol(TSNode *n) { return ts_node_symbol(*n); }

// Spans (byte offsets + zero-based row/column points).
uint32_t tsm_node_start_byte(TSNode *n) { return ts_node_start_byte(*n); }
uint32_t tsm_node_end_byte(TSNode *n) { return ts_node_end_byte(*n); }
uint32_t tsm_node_start_row(TSNode *n) { return ts_node_start_point(*n).row; }
uint32_t tsm_node_start_col(TSNode *n) { return ts_node_start_point(*n).column; }
uint32_t tsm_node_end_row(TSNode *n) { return ts_node_end_point(*n).row; }
uint32_t tsm_node_end_col(TSNode *n) { return ts_node_end_point(*n).column; }

// Structure.
uint32_t tsm_node_child_count(TSNode *n) { return ts_node_child_count(*n); }
uint32_t tsm_node_named_child_count(TSNode *n) { return ts_node_named_child_count(*n); }
TSNode *tsm_node_child(TSNode *n, uint32_t i) { return box(ts_node_child(*n, i)); }
TSNode *tsm_node_named_child(TSNode *n, uint32_t i) { return box(ts_node_named_child(*n, i)); }

// The field name (e.g. "name", "body") under which child i sits, or NULL.
const char *tsm_node_field_name_for_child(TSNode *n, uint32_t i) {
  return ts_node_field_name_for_child(*n, i);
}

// Predicates (i32 booleans).
int32_t tsm_node_is_named(TSNode *n) { return ts_node_is_named(*n) ? 1 : 0; }
int32_t tsm_node_is_missing(TSNode *n) { return ts_node_is_missing(*n) ? 1 : 0; }
int32_t tsm_node_is_error(TSNode *n) { return ts_node_is_error(*n) ? 1 : 0; }
int32_t tsm_node_has_error(TSNode *n) { return ts_node_has_error(*n) ? 1 : 0; }
int32_t tsm_node_is_null(TSNode *n) { return ts_node_is_null(*n) ? 1 : 0; }

// The Lisp-style s-expression for the subtree. Caller frees with tsm_free.
char *tsm_node_string(TSNode *n) { return ts_node_string(*n); }

// --- grammar metadata (printed by `m-parse version`; the --version audit) ----

uint32_t tsm_language_abi_version(void) { return ts_language_abi_version(tree_sitter_m()); }
uint32_t tsm_language_symbol_count(void) { return ts_language_symbol_count(tree_sitter_m()); }
