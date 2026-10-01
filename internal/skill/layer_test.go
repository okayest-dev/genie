package skill

import (
	"strings"
	"testing"
)

func TestBuildSkillLayerEmpty(t *testing.T) {
	if got := BuildSkillLayer(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBuildSkillLayerEmptySlice(t *testing.T) {
	if got := BuildSkillLayer([]ParsedSkill{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBuildSkillLayerSingle(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "tdd", Description: "Test-driven development.", Body: "Do TDD."},
	}
	got := BuildSkillLayer(skills)
	if !strings.HasPrefix(got, "## Skills\n") {
		t.Errorf("got %q, want it to start with '## Skills'", got)
	}
	if !strings.Contains(got, "- tdd: Test-driven development.") {
		t.Errorf("got %q, want the skill index line", got)
	}
}

func TestBuildSkillLayerNeverIncludesBodies(t *testing.T) {
	// The layer is index-only by design. A body reaches the model when the
	// skill tool returns it, so shipping one here would make every discovered
	// skill permanently resident — the cost the index exists to avoid.
	skills := []ParsedSkill{
		{Name: "tdd", Description: "TDD.", Body: "Do TDD."},
		{Name: "research", Description: "Research.", Body: "Research it."},
	}
	got := BuildSkillLayer(skills)
	for _, unwanted := range []string{"Do TDD.", "Research it.", "### Skill:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("got %q, want no body content (%q) in the index layer", got, unwanted)
		}
	}
}

func TestBuildSkillLayerWithArgumentHint(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "compile", Description: "Compile code.", ArgumentHint: "<file>", Body: "Compile it."},
	}
	got := BuildSkillLayer(skills)
	if !strings.Contains(got, "[argument: <file>]") {
		t.Errorf("got %q, want argument hint in the index", got)
	}
}

func TestBuildSkillLayerWithoutArgumentHint(t *testing.T) {
	skills := []ParsedSkill{{Name: "tdd", Description: "TDD.", Body: "Do TDD."}}
	got := BuildSkillLayer(skills)
	if strings.Contains(got, "[argument:") {
		t.Errorf("got %q, should not contain argument hint", got)
	}
}

func TestBuildSkillLayerListsEveryBoundSkill(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "alpha", Description: "First.", Body: "Alpha body."},
		{Name: "beta", Description: "Second."},
	}
	got := BuildSkillLayer(skills)

	if !strings.Contains(got, "- alpha: First.") {
		t.Errorf("got %q, want alpha index line", got)
	}
	if !strings.Contains(got, "- beta: Second.") {
		t.Errorf("got %q, want beta index line", got)
	}
}

func TestBuildSkillLayerPreservesBoundOrder(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "zeta", Description: "Last."},
		{Name: "alpha", Description: "First."},
	}
	got := BuildSkillLayer(skills)
	zeta := strings.Index(got, "- zeta:")
	alpha := strings.Index(got, "- alpha:")
	if zeta < 0 || alpha < 0 {
		t.Fatalf("got %q, want both index lines", got)
	}
	if zeta > alpha {
		t.Errorf("got %q, want index lines in bound (discovery) order", got)
	}
}

func TestBuildSkillLayerBodyRightTrimmed(t *testing.T) {
	skills := []ParsedSkill{{Name: "x", Description: "X.", Body: "body content"}}
	got := BuildSkillLayer(skills)
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("layer should end with newline")
	}
}

func TestBuildSkillLayerStatesTheTrigger(t *testing.T) {
	// The framing line is the model's only cue for when to reach for a skill.
	got := BuildSkillLayer([]ParsedSkill{{Name: "tdd", Description: "TDD."}})
	if !strings.Contains(got, "engage a skill when its description matches the current task") {
		t.Errorf("got %q, want the trigger-semantics framing line", got)
	}
}
