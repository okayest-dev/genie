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
	name := strings.TrimSuffix(binaryName, filepath.Ext(binaryName))

	// Try new directory layout first: <pluginDir>/<name>/manifest.toml
	dirManifest := filepath.Join(pluginDir, name, "manifest.toml")
	
	// First check if the plugin directory exists and is actually a directory
	pluginDirPath := filepath.Join(pluginDir, name)
	if info, err := os.Stat(pluginDirPath); err == nil && info.IsDir() {
		data, err := os.ReadFile(dirManifest)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if data != nil {
			return parseManifestData(data, dirManifest)
		}
	}

	// Fall back to old flat layout: <pluginDir>/<name>.toml
	flatManifest := filepath.Join(pluginDir, name+".toml")
	data, err := os.ReadFile(flatManifest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseManifestData(data, flatManifest)
}

func parseManifestData(data []byte, manifestPath string) (*Manifest, error) {
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
	validCaps := map[string]bool{"tools": true}
	for _, c := range m.Capabilities {
		if !validCaps[c] {
			return fmt.Errorf("unknown capability: %s", c)
		}
	}
	return nil
}
