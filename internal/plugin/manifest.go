package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Manifest struct {
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Capabilities []string `toml:"capabilities"`
}

func ParseManifest(pluginDir, binaryName string) (*Manifest, error) {
	manifestPath := filepath.Join(pluginDir, strings.TrimSuffix(binaryName, filepath.Ext(binaryName))+".toml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var m Manifest
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}

	if m.Name == "" {
		return nil, fmt.Errorf("manifest %s: missing name", manifestPath)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("manifest %s: missing version", manifestPath)
	}
	if len(m.Capabilities) == 0 {
		return nil, fmt.Errorf("manifest %s: missing capabilities", manifestPath)
	}

	return &m, nil
}

func (m *Manifest) HasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func (m *Manifest) Validate() error {
	validCaps := map[string]bool{"tools": true, "wires": true, "providers": true}
	for _, c := range m.Capabilities {
		if !validCaps[c] {
			return fmt.Errorf("unknown capability: %s", c)
		}
	}
	return nil
}