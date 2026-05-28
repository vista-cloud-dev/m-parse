// Package parse is the engine-neutral M parse substrate: it runs the
// tree-sitter-m grammar through a pure-Go WASM runtime (wazero) with no CGO,
// exposing a small Tree/Node API that fmt/lint/lsp build on (spec §4).
//
// The grammar (tree-sitter-m) is C compiled to WASM; the mainstream Go
// tree-sitter binding is CGO, which would kill the CGO_ENABLED=0 static-binary
// premise. Embedding the grammar WASM and driving it via wazero keeps the
// whole binary static and lets the Go and TypeScript tiers share one grammar
// artifact. The .wasm bundles the tree-sitter runtime + the M grammar + a thin
// C shim (see ../wasm); build it with ../wasm/build.sh.
//
// .mac is parsed as M, mechanically the same as .m; non-MUMPS .mac constructs
// (embedded SQL, preprocessor, ObjectScript) are out of scope (spec §4/§13).
package parse

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
)

//go:embed tree-sitter-m.wasm
var grammarWASM []byte

// GrammarHash is the sha256 of the embedded grammar WASM, printed by the
// consumer's --version for the pin/mirror audit trail (spec §4).
func GrammarHash() string {
	sum := sha256.Sum256(grammarWASM)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Point is a zero-based source position (tree-sitter row/column, in bytes).
type Point struct {
	Row    uint32
	Column uint32
}

// Tree is a parsed syntax tree. It owns the WASM-side tree plus every node
// handle produced from it; Close frees them all. A Tree is valid only while
// its Parser is open, and is not safe for concurrent use.
type Tree struct {
	p     *Parser
	ptr   uint32   // TSTree* in wasm memory
	nodes []uint32 // heap-boxed TSNode* handles to free on Close
	src   []byte   // the source, retained so Node.Text can slice it
}

// RootNode returns the tree's root node.
func (t *Tree) RootNode() Node {
	t.p.mu.Lock()
	defer t.p.mu.Unlock()
	ptr := uint32(t.p.must1("tsm_root_node", uint64(t.ptr)))
	t.nodes = append(t.nodes, ptr)
	return Node{t: t, ptr: ptr}
}

// Close frees the tree and all node handles derived from it.
func (t *Tree) Close() {
	if t.p == nil || t.ptr == 0 {
		return
	}
	t.p.mu.Lock()
	defer t.p.mu.Unlock()
	for _, n := range t.nodes {
		_, _ = t.p.call(context.Background(), "tsm_node_delete", uint64(n))
	}
	_, _ = t.p.call(context.Background(), "tsm_tree_delete", uint64(t.ptr))
	t.nodes = nil
	t.ptr = 0
}

// Node is a handle to a node in a Tree. It is a value type but refers back to
// the owning Tree; it stays valid until the Tree is closed.
type Node struct {
	t   *Tree
	ptr uint32 // heap-boxed TSNode* in wasm memory
}

// Type is the node's grammar type name (e.g. "command", "routine_header").
func (n Node) Type() string {
	n.t.p.mu.Lock()
	defer n.t.p.mu.Unlock()
	return n.t.p.readCStr(uint32(n.t.p.must1("tsm_node_type", uint64(n.ptr))))
}

// Symbol is the node's numeric grammar symbol id.
func (n Node) Symbol() uint16 {
	return uint16(n.u32("tsm_node_symbol"))
}

// StartByte is the byte offset where the node's source span begins.
func (n Node) StartByte() uint32 { return n.u32("tsm_node_start_byte") }

// EndByte is the byte offset where the node's source span ends.
func (n Node) EndByte() uint32 { return n.u32("tsm_node_end_byte") }

// StartPoint is the node's start position as a row/column point.
func (n Node) StartPoint() Point {
	return Point{Row: n.u32("tsm_node_start_row"), Column: n.u32("tsm_node_start_col")}
}

// EndPoint is the node's end position as a row/column point.
func (n Node) EndPoint() Point {
	return Point{Row: n.u32("tsm_node_end_row"), Column: n.u32("tsm_node_end_col")}
}

// ChildCount is the number of children (named and anonymous tokens).
func (n Node) ChildCount() uint32 { return n.u32("tsm_node_child_count") }

// NamedChildCount is the number of named (rule) children, skipping tokens.
func (n Node) NamedChildCount() uint32 { return n.u32("tsm_node_named_child_count") }

// Child returns the i-th child (all children, including anonymous tokens).
func (n Node) Child(i uint32) Node { return n.childAt("tsm_node_child", i) }

// NamedChild returns the i-th named child (skips anonymous tokens).
func (n Node) NamedChild(i uint32) Node { return n.childAt("tsm_node_named_child", i) }

// FieldNameForChild is the grammar field name (e.g. "name") under which child i
// sits, or "" if the child is not in a field.
func (n Node) FieldNameForChild(i uint32) string {
	n.t.p.mu.Lock()
	defer n.t.p.mu.Unlock()
	ptr := uint32(n.t.p.must1("tsm_node_field_name_for_child", uint64(n.ptr), uint64(i)))
	if ptr == 0 {
		return ""
	}
	return n.t.p.readCStr(ptr)
}

// IsNamed reports whether the node is a named (rule) node vs. an anonymous token.
func (n Node) IsNamed() bool { return n.boolFn("tsm_node_is_named") }

// IsMissing reports whether the node was inserted by error recovery.
func (n Node) IsMissing() bool { return n.boolFn("tsm_node_is_missing") }

// IsError reports whether the node is a syntax-error node.
func (n Node) IsError() bool { return n.boolFn("tsm_node_is_error") }

// HasError reports whether the node or any descendant is an error/missing node.
func (n Node) HasError() bool { return n.boolFn("tsm_node_has_error") }

// IsNull reports whether the handle is the tree-sitter null node (e.g. an
// out-of-range Child index).
func (n Node) IsNull() bool { return n.boolFn("tsm_node_is_null") }

// Text returns the node's source slice, using the Tree's retained source.
func (n Node) Text() []byte {
	s, e := n.StartByte(), n.EndByte()
	if int(e) > len(n.t.src) || s > e {
		return nil
	}
	return n.t.src[s:e]
}

// SExpr returns the Lisp-style s-expression of the subtree (handy for tests).
func (n Node) SExpr() string {
	n.t.p.mu.Lock()
	defer n.t.p.mu.Unlock()
	ptr := uint32(n.t.p.must1("tsm_node_string", uint64(n.ptr)))
	if ptr == 0 {
		return ""
	}
	s := n.t.p.readCStr(ptr)
	_, _ = n.t.p.call(context.Background(), "tsm_free", uint64(ptr))
	return s
}

// --- internal node helpers (each takes the parser lock for the whole op) ----

func (n Node) u32(fn string) uint32 {
	n.t.p.mu.Lock()
	defer n.t.p.mu.Unlock()
	return uint32(n.t.p.must1(fn, uint64(n.ptr)))
}

func (n Node) boolFn(fn string) bool {
	return n.u32(fn) != 0
}

func (n Node) childAt(fn string, i uint32) Node {
	n.t.p.mu.Lock()
	defer n.t.p.mu.Unlock()
	ptr := uint32(n.t.p.must1(fn, uint64(n.ptr), uint64(i)))
	n.t.nodes = append(n.t.nodes, ptr)
	return Node{t: n.t, ptr: ptr}
}

// errParse is returned when the grammar fails to produce a tree at all (as
// opposed to a tree with error nodes, which is normal and inspectable).
var errParse = errors.New("parse: grammar returned no tree")
