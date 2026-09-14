package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadStoreValid(t *testing.T) {
	store := credentialsFile{
		Version: 1,
		Hosts: map[string]hostEntry{
			"github.com": {OAuthToken: "gho_test123", User: "testuser"},
		},
	}
	dir := t.TempDir()
	writeStore(t, dir, store)

	token, err := ReadOAuthToken(dir, "github.com")
	if err != nil {
		t.Fatalf("ReadOAuthToken: %v", err)
	}
	if token != "gho_test123" {
		t.Errorf("token = %q, want gho_test123", token)
	}
}

func TestReadStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadOAuthToken(dir, "github.com")
	if err == nil {
		t.Fatal("ReadOAuthToken should error on missing file")
	}
}

func TestReadStoreWrongHost(t *testing.T) {
	store := credentialsFile{
		Version: 1,
		Hosts: map[string]hostEntry{
			"github.com": {OAuthToken: "gho_test123"},
		},
	}
	dir := t.TempDir()
	writeStore(t, dir, store)

	_, err := ReadOAuthToken(dir, "enterprise.example.com")
	if err == nil {
		t.Fatal("ReadOAuthToken should error for unknown host")
	}
}

func TestReadStoreEmptyOAuthToken(t *testing.T) {
	store := credentialsFile{
		Version: 1,
		Hosts: map[string]hostEntry{
			"github.com": {OAuthToken: ""},
		},
	}
	dir := t.TempDir()
	writeStore(t, dir, store)

	_, err := ReadOAuthToken(dir, "github.com")
	if err == nil {
		t.Fatal("ReadOAuthToken should error for empty oauth_token")
	}
}

func TestReadStoreMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := StorePath(dir)
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("{not json}"), 0600)
	_, err := ReadOAuthToken(dir, "github.com")
	if err == nil {
		t.Fatal("ReadOAuthToken should error for malformed JSON")
	}
}

func TestReadStoreWrongVersion(t *testing.T) {
	store := credentialsFile{
		Version: 99,
		Hosts: map[string]hostEntry{
			"github.com": {OAuthToken: "gho_test123"},
		},
	}
	dir := t.TempDir()
	writeStore(t, dir, store)

	_, err := ReadOAuthToken(dir, "github.com")
	if err == nil {
		t.Fatal("ReadOAuthToken should error for unsupported version")
	}
}

func TestStoreDefaultPath(t *testing.T) {
	// The default path is $XDG_DATA_HOME/github-copilot/hosts.json, matching
	// the external Copilot plugin's store (ADR-0002) so an existing install's
	// credentials are picked up by the bundled wire.
	got := StorePath("/tmp/xdg")
	want := filepath.Join("/tmp/xdg", "github-copilot", "hosts.json")
	if got != want {
		t.Errorf("StorePath(%q) = %q, want %q", "/tmp/xdg", got, want)
	}
}

// writeStore writes a credentials file to dir/github-copilot/hosts.json.
func writeStore(t *testing.T, dir string, store credentialsFile) {
	t.Helper()
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}
	path := StorePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write store: %v", err)
	}
}
