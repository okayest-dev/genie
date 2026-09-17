package skill

import (
	"sort"
	"testing"
)

var (
	skillAlpha = ParsedSkill{Name: "alpha", Description: "Alpha skill.", Body: "alpha body"}
	skillBeta  = ParsedSkill{Name: "beta", Description: "Beta skill.", Body: "beta body"}
	skillGamma = ParsedSkill{Name: "gamma", Description: "Gamma skill.", Body: "gamma body"}
)

func pool() []ParsedSkill {
	return []ParsedSkill{skillAlpha, skillBeta, skillGamma}
}

func names(skills []ParsedSkill) []string {
	var out []string
	for _, s := range skills {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func TestGlobalFilterNoLists(t *testing.T) {
	got, warns := GlobalFilter(pool(), nil, nil)
	if len(warns) != 0 {
		t.Errorf("warns = %d, want 0", len(warns))
	}
	if len(got) != 3 {
		t.Errorf("got %d skills, want 3", len(got))
	}
}

func TestGlobalFilterEnableOnly(t *testing.T) {
	got, _ := GlobalFilter(pool(), []string{"beta"}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1", len(got))
	}
	if got[0].Name != "beta" {
		t.Errorf("got %q, want beta", got[0].Name)
	}
}

func TestGlobalFilterEnableMultiple(t *testing.T) {
	got, _ := GlobalFilter(pool(), []string{"alpha", "gamma"}, nil)
	gotNames := names(got)
	want := []string{"alpha", "gamma"}
	if len(gotNames) != len(want) {
		t.Fatalf("got %v, want %v", gotNames, want)
	}
	for i := range gotNames {
		if gotNames[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, gotNames[i], want[i])
		}
	}
}

func TestGlobalFilterDisableOnly(t *testing.T) {
	got, warns := GlobalFilter(pool(), nil, []string{"beta"})
	gotNames := names(got)
	want := []string{"alpha", "gamma"}
	if len(gotNames) != len(want) {
		t.Fatalf("got %v, want %v", gotNames, want)
	}
	for i := range gotNames {
		if gotNames[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, gotNames[i], want[i])
		}
	}
	if len(warns) != 1 {
		t.Errorf("warns = %d, want 1", len(warns))
	}
}

func TestGlobalFilterEnableAndDisable(t *testing.T) {
	got, _ := GlobalFilter(pool(), []string{"alpha", "beta"}, []string{"beta"})
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1", len(got))
	}
	if got[0].Name != "alpha" {
		t.Errorf("got %q, want alpha", got[0].Name)
	}
}

func TestGlobalFilterDisableNonExistent(t *testing.T) {
	got, _ := GlobalFilter(pool(), nil, []string{"nonexistent"})
	if len(got) != 3 {
		t.Errorf("got %d skills, want 3 (disable non-existent is no-op)", len(got))
	}
}

func TestGlobalFilterEnableNonExistent(t *testing.T) {
	got, _ := GlobalFilter(pool(), []string{"nonexistent"}, nil)
	if len(got) != 0 {
		t.Errorf("got %d skills, want 0 (enable non-existent means nothing passes)", len(got))
	}
}

func TestGlobalFilterEmptyPool(t *testing.T) {
	got, _ := GlobalFilter(nil, []string{"alpha"}, nil)
	if len(got) != 0 {
		t.Errorf("got %d skills, want 0", len(got))
	}
}

func TestBindToAgentNilInheritsAll(t *testing.T) {
	got, err := BindToAgent(pool(), nil, "test-agent")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d skills, want 3", len(got))
	}
}

func TestBindToAgentEmptySelectsNone(t *testing.T) {
	got, err := BindToAgent(pool(), []string{}, "test-agent")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestBindToAgentExplicit(t *testing.T) {
	got, err := BindToAgent(pool(), []string{"gamma", "alpha"}, "test-agent")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills, want 2", len(got))
	}
	if got[0].Name != "gamma" || got[1].Name != "alpha" {
		t.Errorf("got %v, want [gamma alpha]", names(got))
	}
}

func TestBindToAgentUnknownSkill(t *testing.T) {
	_, err := BindToAgent(pool(), []string{"nope"}, "test-agent")
	if err == nil {
		t.Fatal("expected error for unknown skill, got nil")
	}
	if err.Error() != `agent "test-agent": unknown skill "nope"` {
		t.Errorf("err = %q, want it to name agent and skill", err.Error())
	}
}

func TestBindToAgentPartialUnknown(t *testing.T) {
	_, err := BindToAgent(pool(), []string{"alpha", "nope"}, "test-agent")
	if err == nil {
		t.Fatal("expected error for unknown skill in partial list")
	}
}

func TestBindToAgentEmptyPool(t *testing.T) {
	_, err := BindToAgent(nil, []string{"alpha"}, "test-agent")
	if err == nil {
		t.Fatal("expected error for skill not in empty pool")
	}
}
