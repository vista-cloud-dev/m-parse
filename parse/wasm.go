package parse

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Parser owns one wazero instance of the grammar WASM. Parse and the Node/Tree
// accessors funnel through it; wazero modules are not reentrant, so every
// WASM-touching operation holds mu. Construct with New; release with Close.
// One Parser can produce many Trees sequentially.
type Parser struct {
	mu      sync.Mutex
	ctx     context.Context
	runtime wazero.Runtime
	mod     api.Module
	memory  api.Memory
	fns     map[string]api.Function
	parser  uint32 // TSParser* in wasm memory
}

// New instantiates the embedded grammar WASM in a fresh wazero runtime
// (pure-Go, CGO_ENABLED=0). The ctx is retained for subsequent WASM calls.
func New(ctx context.Context) (*Parser, error) {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	// emscripten emits an env import for the memory-growth callback even in
	// STANDALONE_WASM; a no-op satisfies it.
	if _, err := rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(func(uint32) {}).
		Export("emscripten_notify_memory_growth").
		Instantiate(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("parse: build env module: %w", err)
	}

	// The module is an emscripten reactor: no _start; call _initialize manually.
	cfg := wazero.NewModuleConfig().WithName("tree-sitter-m").WithStartFunctions()
	mod, err := rt.InstantiateWithConfig(ctx, grammarWASM, cfg)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("parse: instantiate grammar wasm: %w", err)
	}

	p := &Parser{
		ctx:     ctx,
		runtime: rt,
		mod:     mod,
		memory:  mod.Memory(),
		fns:     make(map[string]api.Function),
	}

	if init := mod.ExportedFunction("_initialize"); init != nil {
		if _, err := init.Call(ctx); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("parse: _initialize: %w", err)
		}
	}

	r, err := p.call("tsm_parser_new")
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if uint32(r[0]) == 0 {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("parse: tsm_parser_new returned null (grammar language not set)")
	}
	p.parser = uint32(r[0])
	return p, nil
}

// Close deletes the parser and tears down the WASM runtime.
func (p *Parser) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.parser != 0 {
		_, _ = p.call("tsm_parser_delete", uint64(p.parser))
		p.parser = 0
	}
	return p.runtime.Close(ctx)
}

// Parse parses src as a fresh M document and returns its syntax tree. A tree
// with syntax errors is returned normally (inspect via Node.HasError); only a
// total grammar failure yields an error. The caller should Close the Tree.
func (p *Parser) Parse(ctx context.Context, src []byte) (*Tree, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := uint32(len(src))
	var buf uint32
	if n > 0 {
		r, err := p.call("tsm_malloc", uint64(n))
		if err != nil {
			return nil, err
		}
		buf = uint32(r[0])
		if buf == 0 {
			return nil, fmt.Errorf("parse: wasm malloc(%d) failed", n)
		}
		if !p.memory.Write(buf, src) {
			_, _ = p.call("tsm_free", uint64(buf))
			return nil, fmt.Errorf("parse: writing %d source bytes to wasm memory failed", n)
		}
	}

	r, err := p.call("tsm_parse", uint64(p.parser), uint64(buf), uint64(n))
	if buf != 0 {
		_, _ = p.call("tsm_free", uint64(buf))
	}
	if err != nil {
		return nil, err
	}
	tree := uint32(r[0])
	if tree == 0 {
		return nil, errParse
	}

	cp := make([]byte, len(src))
	copy(cp, src)
	return &Tree{p: p, ptr: tree, src: cp}, nil
}

// GrammarABIVersion is the tree-sitter language ABI version of the embedded grammar.
func (p *Parser) GrammarABIVersion() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return uint32(p.must1("tsm_language_abi_version"))
}

// SymbolCount is the number of grammar symbols (rules + tokens).
func (p *Parser) SymbolCount() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return uint32(p.must1("tsm_language_symbol_count"))
}

// --- raw WASM call/memory helpers (no locking; callers must hold p.mu) ------

func (p *Parser) call(name string, args ...uint64) ([]uint64, error) {
	fn := p.fns[name]
	if fn == nil {
		fn = p.mod.ExportedFunction(name)
		if fn == nil {
			return nil, fmt.Errorf("parse: missing wasm export %q", name)
		}
		p.fns[name] = fn
	}
	return fn.Call(p.ctx, args...)
}

// must1 calls a single-result function; a trap here means a binding/shim bug,
// so it panics rather than poisoning the clean Node API with errors.
func (p *Parser) must1(name string, args ...uint64) uint64 {
	r, err := p.call(name, args...)
	if err != nil {
		panic(fmt.Sprintf("parse: %s: %v", name, err))
	}
	if len(r) == 0 {
		return 0
	}
	return r[0]
}

// readCStr reads a NUL-terminated C string from wasm memory at ptr.
func (p *Parser) readCStr(ptr uint32) string {
	if ptr == 0 {
		return ""
	}
	var b []byte
	for off := ptr; ; off++ {
		c, ok := p.memory.ReadByte(off)
		if !ok || c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}
