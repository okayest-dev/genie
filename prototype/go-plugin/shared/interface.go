package shared

// ToolInfo describes a single tool the plugin exposes.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"` // raw JSON Schema string
}

// Capabilities declares what the plugin can do.
type Capabilities struct {
	Tools     bool `json:"tools"`
	Wires     bool `json:"wires"`
	Providers bool `json:"providers"`
}

// OGPlugin is the contract between host and plugin.
type OGPlugin interface {
	Capabilities() (Capabilities, error)
	ListTools() ([]ToolInfo, error)
	CallTool(name string, args map[string]any) (string, error)
}
