package skilltool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/skill"
)

// fixedPool is a skilltool.Pool over a static bound set.
type fixedPool []skill.ParsedSkill

func (f fixedPool) Bound() []skill.ParsedSkill { return f }

// mutablePool is a Pool whose bound set can change between calls.
type mutablePool struct{ skills fixedPool }

func (m *mutablePool) Bound() []skill.ParsedSkill { return m.skills }

func samplePool() fixedPool {
	return fixedPool{
		{Name: "tdd", Description: "Test-driven development.", Body: "Do TDD."},
		{Name: "research", Description: "Research a topic.", Body: "Research it."},
	}
}

func call(t *testing.T, tool *Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(raw)
}

func TestSkillToolSchema(t *testing.T) {
	tool := New(samplePool())
	if tool.Name() != "skill" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "skill")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("schema type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object")
	}
	if _, ok := props["name"]; !ok {
		t.Errorf("schema has no name property, got %v", props)
	}
	req, ok := params["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "name" {
		t.Errorf("required = %v, want [name]", params["required"])
	}
}

func TestSkillToolDescriptionStatesTheTrigger(t *testing.T) {
	tool := New(samplePool())
	for _, want := range []string{"## Skills", "follow them for the rest of the conversation"} {
		if !strings.Contains(tool.Description(), want) {
			t.Errorf("Description() = %q, want it to mention %q", tool.Description(), want)
		}
	}
}

func TestSkillToolReturnsBody(t *testing.T) {
	tool := New(samplePool())
	got, err := call(t, tool, map[string]any{"name": "tdd"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The body must come back verbatim: it is the whole reason to call.
	if !strings.Contains(got, "Do TDD.") {
		t.Errorf("result = %q, want the skill body", got)
	}
	if !strings.Contains(got, "tdd") {
		t.Errorf("result = %q, want it to name the skill", got)
	}
}

func TestSkillToolReturnsOnlyTheRequestedSkill(t *testing.T) {
	tool := New(samplePool())
	got, err := call(t, tool, map[string]any{"name": "tdd"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Research it.") {
		t.Errorf("result = %q, want only the requested skill's body", got)
	}
}

func TestSkillToolEmptyBodyIsNotAnError(t *testing.T) {
	// A skill with a description and no body is legal. Failing the call would
	// make the model retry a name that is already correct.
	tool := New(fixedPool{{Name: "pointer", Description: "Points elsewhere.", Body: ""}})
	got, err := call(t, tool, map[string]any{"name": "pointer"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil for a bodyless skill", err)
	}
	if !strings.Contains(got, "pointer") {
		t.Errorf("result = %q, want it to explain the skill has no body", got)
	}
}

func TestSkillToolUnknownNameNamesTheAlternatives(t *testing.T) {
	tool := New(samplePool())
	_, err := call(t, tool, map[string]any{"name": "nope"})
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown-skill error")
	}
	// A bare rejection leaves the model with nothing to act on; the available
	// set is the correction.
	for _, want := range []string{"nope", "research", "tdd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestSkillToolUnknownNameOnEmptyPool(t *testing.T) {
	tool := New(fixedPool{})
	_, err := call(t, tool, map[string]any{"name": "tdd"})
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown-skill error")
	}
	if !strings.Contains(err.Error(), "no skills are available") {
		t.Errorf("error = %q, want it to say no skills are available rather than list an empty set", err)
	}
}

func TestSkillToolRejectsSkillOutsideTheBoundPool(t *testing.T) {
	// The pool is the *agent's* bound set. An agent binding only research must
	// not reach tdd through the tool even though tdd exists on disk and is
	// enabled in config.
	tool := New(fixedPool{{Name: "research", Description: "Research a topic.", Body: "Research it."}})
	if _, err := call(t, tool, map[string]any{"name": "tdd"}); err == nil {
		t.Fatal("Execute() succeeded for a skill outside the bound pool")
	}
	if _, err := call(t, tool, map[string]any{"name": "research"}); err != nil {
		t.Errorf("Execute() on the bound skill failed: %v", err)
	}
}

func TestSkillToolMissingName(t *testing.T) {
	tool := New(samplePool())
	if _, err := call(t, tool, map[string]any{}); err == nil {
		t.Fatal("Execute() error = nil, want missing-argument error")
	}
	if _, err := call(t, tool, map[string]any{"name": ""}); err == nil {
		t.Fatal("Execute() error = nil, want missing-argument error for an empty name")
	}
}

func TestSkillToolMalformedArgs(t *testing.T) {
	tool := New(samplePool())
	if _, err := tool.Execute(json.RawMessage(`not json`)); err == nil {
		t.Fatal("Execute() error = nil, want an error for malformed JSON")
	}
}

func TestSkillToolNameIsNotFuzzyMatched(t *testing.T) {
	// A prefix or case variant must not silently resolve: the model gets an
	// error naming the real skill rather than the wrong instructions.
	tool := New(samplePool())
	for _, name := range []string{"TDD", "td", "tddd", " tdd"} {
		if _, err := call(t, tool, map[string]any{"name": name}); err == nil {
			t.Errorf("Execute(%q) succeeded, want a miss", name)
		}
	}
}

func TestSkillToolResolvesAgainstLivePool(t *testing.T) {
	// The pool is re-derived per turn and per agent, so the tool must consult
	// it on every call rather than capture a snapshot at construction.
	pool := &mutablePool{skills: samplePool()}
	tool := New(pool)

	if _, err := call(t, tool, map[string]any{"name": "tdd"}); err != nil {
		t.Fatal(err)
	}
	// The agent switches and its bound set no longer includes tdd.
	pool.skills = fixedPool{{Name: "research", Description: "Research.", Body: "R."}}
	if _, err := call(t, tool, map[string]any{"name": "tdd"}); err == nil {
		t.Error("Execute(tdd) succeeded after the bound pool dropped tdd")
	}
	if _, err := call(t, tool, map[string]any{"name": "research"}); err != nil {
		t.Errorf("Execute(research) failed: %v", err)
	}
}

func TestSkillToolReflectsEditedSkillFile(t *testing.T) {
	// Nothing is cached, so a re-read after an edit serves the new text without
	// any invalidation step.
	pool := &mutablePool{skills: fixedPool{{Name: "tdd", Description: "TDD.", Body: "old body"}}}
	tool := New(pool)
	if got, _ := call(t, tool, map[string]any{"name": "tdd"}); !strings.Contains(got, "old body") {
		t.Fatalf("result = %q, want the original body", got)
	}
	pool.skills = fixedPool{{Name: "tdd", Description: "TDD.", Body: "new body"}}
	if got, _ := call(t, tool, map[string]any{"name": "tdd"}); !strings.Contains(got, "new body") {
		t.Errorf("result = %q, want the edited body", got)
	}
}

func TestSkillToolConcurrentCalls(t *testing.T) {
	// The tool is reachable from the tool-call loop alongside other tools.
	// Run under -race.
	tool := New(samplePool())
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			name := "tdd"
			if i%2 == 0 {
				name = "research"
			}
			_, _ = call(t, tool, map[string]any{"name": name})
		}(i)
	}
	for i := 0; i < 16; i++ {
		<-done
	}
}
