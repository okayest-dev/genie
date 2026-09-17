package skill

import (
	"sort"
	"strings"
	"testing"
)

func filteredPoolDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	writeSkill(t, d, "alpha", `---
name: alpha
description: Alpha desc.
---
Alpha body.
`)
	writeSkill(t, d, "beta", `---
name: beta
description: Beta desc.
---
Beta body.
`)
	writeSkill(t, d, "bad", "not frontmatter")
	writeSkill(t, d, "gamma", `---
name: gamma
description: Gamma desc.
---
`)
	return d
}

func sortedPoolNames(pool []ParsedSkill) []string {
	var names []string
	for _, s := range pool {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func TestFilteredPoolNoFilter(t *testing.T) {
	d := filteredPoolDir(t)
	pool, warns, err := FilteredPool([]string{d}, nil, nil)
	if err != nil {
		t.Fatalf("FilteredPool() error = %v", err)
	}
	if got, want := sortedPoolNames(pool), []string{"alpha", "beta", "gamma"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pool = %v, want %v", got, want)
	}
	// The invalid SKILL.md produces a discovery warning.
	if len(warns) != 1 {
		t.Errorf("got %d warnings, want 1", len(warns))
	}
}

func TestFilteredPoolEnable(t *testing.T) {
	d := filteredPoolDir(t)
	pool, _, err := FilteredPool([]string{d}, []string{"beta"}, nil)
	if err != nil {
		t.Fatalf("FilteredPool() error = %v", err)
	}
	if got, want := sortedPoolNames(pool), []string{"beta"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pool = %v, want %v", got, want)
	}
}

func TestFilteredPoolDisable(t *testing.T) {
	d := filteredPoolDir(t)
	pool, warns, err := FilteredPool([]string{d}, nil, []string{"gamma"})
	if err != nil {
		t.Fatalf("FilteredPool() error = %v", err)
	}
	if got, want := sortedPoolNames(pool), []string{"alpha", "beta"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pool = %v, want %v", got, want)
	}
	// Disabled skill + invalid SKILL.md = two warnings.
	if !strings.Contains(strings.Join(warnMessages(warns), " "), `"gamma" disabled by config`) {
		t.Errorf("warnings = %v, want a disable warning for gamma", warns)
	}
}

func TestFilteredPoolDisableWinsOverEnable(t *testing.T) {
	d := filteredPoolDir(t)
	pool, _, err := FilteredPool([]string{d}, []string{"alpha", "beta"}, []string{"alpha"})
	if err != nil {
		t.Fatalf("FilteredPool() error = %v", err)
	}
	if got, want := sortedPoolNames(pool), []string{"beta"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pool = %v, want %v", got, want)
	}
}

func TestFilteredPoolError(t *testing.T) {
	// A dir with an unreadable SKILL.md subdir yields an error. Use a file
	// in place of a directory to force ReadDir/ReadFile to fail.
	_, _, err := FilteredPool([]string{"/dev/null/as-a-dir"}, nil, nil)
	if err == nil {
		t.Fatalf("FilteredPool() error = nil, want error")
	}
}

func TestNames(t *testing.T) {
	pool := []ParsedSkill{{Name: "b"}, {Name: "a"}}
	got := Names(pool)
	want := []string{"b", "a"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Names() = %v, want %v (discovery order preserved)", got, want)
	}
	if got := len(Names(nil)); got != 0 {
		t.Errorf("Names(nil) returned %d names, want 0", got)
	}
}

func TestPipelineInheritAll(t *testing.T) {
	d := filteredPoolDir(t)
	layer, warns, err := Pipeline([]string{d}, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("Pipeline() error = %v", err)
	}
	if !strings.Contains(layer, "## Skills") {
		t.Errorf("layer missing header: %q", layer)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(layer, "- "+name+":") {
			t.Errorf("layer missing index line for %s:\n%s", name, layer)
		}
	}
	if len(warns) != 1 {
		t.Errorf("got %d warnings, want 1 (invalid SKILL.md)", len(warns))
	}
}

func TestPipelineInheritNothing(t *testing.T) {
	d := filteredPoolDir(t)
	layer, _, err := Pipeline([]string{d}, nil, nil, []string{}, "")
	if err != nil {
		t.Fatalf("Pipeline() error = %v", err)
	}
	if layer != "" {
		t.Errorf("layer = %q, want empty for empty agent skills", layer)
	}
}

func TestPipelineExplicitSkills(t *testing.T) {
	d := filteredPoolDir(t)
	layer, _, err := Pipeline([]string{d}, nil, nil, []string{"beta"}, "")
	if err != nil {
		t.Fatalf("Pipeline() error = %v", err)
	}
	if !strings.Contains(layer, "- beta:") {
		t.Errorf("layer missing beta: %q", layer)
	}
	if strings.Contains(layer, "alpha") || strings.Contains(layer, "gamma") {
		t.Errorf("layer should only bind beta:\n%s", layer)
	}
}

func TestPipelineUnknownSkill(t *testing.T) {
	d := filteredPoolDir(t)
	_, _, err := Pipeline([]string{d}, nil, nil, []string{"nope"}, "tester")
	if err == nil {
		t.Fatalf("Pipeline() error = nil, want unknown-skill error")
	}
	if !strings.Contains(err.Error(), `agent "tester": unknown skill "nope"`) {
		t.Errorf("error = %q, want agent/unknown-skill message", err)
	}
}

func TestPoolNames(t *testing.T) {
	d := filteredPoolDir(t)
	names, _, err := PoolNames([]string{d}, []string{"alpha", "gamma"}, nil)
	if err != nil {
		t.Fatalf("PoolNames() error = %v", err)
	}
	sort.Strings(names)
	if got, want := strings.Join(names, ","), "alpha,gamma"; got != want {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func warnMessages(warns []Warning) []string {
	var msgs []string
	for _, w := range warns {
		msgs = append(msgs, w.Message)
	}
	return msgs
}