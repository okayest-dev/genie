package skill

import (
	"strings"
	"testing"
)

func TestBuildSkillLayerEmpty(t *testing.T) {
	got := BuildSkillLayer(nil)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBuildSkillLayerEmptySlice(t *testing.T) {
	got := BuildSkillLayer([]ParsedSkill{})
	if got != "" {
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
	if !strings.Contains(got, "### Skill: tdd") {
		t.Errorf("got %q, want the body header", got)
	}
	if !strings.Contains(got, "Do TDD.") {
		t.Errorf("got %q, want the body content", got)
	}
}

func TestBuildSkillLayerWithArgumentHint(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "compile", Description: "Compile code.", ArgumentHint: "<file>", Body: "Compile it."},
	}
	got := BuildSkillLayer(skills)
	if !strings.Contains(got, "[argument: <file>]") {
		t.Errorf("got %q, want argument hint", got)
	}
}

func TestBuildSkillLayerWithoutArgumentHint(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "tdd", Description: "TDD.", Body: "Do TDD."},
	}
	got := BuildSkillLayer(skills)
	if strings.Contains(got, "[argument:") {
		t.Errorf("got %q, should not contain argument hint", got)
	}
}

func TestBuildSkillLayerMultiple(t *testing.T) {
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
	if !strings.Contains(got, "### Skill: alpha") {
		t.Errorf("got %q, want alpha body header", got)
	}
	if strings.Contains(got, "### Skill: beta") {
		t.Errorf("got %q, should NOT have beta body header (empty body)", got)
	}

	alphaIdx := strings.Index(got, "### Skill: alpha")
	betaIdx := strings.Index(got, "- beta: Second.")
	if betaIdx > alphaIdx {
		t.Errorf("index section should list all skills before body sections")
	}
}

func TestBuildSkillLayerBodyRightTrimmed(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "x", Description: "X.", Body: "body content"},
	}
	got := BuildSkillLayer(skills)
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("layer should end with newline")
	}
}

func TestBuildSkillLayerIndexBeforeBody(t *testing.T) {
	skills := []ParsedSkill{
		{Name: "a", Description: "A.", Body: "A body."},
		{Name: "b", Description: "B.", Body: "B body."},
	}
	got := BuildSkillLayer(skills)
	lines := strings.Split(got, "\n")

	var lastIdxLine, firstBodyLine int
	for i, l := range lines {
		if strings.HasPrefix(l, "- ") {
			lastIdxLine = i
		}
		if strings.HasPrefix(l, "### Skill:") && firstBodyLine == 0 {
			firstBodyLine = i
		}
	}
	if lastIdxLine >= firstBodyLine {
		t.Errorf("all index lines (%d) should come before body sections (%d)", lastIdxLine, firstBodyLine)
	}
}
