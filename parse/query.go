package parse

import (
	"context"
	"fmt"
)

// Query is a compiled tree-sitter query against the M grammar. Compile one with
// Parser.NewQuery, run it over a Node with Matches, and Close it when done.
// Queries use tree-sitter's S-expression pattern syntax with @captures, e.g.
//
//	(command (command_keyword) @kw)
//
// This is the substrate lint/lsp build their rules on (spec §4/§6).
type Query struct {
	p         *Parser
	ptr       uint32 // TSQuery*
	nameCache map[uint32]string
}

// Capture is a captured node together with its @name from the query.
type Capture struct {
	Name string
	Node Node
}

// Match is one match of a query pattern: the pattern's index plus its captures
// in capture order.
type Match struct {
	PatternIndex uint32
	Captures     []Capture
}

var queryErrNames = map[uint32]string{
	1: "syntax", 2: "node-type", 3: "field", 4: "capture", 5: "structure", 6: "language",
}

// NewQuery compiles a tree-sitter query against the M grammar. A compile error
// is returned with its kind and byte offset.
func (p *Parser) NewQuery(source string) (*Query, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bg := context.Background()

	src := []byte(source)
	n := uint32(len(src))
	var srcPtr uint32
	if n > 0 {
		r, err := p.call(bg, "tsm_malloc", uint64(n))
		if err != nil {
			return nil, err
		}
		srcPtr = uint32(r[0])
		if srcPtr == 0 || !p.memory.Write(srcPtr, src) {
			return nil, fmt.Errorf("parse: query source alloc/write failed")
		}
	}

	// scratch holds two out-params: err_offset (u32) at +0, err_type (u32) at +4.
	sr, err := p.call(bg, "tsm_malloc", 8)
	if err != nil {
		return nil, err
	}
	scratch := uint32(sr[0])
	defer func() { _, _ = p.call(bg, "tsm_free", uint64(scratch)) }()

	r, err := p.call(bg, "tsm_query_new", uint64(srcPtr), uint64(n), uint64(scratch), uint64(scratch+4))
	if srcPtr != 0 {
		_, _ = p.call(bg, "tsm_free", uint64(srcPtr))
	}
	if err != nil {
		return nil, err
	}
	qptr := uint32(r[0])
	if qptr == 0 {
		off, _ := p.memory.ReadUint32Le(scratch)
		et, _ := p.memory.ReadUint32Le(scratch + 4)
		kind := queryErrNames[et]
		if kind == "" {
			kind = "unknown"
		}
		return nil, fmt.Errorf("parse: query compile error (%s) at byte %d", kind, off)
	}
	return &Query{p: p, ptr: qptr, nameCache: map[uint32]string{}}, nil
}

// Close frees the compiled query.
func (q *Query) Close() {
	if q.p == nil || q.ptr == 0 {
		return
	}
	q.p.mu.Lock()
	defer q.p.mu.Unlock()
	_, _ = q.p.call(context.Background(), "tsm_query_delete", uint64(q.ptr))
	q.ptr = 0
}

// Matches runs the query over the subtree rooted at n and returns its matches,
// grouped per match in document order. Captured nodes belong to n's Tree and
// stay valid until that Tree is closed.
func (q *Query) Matches(n Node) []Match {
	p := q.p
	p.mu.Lock()
	defer p.mu.Unlock()
	bg := context.Background()

	cur := uint32(p.must1("tsm_query_cursor_new"))
	defer func() { _, _ = p.call(bg, "tsm_query_cursor_delete", uint64(cur)) }()
	_, _ = p.call(bg, "tsm_query_cursor_exec", uint64(cur), uint64(q.ptr), uint64(n.ptr))

	// scratch: out_node, out_capture_id, out_match_id, out_pattern (4×u32).
	scratch := uint32(p.must1("tsm_malloc", 16))
	defer func() { _, _ = p.call(bg, "tsm_free", uint64(scratch)) }()

	var order []uint32
	byMatch := map[uint32]*Match{}
	for {
		r, err := p.call(bg, "tsm_query_cursor_next_capture",
			uint64(cur), uint64(scratch), uint64(scratch+4), uint64(scratch+8), uint64(scratch+12))
		if err != nil || len(r) == 0 || uint32(r[0]) == 0 {
			break
		}
		nodePtr, _ := p.memory.ReadUint32Le(scratch)
		capID, _ := p.memory.ReadUint32Le(scratch + 4)
		matchID, _ := p.memory.ReadUint32Le(scratch + 8)
		pattern, _ := p.memory.ReadUint32Le(scratch + 12)

		n.t.nodes = append(n.t.nodes, nodePtr) // freed on Tree.Close
		cp := Capture{Name: q.captureName(capID), Node: Node{t: n.t, ptr: nodePtr}}

		m := byMatch[matchID]
		if m == nil {
			order = append(order, matchID)
			m = &Match{PatternIndex: pattern}
			byMatch[matchID] = m
		}
		m.Captures = append(m.Captures, cp)
	}

	out := make([]Match, 0, len(order))
	for _, id := range order {
		out = append(out, *byMatch[id])
	}
	return out
}

// captureName resolves (and caches) the @name for a capture id. The capture
// name bytes are not NUL-terminated, so the length is read from the out-param.
// Caller holds p.mu.
func (q *Query) captureName(id uint32) string {
	if name, ok := q.nameCache[id]; ok {
		return name
	}
	p := q.p
	bg := context.Background()
	scratch := uint32(p.must1("tsm_malloc", 4))
	defer func() { _, _ = p.call(bg, "tsm_free", uint64(scratch)) }()

	name := ""
	if r, err := p.call(bg, "tsm_query_capture_name", uint64(q.ptr), uint64(id), uint64(scratch)); err == nil && len(r) > 0 {
		ptr := uint32(r[0])
		ln, _ := p.memory.ReadUint32Le(scratch)
		if ptr != 0 && ln > 0 {
			if b, ok := p.memory.Read(ptr, ln); ok {
				name = string(b)
			}
		}
	}
	q.nameCache[id] = name
	return name
}
