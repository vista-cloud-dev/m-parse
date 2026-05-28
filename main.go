// Command m-parse is the spike/reference CLI for the m-parse substrate (spec
// §4): it runs the embedded tree-sitter-m grammar through wazero (pure-Go, no
// CGO) and parses M source into a syntax tree. The real deliverable is the
// importable parse package; this CLI exercises it end-to-end and inherits the
// shared clikit conventions (--output text|json, schema, deterministic errors).
//
// Try:
//
//	m-parse parse routine.m            # styled tree summary + s-expression
//	m-parse parse routine.m -o json    # machine surface: counts + error diags
//	m-parse parse routine.m --check    # exit 3 if the parse has syntax errors
//	m-parse version                    # build info + embedded grammar hash
//	m-parse schema | jq .
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-parse/clikit"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// CLI is the root command grammar (one typed struct; spec §5).
type CLI struct {
	clikit.Globals

	Parse   parseCmd         `cmd:"" help:"Parse an M (.m/.mac) file and report its syntax tree."`
	Version versionCmd       `cmd:"" help:"Show version, Go toolchain, and embedded grammar hash."`
	Schema  clikit.SchemaCmd `cmd:"" help:"Emit the command/flag/enum tree as JSON (agent discovery)."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"m-parse",
		"m-parse — the engine-neutral M parse substrate (tree-sitter-m via wazero, no CGO).",
		cli, &cli.Globals,
	))
}

// --- parse -------------------------------------------------------------------

type parseCmd struct {
	File  string `arg:"" type:"existingfile" help:"M source file to parse (.m or .mac)."`
	Check bool   `help:"Exit 3 if the parse contains syntax errors (CI gate)."`
	Sexpr bool   `help:"Print the full s-expression (text mode; on by default when not piped)."`
}

type nodeSpan struct {
	Type   string `json:"type"`
	Start  uint32 `json:"start"`
	End    uint32 `json:"end"`
	Row    uint32 `json:"row"`
	Column uint32 `json:"column"`
}

type parseResult struct {
	File            string     `json:"file"`
	Root            string     `json:"root"`
	ChildCount      uint32     `json:"childCount"`
	NamedChildCount uint32     `json:"namedChildCount"`
	Bytes           int        `json:"bytes"`
	HasError        bool       `json:"hasError"`
	GrammarHash     string     `json:"grammarHash"`
	ABIVersion      uint32     `json:"abiVersion"`
	Errors          []nodeSpan `json:"errors,omitempty"`
}

func (c *parseCmd) Run(cc *clikit.Context) error {
	src, err := os.ReadFile(c.File)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "READ_FAILED", err.Error(), "")
	}

	ctx := context.Background()
	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(ctx) }()

	tree, err := p.Parse(ctx, src)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSE_FAILED", err.Error(), "")
	}
	defer tree.Close()

	root := tree.RootNode()
	res := parseResult{
		File:            c.File,
		Root:            root.Type(),
		ChildCount:      root.ChildCount(),
		NamedChildCount: root.NamedChildCount(),
		Bytes:           len(src),
		HasError:        root.HasError(),
		GrammarHash:     parse.GrammarHash(),
		ABIVersion:      p.GrammarABIVersion(),
		Errors:          collectErrors(root),
	}

	emit := func() {
		cc.Title("parse")
		cc.KV(
			[2]string{"file", cc.Accent(res.File)},
			[2]string{"root", res.Root},
			[2]string{"nodes", fmt.Sprintf("%d (%d named)", res.ChildCount, res.NamedChildCount)},
			[2]string{"bytes", fmt.Sprintf("%d", res.Bytes)},
			[2]string{"errors", fmt.Sprintf("%d", len(res.Errors))},
		)
		if res.HasError {
			fmt.Fprintln(cc.Stdout, cc.Failure(fmt.Sprintf("%d syntax error node(s)", len(res.Errors))))
			for _, e := range res.Errors {
				fmt.Fprintf(cc.Stdout, "  %s %d:%d  %s\n", cc.Faint("x"), e.Row+1, e.Column+1, e.Type)
			}
		} else {
			fmt.Fprintln(cc.Stdout, cc.Success("no syntax errors"))
		}
		if c.Sexpr || !cc.Color {
			cc.Rule("s-expression")
			fmt.Fprintln(cc.Stdout, root.SExpr())
		}
	}

	if err := cc.Result(res, emit); err != nil {
		return err
	}
	if c.Check && res.HasError {
		return clikit.Fail(clikit.ExitCheck, "SYNTAX_ERRORS",
			fmt.Sprintf("%d syntax error node(s) in %s", len(res.Errors), c.File), "")
	}
	return nil
}

// collectErrors walks the tree and records every error/missing node.
func collectErrors(n parse.Node) []nodeSpan {
	var out []nodeSpan
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		if n.IsError() || n.IsMissing() {
			sp := n.StartPoint()
			out = append(out, nodeSpan{
				Type: n.Type(), Start: n.StartByte(), End: n.EndByte(),
				Row: sp.Row, Column: sp.Column,
			})
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return out
}

// --- version -----------------------------------------------------------------

type versionCmd struct{}

type versionInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Date        string `json:"date"`
	Go          string `json:"go"`
	GrammarHash string `json:"grammarHash"`
	ABIVersion  uint32 `json:"grammarAbiVersion"`
}

func (versionCmd) Run(cc *clikit.Context) error {
	abi := uint32(0)
	if p, err := parse.New(context.Background()); err == nil {
		abi = p.GrammarABIVersion()
		_ = p.Close(context.Background())
	}
	info := versionInfo{
		Version: clikit.Version, Commit: clikit.Commit, Date: clikit.Date,
		Go: runtime.Version(), GrammarHash: parse.GrammarHash(), ABIVersion: abi,
	}
	return cc.Result(info, func() {
		cc.KV(
			[2]string{"version", cc.Accent(info.Version)},
			[2]string{"commit", info.Commit},
			[2]string{"built", info.Date},
			[2]string{"go", info.Go},
			[2]string{"grammar", info.GrammarHash},
			[2]string{"grammar ABI", fmt.Sprintf("%d", info.ABIVersion)},
		)
	})
}
