package shared

import (
	"fmt"
	"net/rpc"

	goplugin "github.com/hashicorp/go-plugin"
)

// GeniePluginNetRPC is the go-plugin adapter that exposes GeniePlugin over net/rpc.
// This file is imported by both host and plugin — it's the shared RPC wiring.
type GeniePluginNetRPC struct {
	goplugin.Plugin
	// Impl is the concrete implementation, set during handshake.
	Impl GeniePlugin
}

// Server returns an RPC-compatible server for the host to call.
func (p *GeniePluginNetRPC) Server(*goplugin.MuxBroker) (any, error) {
	return &GeniePluginServer{Impl: p.Impl}, nil
}

// Client returns a proxy that translates RPC calls into GeniePlugin methods.
func (p *GeniePluginNetRPC) Client(b *goplugin.MuxBroker, c *rpc.Client) (any, error) {
	return &GeniePluginClient{client: c}, nil
}

// --- RPC Server side (runs in plugin process) ---

type GeniePluginServer struct {
	Impl GeniePlugin
}

type CapabilitiesReply struct {
	Caps Capabilities
	Err  string
}

func (s *GeniePluginServer) Capabilities(args *EmptyArgs, reply *CapabilitiesReply) error {
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

func (s *GeniePluginServer) ListTools(args *EmptyArgs, reply *ListToolsReply) error {
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

func (s *GeniePluginServer) CallTool(args *CallToolArgs, reply *CallToolReply) error {
	result, err := s.Impl.CallTool(args.Name, args.Args)
	reply.Result = result
	if err != nil {
		reply.Err = err.Error()
	}
	return nil
}

// --- RPC Client side (runs in host process) ---

type GeniePluginClient struct {
	client *rpc.Client
}

// EmptyArgs is used for RPC calls that take no arguments (gob can't encode nil).
type EmptyArgs struct{}

func (c *GeniePluginClient) Capabilities() (Capabilities, error) {
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

func (c *GeniePluginClient) ListTools() ([]ToolInfo, error) {
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

func (c *GeniePluginClient) CallTool(name string, args map[string]any) (string, error) {
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
