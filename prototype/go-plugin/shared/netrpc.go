package shared

import (
	"fmt"
	"net/rpc"

	goplugin "github.com/hashicorp/go-plugin"
)

// OGPluginNetRPC is the go-plugin adapter that exposes OGPlugin over net/rpc.
// This file is imported by both host and plugin — it's the shared RPC wiring.
type OGPluginNetRPC struct {
	goplugin.Plugin
	// Impl is the concrete implementation, set during handshake.
	Impl OGPlugin
}

// Server returns an RPC-compatible server for the host to call.
func (p *OGPluginNetRPC) Server(*goplugin.MuxBroker) (any, error) {
	return &OGPluginServer{Impl: p.Impl}, nil
}

// Client returns a proxy that translates RPC calls into OGPlugin methods.
func (p *OGPluginNetRPC) Client(b *goplugin.MuxBroker, c *rpc.Client) (any, error) {
	return &OGPluginClient{client: c}, nil
}

// --- RPC Server side (runs in plugin process) ---

type OGPluginServer struct {
	Impl OGPlugin
}

type CapabilitiesReply struct {
	Caps Capabilities
	Err  string
}

func (s *OGPluginServer) Capabilities(args *EmptyArgs, reply *CapabilitiesReply) error {
	caps, err := s.Impl.Capabilities()
	reply.Caps = caps
	if err != nil {
		reply.Err = err.Error()
	}
	return nil
}

type ListToolsReply struct {
	Tools []ToolInfo
	Err   string
}

func (s *OGPluginServer) ListTools(args *EmptyArgs, reply *ListToolsReply) error {
	tools, err := s.Impl.ListTools()
	reply.Tools = tools
	if err != nil {
		reply.Err = err.Error()
	}
	return nil
}

type CallToolArgs struct {
	Name string
	Args map[string]any
}

type CallToolReply struct {
	Result string
	Err    string
}

func (s *OGPluginServer) CallTool(args *CallToolArgs, reply *CallToolReply) error {
	result, err := s.Impl.CallTool(args.Name, args.Args)
	reply.Result = result
	if err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// --- RPC Client side (runs in host process) ---

type OGPluginClient struct {
	client *rpc.Client
}

// EmptyArgs is used for RPC calls that take no arguments (gob can't encode nil).
type EmptyArgs struct{}

func (c *OGPluginClient) Capabilities() (Capabilities, error) {
	var reply CapabilitiesReply
	err := c.client.Call("Plugin.Capabilities", &EmptyArgs{}, &reply)
	if err != nil {
		return Capabilities{}, err
	}
	if reply.Err != "" {
		return reply.Caps, fmt.Errorf(reply.Err)
	}
	return reply.Caps, nil
}

func (c *OGPluginClient) ListTools() ([]ToolInfo, error) {
	var reply ListToolsReply
	err := c.client.Call("Plugin.ListTools", &EmptyArgs{}, &reply)
	if err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return reply.Tools, fmt.Errorf(reply.Err)
	}
	return reply.Tools, nil
}

func (c *OGPluginClient) CallTool(name string, args map[string]any) (string, error) {
	var reply CallToolReply
	err := c.client.Call("Plugin.CallTool", &CallToolArgs{Name: name, Args: args}, &reply)
	if err != nil {
		return "", err
	}
	if reply.Err != "" {
		return reply.Result, fmt.Errorf(reply.Err)
	}
	return reply.Result, nil
}
