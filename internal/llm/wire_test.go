package llm_test

import (
	"slices"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
)

// TestWireNamesListsEveryBundledWire: WireNames is the one list config
// validates a provider's wire against, so it must be exactly the bundled
// vocabulary — every bundled wire, and nothing else. The want list is the
// documented wire set, not WireNames' own output, so a wire added to the
// constants but forgotten here fails.
func TestWireNamesListsEveryBundledWire(t *testing.T) {
	got := llm.WireNames()
	want := []string{"openai", "anthropic", "responses", "google", "copilot", "bedrock"}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("WireNames() = %v, missing bundled wire %q", got, name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("WireNames() = %v, want exactly the %d bundled wires", got, len(want))
	}
}
