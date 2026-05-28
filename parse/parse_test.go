package parse_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

func mustParser(t *testing.T) *parse.Parser {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func TestParseRoot(t *testing.T) {
	src, err := os.ReadFile("testdata/hello.m")
	if err != nil {
		t.Fatal(err)
	}
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tree.Close()

	root := tree.RootNode()
	if got := root.Type(); got != "source_file" {
		t.Errorf("root type = %q, want %q", got, "source_file")
	}
	if root.HasError() {
		t.Errorf("valid routine parsed with errors:\n%s", root.SExpr())
	}
	if root.ChildCount() == 0 {
		t.Fatal("root has no children")
	}
	if root.StartByte() != 0 || root.EndByte() != uint32(len(src)) {
		t.Errorf("root span = [%d,%d), want [0,%d)", root.StartByte(), root.EndByte(), len(src))
	}
}

func TestNamedChildrenAreLines(t *testing.T) {
	src, _ := os.ReadFile("testdata/hello.m")
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	root := tree.RootNode()
	var lines int
	for i := uint32(0); i < root.NamedChildCount(); i++ {
		if root.NamedChild(i).Type() == "line" {
			lines++
		}
	}
	if lines == 0 {
		t.Fatalf("expected at least one 'line' node under root; sexpr:\n%s", root.SExpr())
	}
}

func TestNodeTextMatchesSource(t *testing.T) {
	src := []byte("EN ; entry\n quit\n")
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	// Every node's Text() must equal the source slice at its byte span.
	root := tree.RootNode()
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		got := string(n.Text())
		want := string(src[n.StartByte():n.EndByte()])
		if got != want {
			t.Errorf("node %q Text()=%q, want %q", n.Type(), got, want)
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

func TestSExprNonEmpty(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), []byte("EN ;\n quit\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	s := tree.RootNode().SExpr()
	if !strings.HasPrefix(s, "(source_file") {
		t.Errorf("sexpr = %q, want it to start with (source_file", s)
	}
}

func TestGrammarABIVersion(t *testing.T) {
	p := mustParser(t)
	if v := p.GrammarABIVersion(); v != 15 {
		t.Errorf("grammar ABI version = %d, want 15", v)
	}
	if p.SymbolCount() == 0 {
		t.Error("symbol count = 0")
	}
}

func TestGrammarHashStable(t *testing.T) {
	h := parse.GrammarHash()
	if !strings.HasPrefix(h, "sha256:") || len(h) != len("sha256:")+64 {
		t.Errorf("GrammarHash = %q, want sha256:<64 hex>", h)
	}
}

func TestParserReuse(t *testing.T) {
	p := mustParser(t)
	for i, src := range []string{"EN ;\n quit\n", "X ;\n write 1\n", ""} {
		tree, err := p.Parse(context.Background(), []byte(src))
		if err != nil {
			t.Fatalf("parse #%d: %v", i, err)
		}
		if tree.RootNode().Type() != "source_file" {
			t.Errorf("parse #%d: root not source_file", i)
		}
		tree.Close()
	}
}

func TestEmptyInput(t *testing.T) {
	p := mustParser(t)
	tree, err := p.Parse(context.Background(), nil)
	if err != nil {
		t.Fatalf("Parse(nil): %v", err)
	}
	defer tree.Close()
	if tree.RootNode().Type() != "source_file" {
		t.Error("empty input: root not source_file")
	}
}
