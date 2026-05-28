package parse_test

import (
	"context"
	"os"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// findFirst returns the first node (pre-order) satisfying pred, or nil.
func findFirst(n parse.Node, pred func(parse.Node) bool) *parse.Node {
	if pred(n) {
		hit := n
		return &hit
	}
	for i := uint32(0); i < n.ChildCount(); i++ {
		if f := findFirst(n.Child(i), pred); f != nil {
			return f
		}
	}
	return nil
}

// Routine source is parsed by content, not file extension. The same M parses
// identically as .m, .mac (IRIS UDL), or .int. VistA loaded via ^%RI stores
// its routine source as .int — there .int IS the source (not compiler output,
// as it is for ObjectScript) — so the substrate must parse .int content as M.
func TestParsesRoutineFixtures(t *testing.T) {
	p := mustParser(t)
	for _, f := range []string{"testdata/vista.int", "testdata/iris.mac", "testdata/hello.m"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := p.Parse(context.Background(), src)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		root := tree.RootNode()
		if root.Type() != "source_file" {
			t.Errorf("%s: root = %q, want source_file", f, root.Type())
		}
		if root.HasError() {
			t.Errorf("%s: parsed with errors:\n%s", f, root.SExpr())
		}
		if root.NamedChildCount() == 0 {
			t.Errorf("%s: no named children", f)
		}
		tree.Close()
	}
}

// Parse keys off bytes only — the filename/extension is irrelevant — so the
// same bytes always yield the same tree.
func TestParseIsContentBased(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n N X S X=1 Q\n")
	a, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.RootNode().SExpr() != b.RootNode().SExpr() {
		t.Error("identical bytes produced different trees")
	}
}

// An unterminated string makes the grammar insert a MISSING closing quote.
func TestErrorDetectionMissing(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n W \"oops\n Q\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	root := tree.RootNode()
	if !root.HasError() {
		t.Fatalf("expected HasError on unterminated string; sexpr:\n%s", root.SExpr())
	}
	if findFirst(root, func(n parse.Node) bool { return n.IsMissing() }) == nil {
		t.Errorf("expected a MISSING node; sexpr:\n%s", root.SExpr())
	}
}

// Garbage in an expression yields an ERROR node the grammar can't reduce.
func TestErrorDetectionErrorNode(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n S X=@#$%\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	root := tree.RootNode()
	if !root.HasError() {
		t.Fatalf("expected HasError on bad expression; sexpr:\n%s", root.SExpr())
	}
	if findFirst(root, func(n parse.Node) bool { return n.IsError() }) == nil {
		t.Errorf("expected an ERROR node; sexpr:\n%s", root.SExpr())
	}
}

// Exercise the leaf node accessors (spans, symbol, named/anonymous, fields).
func TestNodeAccessors(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n N X\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	root := tree.RootNode()

	if root.IsNull() {
		t.Error("root reports IsNull")
	}
	if !root.IsNamed() {
		t.Error("source_file should be a named node")
	}
	if root.Symbol() == 0 {
		t.Error("root Symbol = 0")
	}
	if got := root.StartPoint(); got != (parse.Point{Row: 0, Column: 0}) {
		t.Errorf("root StartPoint = %+v, want {0 0}", got)
	}
	if end := root.EndPoint(); end.Row == 0 && end.Column == 0 {
		t.Error("root EndPoint did not advance past the start")
	}

	// Find an anonymous token, and exercise FieldNameForChild + Symbol on every
	// child. The grammar defines no fields, so FieldNameForChild is always "".
	var sawAnon bool
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		for i := uint32(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if !c.IsNamed() {
				sawAnon = true
			}
			if fn := n.FieldNameForChild(i); fn != "" {
				t.Errorf("FieldNameForChild = %q, want empty (grammar defines no fields)", fn)
			}
			_ = c.Symbol()
			walk(c)
		}
	}
	walk(root)
	if !sawAnon {
		t.Error("expected at least one anonymous token node")
	}
}
