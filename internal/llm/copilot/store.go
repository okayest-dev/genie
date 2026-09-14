// Package copilot implements the llm wire for GitHub Copilot, using the
// Copilot credential store (ADR-0002) for auth and the chat/completions
// wire protocol. It is a bundled in-process wire registered at init time.
package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// credentialsFile is the top-level shape of the host-keyed credential store
// at $XDG_DATA_HOME/genie/copilot/credentials.json (ADR-0002). Genie owns this
// file; the wire never stores a derived Copilot JWT here — only the durable
// GitHub OAuth record, and only what a future `auth login` writes.
type credentialsFile struct {
	Version int                  `json:"version"`
	Hosts   map[string]hostEntry `json:"hosts"`
}

// hostEntry is one host's durable OAuth record in the credential store.
type hostEntry struct {
	OAuthToken string `json:"oauth_token"`
	User       string `json:"user"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// StorePath returns the default credential store path for a given XDG data dir.
// The store is genie-owned, under genie's own data directory (ADR-0002).
func StorePath(xdgDataHome string) string {
	return filepath.Join(xdgDataHome, "genie", "copilot", "credentials.json")
}

// defaultXDGDataHome returns the platform's XDG data directory, falling back
// to ~/.local/share.
func defaultXDGDataHome() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share")
	}
	return "."
}

// ReadOAuthToken reads the GitHub OAuth token for the given host from the
// credential store at the default path. Returns an error when the file is
// missing, malformed, has an unsupported version, or the host has no token.
func ReadOAuthToken(xdgDataHome, host string) (string, error) {
	return readOAuthTokenFromFile(StorePath(xdgDataHome), host)
}

// readOAuthTokenFromFile reads the OAuth token from an explicit file path.
func readOAuthTokenFromFile(path, host string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("copilot credentials: %w", err)
	}
	var store credentialsFile
	if err := json.Unmarshal(data, &store); err != nil {
		return "", fmt.Errorf("copilot credentials: parse: %w", err)
	}
	if store.Version != 1 {
		return "", fmt.Errorf("copilot credentials: unsupported version %d", store.Version)
	}
	entry, ok := store.Hosts[host]
	if !ok {
		return "", fmt.Errorf("copilot credentials: no entry for host %q", host)
	}
	if entry.OAuthToken == "" {
		return "", fmt.Errorf("copilot credentials: empty oauth_token for host %q", host)
	}
	return entry.OAuthToken, nil
}
