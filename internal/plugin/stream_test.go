package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/tools"
)

// slowFirstWirePluginScript builds a wire plugin whose first wire/stream call
// sleeps delaySeconds before answering; later calls answer immediately.
func slowFirstWirePluginScript(delaySeconds int) string {
	return fmt.Sprintf(`#!/bin/bash
count=0
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":true,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "wire/init")
            echo '{"jsonrpc":"2.0","result":{"ok":true},"id":'"$id"'}'
            ;;
        "wire/list_models")
            echo '{"jsonrpc":"2.0","result":{"models":[{"id":"slow-wire","name":"Slow Wire"}]},"id":'"$id"'}'
            ;;
        "wire/stream")
            if [ "$count" -eq 0 ]; then
                count=1
                sleep %d
            fi
            echo '{"jsonrpc":"2.0","result":{"events":[{"kind":"text","text":"hello"}]},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`, delaySeconds)
}

// hungWirePluginScript builds a wire plugin whose wire/stream call sleeps long
// enough that the resync grace period elapses before it answers.
func hungWirePluginScript() string {
	return `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":true,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "wire/init")
            echo '{"jsonrpc":"2.0","result":{"ok":true},"id":'"$id"'}'
            ;;
        "wire/list_models")
            echo '{"jsonrpc":"2.0","result":{"models":[{"id":"hung-wire","name":"Hung Wire"}]},"id":'"$id"'}'
            ;;
        "wire/stream")
            sleep 10
            echo '{"jsonrpc":"2.0","result":{"events":[{"kind":"text","text":"never"}]},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
}

// loadStreamTestPlugin spins up a standalone manager with opts and returns the
// single plugin it discovers. The manager is shut down at test cleanup.
func loadStreamTestPlugin(t *testing.T, script string, opts ...Option) *Plugin {
	t.Helper()
	dir := t.TempDir()
	name := "streamplug"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	mgr := NewManager(dir, nil, nil, reg, opts...)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	plugins := mgr.GetPlugins()
	p, ok := plugins[name]
	if !ok {
		t.Fatal("streamplug plugin not found")
	}
	t.Cleanup(mgr.Shutdown)
	return p
}

// A slow stream completion must not kill the plugin: after the stream timeout
// elapses, StreamWire drains the late response, keeps the plugin active, and a
// subsequent stream call works.
func TestManagerStreamWireTimeoutRecovers(t *testing.T) {
	defer func() { streamResync = 30 * time.Second }()
	streamResync = 5 * time.Second

	p := loadStreamTestPlugin(t, slowFirstWirePluginScript(2), WithStreamTimeout(200*time.Millisecond))

	start := time.Now()
	if _, err := p.StreamWire(json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "wire stream timeout") {
		t.Fatalf("slow stream should time out with 'wire stream timeout', got %v", err)
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatalf("timed-out stream returned after %v; the resync drain should wait for the late response", time.Since(start))
	}
	if p.Active == false {
		t.Fatal("plugin was deactivated despite the late response arriving during the resync window")
	}

	res, err := p.StreamWire(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("second stream after recovery failed: %v", err)
	}
	if !strings.Contains(string(res), "hello") {
		t.Errorf("second stream result = %s, want it to contain %q", res, "hello")
	}
}

// A stream that never answers (even past the resync grace period) degrades the
// plugin to inactive instead of blocking the harness forever.
func TestManagerStreamWireTimeoutMarksInactiveWhenStuck(t *testing.T) {
	defer func() { streamResync = 30 * time.Second }()
	streamResync = 200 * time.Millisecond

	p := loadStreamTestPlugin(t, hungWirePluginScript(), WithStreamTimeout(200*time.Millisecond))
	defer p.Cmd.Process.Kill()

	start := time.Now()
	if _, err := p.StreamWire(json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "marked inactive") {
		t.Fatalf("hung stream should time out and deactivate the plugin, got %v", err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatalf("deactivation happened in %v; the resync grace period should elapse first", time.Since(start))
	}
	if p.Active == true {
		t.Fatal("plugin stayed active after the resync grace period elapsed with no response")
	}

	if _, err := p.StreamWire(json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Errorf("stream on a deactivated plugin should fail with 'not active', got %v", err)
	}
}
