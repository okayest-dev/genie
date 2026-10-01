package run

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/tools/skilltool"
)

// writeSkill creates a skills dir with one SKILL.md and returns the dir.
func writeSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillHandle builds a Handle whose skills come from one temp dir.
func skillHandle(t *testing.T, skillsDir string) *Handle {
	t.Helper()
	return newTestHandle(t, Options{
		Config: &config.Config{
			SessionDir: t.TempDir(),
			Skills:     config.Skills{Dirs: []string{skillsDir}},
		},
		Cwd:    t.TempDir(),
		Stderr: io.Discard,
	})
}

// testAgent builds a resolved agent binding the given skills (nil = inherit all).
func testAgent(name string, skills []string) *config.ResolvedAgent {
	return &config.ResolvedAgent{Name: name, Skills: skills}
}

func TestBoundMirrorsTheAgentBinding(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")
	writeSkill(t, dir, "beta", "Second skill.", "Beta body.")

	h := skillHandle(t, dir)
	if _, err := h.resolveInstruction(testAgent("tester", []string{"beta"})); err != nil {
		t.Fatal(err)
	}

	bound := h.Bound()
	if len(bound) != 1 || bound[0].Name != "beta" {
		t.Fatalf("Bound() = %v, want only the agent's bound skill", bound)
	}
}

func TestBoundFollowsAgentSwitch(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")
	writeSkill(t, dir, "beta", "Second skill.", "Beta body.")

	h := skillHandle(t, dir)
	if _, err := h.resolveInstruction(testAgent("tester", nil)); err != nil {
		t.Fatal(err)
	}
	if len(h.Bound()) != 2 {
		t.Fatalf("Bound() = %v, want both skills for an inheriting agent", h.Bound())
	}

	// A different agent binds a narrower set; the pool must follow.
	if _, err := h.resolveInstruction(&config.ResolvedAgent{Name: "tester", Skills: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	bound := h.Bound()
	if len(bound) != 1 || bound[0].Name != "alpha" {
		t.Fatalf("Bound() = %v, want the switched agent's set", bound)
	}
}

func TestBoundPicksUpEditedSkillFile(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")

	h := skillHandle(t, dir)
	a := testAgent("tester", nil)
	if _, err := h.resolveInstruction(a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Bound()[0].Body, "Alpha body.") {
		t.Fatalf("Bound() body = %q, want the original text", h.Bound()[0].Body)
	}

	// The pool is re-derived per turn, so an edit lands without a reload.
	writeSkill(t, dir, "alpha", "First skill.", "Rewritten body.")
	if _, err := h.resolveInstruction(a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Bound()[0].Body, "Rewritten body.") {
		t.Errorf("Bound() body = %q, want the edited text", h.Bound()[0].Body)
	}
}

func TestInstructionLayerIsIndexOnly(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")
	writeSkill(t, dir, "beta", "Second skill.", "Beta body.")

	h := skillHandle(t, dir)
	instruction, err := h.resolveInstruction(testAgent("tester", nil))
	if err != nil {
		t.Fatal(err)
	}

	// The index is what lets the model choose. Bodies in the layer would make
	// every discovered skill permanently resident, which is the cost the tool
	// exists to avoid.
	if !strings.Contains(instruction, "## Skills") {
		t.Errorf("instruction missing the skills index:\n%s", instruction)
	}
	if !strings.Contains(instruction, "- alpha: First skill.") {
		t.Errorf("instruction missing alpha's index line:\n%s", instruction)
	}
	for _, unwanted := range []string{"Alpha body.", "Beta body.", "### Skill:"} {
		if strings.Contains(instruction, unwanted) {
			t.Errorf("instruction carries body content %q; the layer should be index-only", unwanted)
		}
	}
}

func TestSkillToolIsRegisteredOnEveryRegistry(t *testing.T) {
	// The tool is how a model reaches a body, so it must be present even when
	// no skills are configured; otherwise a later skill appearing is invisible.
	h := skillHandle(t, t.TempDir())
	if _, ok := h.full.Get(skilltool.ToolName); !ok {
		t.Error("skill tool missing from the full registry")
	}
	// Explicit agent tool subsets still gate it: the tool is a tool, and an
	// agent that names its tools gets exactly those.
	if _, ok := h.full.Subset([]string{"read", skilltool.ToolName}).Get(skilltool.ToolName); !ok {
		t.Error("skill tool missing from an explicit subset that names it")
	}
	if _, ok := h.full.Subset([]string{"read"}).Get(skilltool.ToolName); ok {
		t.Error("skill tool present in a subset that does not name it")
	}
}

func TestSkillToolServesBodiesThroughTheHandle(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")
	writeSkill(t, dir, "beta", "Second skill.", "Beta body.")

	h := skillHandle(t, dir)
	if _, err := h.resolveInstruction(testAgent("tester", []string{"alpha"})); err != nil {
		t.Fatal(err)
	}

	got, err := h.full.Execute(skilltool.ToolName, json.RawMessage(`{"name":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Alpha body.") {
		t.Errorf("tool result = %q, want alpha's body", got)
	}

	// beta is enabled on disk but not bound to this agent, so the tool must
	// not serve it.
	if _, err := h.full.Execute(skilltool.ToolName, json.RawMessage(`{"name":"beta"}`)); err == nil {
		t.Error("tool served a skill the agent did not bind")
	}
}

func TestSkillToolWorksThroughASubsetRegistry(t *testing.T) {
	// An agent naming its tools gets a subset registry. The skill tool must
	// still reach the run's pool through that subset rather than through a
	// registry reference captured at build time.
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "First skill.", "Alpha body.")

	h := skillHandle(t, dir)
	if _, err := h.resolveInstruction(testAgent("tester", nil)); err != nil {
		t.Fatal(err)
	}
	subset := h.full.Subset([]string{skilltool.ToolName})
	got, err := subset.Execute(skilltool.ToolName, json.RawMessage(`{"name":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Alpha body.") {
		t.Errorf("result = %q, want the body served through the subset", got)
	}
}
