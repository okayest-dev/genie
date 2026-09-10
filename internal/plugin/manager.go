package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/tools"
)

const (
	MaxPlugins     = 16
	RequestTimeout = 5 * time.Second
	PingInterval   = 30 * time.Second
	ShutdownGrace  = 2 * time.Second
	ShutdownForce  = 2 * time.Second
)

// ReservedSlashNames are built-in REPL slash commands that plugins may not
// shadow. A plugin whose display name matches one of these is rejected at load.
var ReservedSlashNames = map[string]bool{
	"help":    true,
	"quit":    true,
	"exit":    true,
	"new":     true,
	"changes": true,
	"model":   true,
	"agent":   true,
}

type Plugin struct {
	Name         string
	Path         string
	Manifest     *Manifest
	Capabilities Capabilities
	Tools        []ToolDef
	Commands     []CommandDef
	Models       []ModelDef
	Cmd          *exec.Cmd
	Codec        *Codec
	mu           sync.Mutex
	Active       bool
	Cancel       context.CancelFunc
	Done         chan struct{}
	wg           sync.WaitGroup
}

type Manager struct {
	plugins      map[string]*Plugin
	pluginOrder  []string
	pluginsMu    sync.RWMutex
	toolReg      *tools.Registry
	wireRegistry map[llm.Wire]llm.Factory
	pluginDir    string
	enableList   []string
	disableList  []string
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
}

func NewManager(pluginDir string, enableList, disableList []string, toolReg *tools.Registry) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		plugins:      make(map[string]*Plugin),
		toolReg:      toolReg,
		wireRegistry: make(map[llm.Wire]llm.Factory),
		pluginDir:    pluginDir,
		enableList:   enableList,
		disableList:  disableList,
		ctx:          ctx,
		cancel:       cancel,
	}
}

func (m *Manager) LoadPlugins() error {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("plugin directory does not exist, skipping", "dir", m.pluginDir)
			return nil
		}
		return fmt.Errorf("read plugin dir: %w", err)
	}

	var pluginPaths []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		if e.IsDir() {
			// Directory layout: plugins/<name>/<name> (binary)
			bin := filepath.Join(m.pluginDir, name, name)
			info, err := os.Stat(bin)
			if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
				continue
			}
			pluginPaths = append(pluginPaths, bin)
			continue
		}

		// Flat layout (backward compat): plugins/<name> (binary)
		full := filepath.Join(m.pluginDir, name)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.Mode()&0111 == 0 {
			continue
		}
		if filepath.Ext(name) == ".go" {
			continue
		}
		pluginPaths = append(pluginPaths, full)
	}

	if len(pluginPaths) > MaxPlugins {
		slog.Warn("too many plugins discovered, limiting", "found", len(pluginPaths), "max", MaxPlugins)
		pluginPaths = pluginPaths[:MaxPlugins]
	}

	for _, path := range pluginPaths {
		if m.isDisabled(filepath.Base(path)) {
			slog.Info("plugin disabled by config, skipping", "plugin", path)
			continue
		}
		if len(m.enableList) > 0 && !m.isEnabled(filepath.Base(path)) {
			slog.Info("plugin not in enable list, skipping", "plugin", path)
			continue
		}
		// Discovery order is deterministic (the directory listing is sorted);
		// record it before spawning the loader goroutine so the context-hook
		// chain's default order is stable regardless of concurrent loads.
		m.pluginsMu.Lock()
		m.pluginOrder = append(m.pluginOrder, filepath.Base(path))
		m.pluginsMu.Unlock()
		m.wg.Add(1)
		go func(p string) {
			defer m.wg.Done()
			if err := m.loadPlugin(p); err != nil {
				slog.Warn("failed to load plugin", "plugin", p, "error", err)
			}
		}(path)
	}

	m.wg.Wait()
	return nil
}

func (m *Manager) isDisabled(name string) bool {
	for _, d := range m.disableList {
		if d == name {
			return true
		}
	}
	return false
}

func (m *Manager) isEnabled(name string) bool {
	for _, e := range m.enableList {
		if e == name {
			return true
		}
	}
	return false
}

func (m *Manager) loadPlugin(path string) error {
	name := filepath.Base(path)
	slog.Info("loading plugin", "name", name, "path", path)

	manifest, err := ParseManifest(m.pluginDir, name)
	if err != nil {
		slog.Warn("failed to parse manifest, will probe plugin", "plugin", name, "error", err)
	}

	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "GENIE_PLUGIN=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start plugin: %w", err)
	}

	slog.Info("plugin started", "name", name, "pid", cmd.Process.Pid)

	ctx, cancel := context.WithCancel(m.ctx)
	codec := NewCodec(stdout, stdin)
	p := &Plugin{
		Name:     name,
		Path:     path,
		Manifest: manifest,
		Cmd:      cmd,
		Codec:    codec,
		Active:   true,
		Cancel:   cancel,
		Done:     make(chan struct{}),
	}

	m.pluginsMu.Lock()
	m.plugins[name] = p
	m.pluginsMu.Unlock()

	p.wg.Add(1)
	go p.monitor(ctx)

	if err := p.handshake(); err != nil {
		p.Close()
		return fmt.Errorf("handshake: %w", err)
	}

	if p.Capabilities.Tools {
		if err := p.loadTools(); err != nil {
			p.Close()
			return fmt.Errorf("load tools: %w", err)
		}
		if err := m.registerPluginTools(p); err != nil {
			p.Close()
			return fmt.Errorf("register tools: %w", err)
		}
	}

	if p.Capabilities.Commands {
		if ReservedSlashNames[name] {
			slog.Warn("plugin name collides with reserved built-in slash command, skipping", "name", name)
			p.Close()
			m.pluginsMu.Lock()
			delete(m.plugins, name)
			m.pluginsMu.Unlock()
			return nil
		}
		if err := p.loadCommands(); err != nil {
			p.Close()
			return fmt.Errorf("load commands: %w", err)
		}
	}

	if p.Capabilities.Wires {
		if err := p.loadWires(); err != nil {
			p.Close()
			return fmt.Errorf("load wires: %w", err)
		}
	}

	slog.Info("plugin loaded", "name", name, "tools", len(p.Tools), "commands", len(p.Commands), "wires", p.Capabilities.Wires)
	return nil
}

func (p *Plugin) handshake() error {
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodCapabilitiesList,
		ID:      1,
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return err
	}

	respCh := make(chan *Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := p.Codec.ReadResponse()
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("capabilities/list timeout")
	case err := <-errCh:
		return err
	case resp := <-respCh:
		if resp.Error != nil {
			return fmt.Errorf("capabilities/list: %w", resp.Error)
		}
		var caps Capabilities
		if err := json.Unmarshal(resp.Result, &caps); err != nil {
			return fmt.Errorf("parse capabilities: %w", err)
		}
		if err := caps.Validate(); err != nil {
			return fmt.Errorf("invalid capabilities: %w", err)
		}
		p.Capabilities = caps
		return nil
	}
}

func (p *Plugin) loadTools() error {
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsList,
		ID:      2,
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return err
	}

	resp, err := p.Codec.ReadResponse()
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("tools/list: %w", resp.Error)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse tools/list: %w", err)
	}
	p.Tools = result.Tools
	return nil
}

func (p *Plugin) loadCommands() error {
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodCommandsList,
		ID:      2,
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return err
	}

	resp, err := p.Codec.ReadResponse()
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("commands/list: %w", resp.Error)
	}

	var result CommandsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse commands/list: %w", err)
	}
	p.Commands = normalizeCommands(result.Commands)
	return nil
}

// normalizeCommands applies the plugin protocol's command-name rules: a command
// whose name is not a single token (contains whitespace or a leading slash) is
// dropped with a warning, duplicate names resolve last-wins with a warning, and
// an empty list is tolerated.
func normalizeCommands(defs []CommandDef) []CommandDef {
	last := make(map[string]int)
	for i, def := range defs {
		if isSingleToken(def.Name) {
			last[def.Name] = i
		}
	}
	var out []CommandDef
	for i, def := range defs {
		if lastIdx, ok := last[def.Name]; ok {
			if lastIdx != i {
				slog.Warn("duplicate command name, last-wins", "name", def.Name)
				continue
			}
			out = append(out, def)
			continue
		}
		slog.Warn("dropping command with non-single-token name", "name", def.Name)
	}
	return out
}

func isSingleToken(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && !strings.ContainsAny(name, " \t\n")
}

func (p *Plugin) loadWires() error {
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodWireInit,
		Params:  mustMarshal(map[string]any{"config": map[string]any{}}),
		ID:      3,
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return err
	}

	resp, err := p.Codec.ReadResponse()
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("wire/init: %w", resp.Error)
	}

	var result WireInitResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse wire/init: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("wire/init failed")
	}

	models, err := p.listModels()
	if err != nil {
		slog.Warn("wire/list_models failed, continuing without models", "plugin", p.Name, "error", err)
	} else {
		p.Models = models
		slog.Info("plugin models loaded", "plugin", p.Name, "models", len(models))
	}

	return nil
}

func (p *Plugin) listModels() ([]ModelDef, error) {
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodWireListModels,
		ID:      4,
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	resp, err := p.Codec.ReadResponse()
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("wire/list_models: %w", resp.Error)
	}

	var result WireListModelsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse wire/list_models: %w", err)
	}
	return result.Models, nil
}

func (m *Manager) registerPluginTools(p *Plugin) error {
	for _, td := range p.Tools {
		if m.toolReg.IsDisabled(td.Name) {
			continue
		}
		if _, ok := m.toolReg.Get(td.Name); ok {
			slog.Warn("plugin tool collides with built-in, skipping", "tool", td.Name, "plugin", p.Name)
			continue
		}
		tool := newPluginTool(p, td)
		m.toolReg.Register(tool)
		slog.Info("registered plugin tool", "tool", td.Name, "plugin", p.Name)
	}
	return nil
}

func (p *Plugin) monitor(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- p.Cmd.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			p.Close()
			return
		case <-ticker.C:
			if !p.ping() {
				slog.Warn("plugin ping failed, marking inactive", "plugin", p.Name)
				p.Close()
				return
			}
		case err := <-waitCh:
			if err != nil {
				slog.Warn("plugin process exited", "plugin", p.Name, "error", err)
			}
			p.Active = false
			close(p.Done)
			return
		}
	}
}

func (p *Plugin) ping() bool {
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodPing,
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return false
	}

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		return false
	case resp := <-respCh:
		return resp != nil && resp.Error == nil
	}
}

func (p *Plugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return
	}
	p.Active = false

	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodShutdown,
		ID:      time.Now().UnixNano(),
	}
	p.Codec.WriteRequest(req)

	select {
	case <-ctx.Done():
	case <-p.Done:
		return
	}

	if p.Cmd.Process != nil {
		p.Cmd.Process.Signal(os.Interrupt)
		select {
		case <-time.After(ShutdownForce):
			p.Cmd.Process.Kill()
		case <-p.Done:
		}
	}
	p.Cancel()
}

func (p *Plugin) CallTool(name string, args map[string]any) (*ToolsCallResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return nil, fmt.Errorf("plugin %s is not active", p.Name)
	}

	params := ToolsCallParams{Name: name, Arguments: args}
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodToolsCall,
		Params:  mustMarshal(params),
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		p.Active = false
		return nil, fmt.Errorf("tool call timeout")
	case resp := <-respCh:
		if resp == nil {
			p.Active = false
			return nil, fmt.Errorf("plugin closed connection")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("tool call error: %s", resp.Error.Message)
		}
		var result ToolsCallResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("parse tool result: %w", err)
		}
		return &result, nil
	}
}

// CallCommandHelp requests curated help text from the plugin. The plugin may
// return a -32601 (method-not-found) error when it does not supply curated
// help, which callers should handle by falling back to the flat commands/list
// listing.
func (p *Plugin) CallCommandHelp(name string) (*CommandsHelpResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return nil, fmt.Errorf("plugin %s is not active", p.Name)
	}

	params := CommandsHelpParams{Name: name}
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodCommandsHelp,
		Params:  mustMarshal(params),
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		p.Active = false
		return nil, fmt.Errorf("command help timeout")
	case resp := <-respCh:
		if resp == nil {
			p.Active = false
			return nil, fmt.Errorf("plugin %s is not active", p.Name)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		var result CommandsHelpResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("parse command help result: %w", err)
		}
		return &result, nil
	}
}

func (p *Plugin) CallCommand(name, args string) (*CommandsRunResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return nil, fmt.Errorf("plugin %s is not active", p.Name)
	}

	params := CommandsRunParams{Name: name, Arguments: args}
	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodCommandsRun,
		Params:  mustMarshal(params),
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		p.Active = false
		return nil, fmt.Errorf("command run timeout")
	case resp := <-respCh:
		if resp == nil {
			p.Active = false
			return nil, fmt.Errorf("plugin %s is not active", p.Name)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("command run error: %s", resp.Error.Message)
		}
		var result CommandsRunResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, fmt.Errorf("parse command result: %w", err)
		}
		return &result, nil
	}
}

func (p *Plugin) StreamWire(request json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return nil, fmt.Errorf("plugin %s is not active", p.Name)
	}

	req := &Request{
		JSONRPC: "2.0",
		Method:  MethodWireStream,
		Params:  request,
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		p.Active = false
		return nil, fmt.Errorf("wire stream timeout")
	case resp := <-respCh:
		if resp == nil {
			p.Active = false
			return nil, fmt.Errorf("plugin closed connection")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("wire stream error: %s", resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// callContext performs a single context-hook RPC round-trip with the plugin
// and returns the raw result for the caller to decode. It holds the plugin's
// mutex so a hook cannot interleave with a tool call or stream.
func (p *Plugin) callContext(method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.Active {
		return nil, fmt.Errorf("plugin %s is not active", p.Name)
	}

	req := &Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustMarshal(params),
		ID:      time.Now().UnixNano(),
	}
	if err := p.Codec.WriteRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	respCh := make(chan *Response, 1)
	go func() {
		resp, _ := p.Codec.ReadResponse()
		respCh <- resp
	}()

	select {
	case <-ctx.Done():
		p.Active = false
		return nil, fmt.Errorf("context hook %s timeout", method)
	case resp := <-respCh:
		if resp == nil {
			p.Active = false
			return nil, fmt.Errorf("plugin closed connection")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("context hook %s error: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// callContextRewrite performs a request-rewriting context-hook round-trip
// (before_request / compact / condense). It invokes the plugin with the full
// request, decodes the returned {request: ...} wrapper, and converts it back to
// a llm.Request. On any failure it returns the original request unchanged, so a
// failing hook's contribution is dropped rather than partially applied. The
// decode target is the shared rewrite-result shape; the distinct named result
// types (ContextBeforeRequestResult, ContextCompactResult, ContextCondenseResult)
// are all identical to it, so this one decode serves all three.
func (p *Plugin) callContextRewrite(ctx context.Context, method, label string, req llm.Request) (llm.Request, error) {
	result, err := p.callContext(method, toContextRequest(req))
	if err != nil {
		return req, err
	}
	var out ContextBeforeRequestResult
	if err := json.Unmarshal(result, &out); err != nil {
		return req, fmt.Errorf("parse %s: %w", label, err)
	}
	return fromContextRequest(out.Request), nil
}

// CallContextBefore invokes a plugin's before_request hook, returning the
// possibly-rewritten request to continue the chain or forward.
func (p *Plugin) CallContextBefore(ctx context.Context, req llm.Request) (llm.Request, error) {
	return p.callContextRewrite(ctx, MethodContextBeforeRequest, "context/before_request", req)
}

// CallContextAfter invokes a plugin's after_response hook, which observes the
// completed turn and reports a narrow usage delta. It never rewrites history.
func (p *Plugin) CallContextAfter(ctx context.Context, req llm.Request, usage llm.Usage) (llm.Usage, error) {
	params := ContextAfterResponseParams{
		Request: toContextRequest(req),
		Usage:   Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens},
	}
	result, err := p.callContext(MethodContextAfterResponse, params)
	if err != nil {
		return usage, err
	}
	var out ContextAfterResponseResult
	if err := json.Unmarshal(result, &out); err != nil {
		return usage, fmt.Errorf("parse context/after_response: %w", err)
	}
	return llm.Usage{
		PromptTokens:     out.Usage.PromptTokens,
		CompletionTokens: out.Usage.CompletionTokens,
		TotalTokens:      out.Usage.TotalTokens,
	}, nil
}

// CallContextCompact invokes a plugin's compact hook, which summarises/evicts
// history into a rewritten request.
func (p *Plugin) CallContextCompact(ctx context.Context, req llm.Request) (llm.Request, error) {
	return p.callContextRewrite(ctx, MethodContextCompact, "context/compact", req)
}

// CallContextCondense invokes a plugin's condense hook, which narrows tool
// outputs in history into a rewritten request.
func (p *Plugin) CallContextCondense(ctx context.Context, req llm.Request) (llm.Request, error) {
	return p.callContextRewrite(ctx, MethodContextCondense, "context/condense", req)
}

// CallLifecycleRequestBuilt invokes a plugin's request_built hook: it may
// rewrite the fully-assembled turn request. fatal reports the plugin's opt-in
// turn-scoped abort escalation (og-cbu.3/og-cbu.4); a non-fatal error means the
// hook failed and its contribution should be dropped (degrade-by-default).
func (p *Plugin) CallLifecycleRequestBuilt(ctx context.Context, req llm.Request) (llm.Request, bool, error) {
	result, err := p.callContext(MethodLifecycleRequestBuilt, toContextRequest(req))
	if err != nil {
		return req, false, err
	}
	var out LifecycleRequestBuiltResult
	if err := json.Unmarshal(result, &out); err != nil {
		return req, false, fmt.Errorf("parse lifecycle/request_built: %w", err)
	}
	return fromContextRequest(out.Request), out.Fatal, nil
}

// CallLifecycleToolBefore invokes a plugin's tool_before guardrail hook, which
// may rewrite arguments and/or suppress the call. On success the returned
// result carries the rewrite/suppression/fatal flags; an error means the hook
// failed (dropped).
func (p *Plugin) CallLifecycleToolBefore(ctx context.Context, name, id, args string) (LifecycleToolBeforeResult, bool, error) {
	params := LifecycleToolBeforeParams{Name: name, ID: id, Arguments: args}
	result, err := p.callContext(MethodLifecycleToolBefore, params)
	if err != nil {
		return LifecycleToolBeforeResult{}, false, err
	}
	var out LifecycleToolBeforeResult
	if err := json.Unmarshal(result, &out); err != nil {
		return LifecycleToolBeforeResult{}, false, fmt.Errorf("parse lifecycle/tool_before: %w", err)
	}
	return out, out.Fatal, nil
}

// CallLifecycleToolAfter invokes a plugin's tool_after hook, which observes a
// completed tool call (err is a field) and may rewrite the result text.
func (p *Plugin) CallLifecycleToolAfter(ctx context.Context, name, id, args, result, errText string) (LifecycleToolAfterResult, bool, error) {
	params := LifecycleToolAfterParams{Name: name, ID: id, Arguments: args, Result: result, Error: errText}
	raw, err := p.callContext(MethodLifecycleToolAfter, params)
	if err != nil {
		return LifecycleToolAfterResult{}, false, err
	}
	var out LifecycleToolAfterResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return LifecycleToolAfterResult{}, false, fmt.Errorf("parse lifecycle/tool_after: %w", err)
	}
	return out, out.Fatal, nil
}

// CallLifecycleResponseReady invokes a plugin's response_ready hook for one text
// delta (or the final release, Final=true, which carries finish reason + usage).
func (p *Plugin) CallLifecycleResponseReady(ctx context.Context, chunk string, final bool, finish llm.FinishReason, usage llm.Usage) (LifecycleResponseReadyResult, bool, error) {
	params := LifecycleResponseReadyParams{
		Chunk:        chunk,
		Final:        final,
		FinishReason: string(finish),
		Usage:        Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens},
	}
	raw, err := p.callContext(MethodLifecycleResponseReady, params)
	if err != nil {
		return LifecycleResponseReadyResult{}, false, err
	}
	var out LifecycleResponseReadyResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return LifecycleResponseReadyResult{}, false, fmt.Errorf("parse lifecycle/response_ready: %w", err)
	}
	return out, out.Fatal, nil
}

// CallLifecycleTurnError invokes a plugin's observe-only turn_error hook.
func (p *Plugin) CallLifecycleTurnError(ctx context.Context, errText, phase, partial string) (bool, error) {
	params := LifecycleTurnErrorParams{Error: errText, Phase: phase, Partial: partial}
	raw, err := p.callContext(MethodLifecycleTurnError, params)
	if err != nil {
		return false, err
	}
	var out LifecycleTurnErrorResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("parse lifecycle/turn_error: %w", err)
	}
	return out.Fatal, nil
}

func (m *Manager) GetPlugins() map[string]*Plugin {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	result := make(map[string]*Plugin, len(m.plugins))
	for k, v := range m.plugins {
		result[k] = v
	}
	return result
}

// PluginsInOrder returns the successfully-loaded plugins in discovery
// (registration) order, which is deterministic and drives the default
// context-hook chain order.
func (m *Manager) PluginsInOrder() []*Plugin {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	var out []*Plugin
	for _, name := range m.pluginOrder {
		if p, ok := m.plugins[name]; ok {
			out = append(out, p)
		}
	}
	return out
}

func (m *Manager) RegisterToolFactory(factory func() tools.Tool) {
	if factory != nil {
		m.toolReg.Register(factory())
	}
}

func (m *Manager) RegisterWire(name llm.Wire, factory llm.Factory) error {
	if _, ok := m.wireRegistry[name]; ok {
		return fmt.Errorf("wire %q already registered", name)
	}
	m.wireRegistry[name] = factory
	llm.RegisterWire(name, factory)
	return nil
}

func (m *Manager) GetWireRegistry() map[llm.Wire]llm.Factory {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	result := make(map[llm.Wire]llm.Factory, len(m.wireRegistry))
	for k, v := range m.wireRegistry {
		result[k] = v
	}
	return result
}

func (m *Manager) Shutdown() {
	m.cancel()
	m.wg.Wait()

	m.pluginsMu.Lock()
	defer m.pluginsMu.Unlock()

	for _, p := range m.plugins {
		p.Close()
	}
}

type pluginTool struct {
	plugin *Plugin
	def    ToolDef
}

func newPluginTool(p *Plugin, def ToolDef) *pluginTool {
	return &pluginTool{plugin: p, def: def}
}

func (t *pluginTool) Name() string        { return t.def.Name }
func (t *pluginTool) Description() string { return t.def.Description }
func (t *pluginTool) Parameters() map[string]any {
	return t.def.Parameters
}
func (t *pluginTool) Execute(args json.RawMessage) (string, error) {
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("unmarshal args: %w", err)
	}
	result, err := t.plugin.CallTool(t.def.Name, params)
	if err != nil {
		return "", err
	}
	if len(result.Content) == 0 {
		return "", nil
	}
	return result.Content[0].Text, nil
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
