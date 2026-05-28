package parse_test

import (
	"context"
	"testing"
)

func TestQueryCapturesCommandKeywords(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n new x set x=1 write x quit\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	q, err := p.NewQuery("(command_keyword) @kw")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer q.Close()

	matches := q.Matches(tree.RootNode())
	var kws []string
	for _, m := range matches {
		for _, c := range m.Captures {
			if c.Name != "kw" {
				t.Errorf("capture name = %q, want kw", c.Name)
			}
			kws = append(kws, string(c.Node.Text()))
		}
	}
	// new, set, write, quit — 4 command keywords.
	if len(kws) != 4 {
		t.Fatalf("got %d keyword captures %v, want 4", len(kws), kws)
	}
	want := map[string]bool{"new": true, "set": true, "write": true, "quit": true}
	for _, k := range kws {
		if !want[k] {
			t.Errorf("unexpected keyword %q", k)
		}
	}
}

func TestQueryMultiCaptureMatch(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n set x=1\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	q, err := p.NewQuery("(command (command_keyword) @kw (argument_list) @args)")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer q.Close()

	matches := q.Matches(tree.RootNode())
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1; tree:\n%s", len(matches), tree.RootNode().SExpr())
	}
	got := map[string]string{}
	for _, c := range matches[0].Captures {
		got[c.Name] = string(c.Node.Text())
	}
	if got["kw"] != "set" {
		t.Errorf("kw = %q, want set", got["kw"])
	}
	if got["args"] != "x=1" {
		t.Errorf("args = %q, want x=1", got["args"])
	}
}

func TestQueryNoMatches(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n quit\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	// string_literal never appears in this source.
	q, err := p.NewQuery("(string) @s")
	if err != nil {
		t.Fatalf("NewQuery: %v", err)
	}
	defer q.Close()

	if got := q.Matches(tree.RootNode()); len(got) != 0 {
		t.Errorf("got %d matches, want 0", len(got))
	}
}

func TestQueryCompileError(t *testing.T) {
	p := mustParser(t)
	if _, err := p.NewQuery("(command_keyword"); err == nil {
		t.Error("expected a compile error for an unbalanced query")
	}
	if _, err := p.NewQuery("(no_such_node_type) @x"); err == nil {
		t.Error("expected a compile error for an unknown node type")
	}
}
