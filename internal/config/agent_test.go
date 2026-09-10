package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentDefValid(t *testing.T) {
	data := []byte(`
model = "gpt-4o"
instruction_file = "/tmp/instr.md"
tools = ["read", "bash"]
inherit_agents_md = false
skills = ["tdd", "research"]
`)
	def, err := ParseAgentDef(data, "coder", "/agents/coder.toml")
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Name != "coder" {
		t.Errorf("Name = %q, want %q", def.Name, "coder")
	}
	if def.Source != "/agents/coder.toml" {
		t.Errorf("Source = %q, want %q", def.Source, "/agents/coder.toml")
	}
	if def.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", def.Model, "gpt-4o")
	}
	if def.InstructionFile != "/tmp/instr.md" {
		t.Errorf("InstructionFile = %q, want %q", def.InstructionFile, "/tmp/instr.md")
	}
	if len(def.Tools) != 2 || def.Tools[0] != "read" || def.Tools[1] != "bash" {
		t.Errorf("Tools = %v, want [read bash]", def.Tools)
	}
	if def.InheritAgentsMD == nil || *def.InheritAgentsMD {
		t.Errorf("InheritAgentsMD = %v, want false", def.InheritAgentsMD)
	}
	if len(def.Skills) != 2 || def.Skills[0] != "tdd" || def.Skills[1] != "research" {
		t.Errorf("Skills = %v, want [tdd research]", def.Skills)
	}
}

func TestParseAgentDefEmpty(t *testing.T) {
	def, err := ParseAgentDef([]byte(""), "empty", "/agents/empty.toml")
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Model != "" {
		t.Errorf("Model = %q, want empty", def.Model)
	}
	if def.Tools != nil {
		t.Errorf("Tools = %v, want nil", def.Tools)
	}
	if def.InheritAgentsMD != nil {
		t.Errorf("InheritAgentsMD = %v, want nil", def.InheritAgentsMD)
	}
	if def.Skills != nil {
		t.Errorf("Skills = %v, want nil", def.Skills)
	}
}

func TestParseAgentDefSkillsEmpty(t *testing.T) {
	def, err := ParseAgentDef([]byte(`skills = []`), "bare", "/x.toml")
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Skills == nil {
		t.Fatal("Skills = nil, want non-nil empty list (explicit no-skill agent)")
	}
	if len(def.Skills) != 0 {
		t.Errorf("Skills = %v, want empty", def.Skills)
	}
}

func TestParseAgentDefPartialFields(t *testing.T) {
	def, err := ParseAgentDef([]byte(`model = "fast"`), "partial", "/x.toml")
	if err != nil {
		t.Fatalf("ParseAgentDef: %v", err)
	}
	if def.Model != "fast" {
		t.Errorf("Model = %q, want %q", def.Model, "fast")
	}
	if def.InstructionFile != "" {
		t.Errorf("InstructionFile = %q, want empty", def.InstructionFile)
	}
	if def.Tools != nil {
		t.Errorf("Tools = %v, want nil", def.Tools)
	}
	if def.Skills != nil {
		t.Errorf("Skills = %v, want nil", def.Skills)
	}
}

func TestParseAgentDefUnknownKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "unknown scalar", file: `theme = "dark"`},
		{name: "unknown bool", file: `verbose = true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAgentDef([]byte(tc.file), "bad", "/x.toml")
			if err == nil {
				t.Fatal("ParseAgentDef accepted unknown key; want an error")
			}
		})
	}
}

func TestParseAgentDefMalformedTOML(t *testing.T) {
	_, err := ParseAgentDef([]byte(`model =`), "bad", "/x.toml")
	if err == nil {
		t.Fatal("ParseAgentDef accepted malformed TOML; want an error")
	}
}

func TestResolveAgentDefDefaults(t *testing.T) {
	cfg := &Config{
		Model:           "big-pickle",
		InstructionFile: "/cfg/instr.md",
		Tools:           Tools{Read: true, Write: true, Edit: false, Bash: true},
	}
	pool := []string{"tdd", "research"}
	def := &AgentDef{Name: "test", Source: "/x.toml"}
	resolved := ResolveAgentDef(def, cfg, pool)

	if resolved.Name != "test" {
		t.Errorf("Name = %q, want %q", resolved.Name, "test")
	}
	if resolved.Model != "big-pickle" {
		t.Errorf("Model = %q, want %q", resolved.Model, "big-pickle")
	}
	if resolved.InstructionFile != "/cfg/instr.md" {
		t.Errorf("InstructionFile = %q, want %q", resolved.InstructionFile, "/cfg/instr.md")
	}
	// edit is disabled in config, so allToolNames skips it
	wantTools := []string{"read", "write", "bash"}
	if len(resolved.Tools) != len(wantTools) {
		t.Fatalf("Tools = %v, want %v", resolved.Tools, wantTools)
	}
	for i, name := range wantTools {
		if resolved.Tools[i] != name {
			t.Errorf("Tools[%d] = %q, want %q", i, resolved.Tools[i], name)
		}
	}
	if !resolved.InheritAgentsMD {
		t.Errorf("InheritAgentsMD = false, want true (default)")
	}
	if resolved.Skills == nil {
		t.Fatal("Skills = nil, want non-nil (inherits pool)")
	}
	if len(resolved.Skills) != 2 || resolved.Skills[0] != "tdd" || resolved.Skills[1] != "research" {
		t.Errorf("Skills = %v, want [tdd research] (inherits pool)", resolved.Skills)
	}
}

func TestResolveAgentDefOverrides(t *testing.T) {
	cfg := &Config{
		Model:           "big-pickle",
		InstructionFile: "/cfg/instr.md",
		Tools:           Tools{Read: true, Write: true, Edit: true, Bash: true},
	}
	falseVal := false
	def := &AgentDef{
		Name:            "coder",
		Model:           "gpt-4o",
		InstructionFile: "/agent/instr.md",
		Tools:           []string{"read", "bash"},
		InheritAgentsMD: &falseVal,
		Skills:          []string{"research"},
	}
	resolved := ResolveAgentDef(def, cfg, []string{"tdd", "research", "grilling"})

	if resolved.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", resolved.Model, "gpt-4o")
	}
	if resolved.InstructionFile != "/agent/instr.md" {
		t.Errorf("InstructionFile = %q, want %q", resolved.InstructionFile, "/agent/instr.md")
	}
	if len(resolved.Tools) != 2 || resolved.Tools[0] != "read" || resolved.Tools[1] != "bash" {
		t.Errorf("Tools = %v, want [read bash]", resolved.Tools)
	}
	if resolved.InheritAgentsMD {
		t.Errorf("InheritAgentsMD = true, want false")
	}
	if len(resolved.Skills) != 1 || resolved.Skills[0] != "research" {
		t.Errorf("Skills = %v, want [research]", resolved.Skills)
	}
}

func TestResolveAgentDefSkillsInherit(t *testing.T) {
	cfg := &Config{Model: "big-pickle"}
	pool := []string{"tdd", "research", "grilling"}
	def := &AgentDef{Name: "x", Source: "/x.toml"}

	resolved := ResolveAgentDef(def, cfg, pool)

	if resolved.Skills == nil {
		t.Fatal("Skills = nil, want non-nil (absent skills field inherits all)")
	}
	if len(resolved.Skills) != 3 ||
		resolved.Skills[0] != "tdd" ||
		resolved.Skills[1] != "research" ||
		resolved.Skills[2] != "grilling" {
		t.Errorf("Skills = %v, want [tdd research grilling]", resolved.Skills)
	}
}

func TestResolveAgentDefSkillsInheritEmptyPool(t *testing.T) {
	cfg := &Config{Model: "big-pickle"}
	def := &AgentDef{Name: "x", Source: "/x.toml"}

	resolved := ResolveAgentDef(def, cfg, nil)

	if resolved.Skills == nil {
		t.Fatal("Skills = nil, want non-nil even with an empty pool")
	}
	if len(resolved.Skills) != 0 {
		t.Errorf("Skills = %v, want empty", resolved.Skills)
	}
}

func TestResolveAgentDefSkillsNone(t *testing.T) {
	cfg := &Config{Model: "big-pickle"}
	pool := []string{"tdd", "research"}
	def := &AgentDef{Name: "x", Source: "/x.toml", Skills: []string{}}

	resolved := ResolveAgentDef(def, cfg, pool)

	if resolved.Skills == nil {
		t.Fatal("Skills = nil, want non-nil empty (skills = [] selects none)")
	}
	if len(resolved.Skills) != 0 {
		t.Errorf("Skills = %v, want empty", resolved.Skills)
	}
}

func TestResolveAgentDefSkillsSelect(t *testing.T) {
	cfg := &Config{Model: "big-pickle"}
	pool := []string{"tdd", "research", "grilling"}
	def := &AgentDef{Name: "x", Source: "/x.toml", Skills: []string{"grilling", "tdd"}}

	resolved := ResolveAgentDef(def, cfg, pool)

	if len(resolved.Skills) != 2 || resolved.Skills[0] != "grilling" || resolved.Skills[1] != "tdd" {
		t.Errorf("Skills = %v, want [grilling tdd]", resolved.Skills)
	}
}

func TestResolveAgentDefDoesNotMutate(t *testing.T) {
	cfg := &Config{
		Model:           "big-pickle",
		InstructionFile: "/cfg/instr.md",
		Tools:           Tools{Read: true, Write: true, Edit: true, Bash: true},
	}
	pool := []string{"tdd", "research"}
	def := &AgentDef{Name: "x", Model: "fast", Skills: []string{"research"}}
	resolved := ResolveAgentDef(def, cfg, pool)

	// Mutate resolved — should not affect original def or cfg.
	resolved.Model = "mutated"
	resolved.Skills[0] = "mutated"
	if def.Model != "fast" {
		t.Errorf("original AgentDef was mutated")
	}
	if def.Skills[0] != "research" {
		t.Errorf("original AgentDef Skills was mutated")
	}
	if pool[0] != "tdd" || pool[1] != "research" {
		t.Errorf("input pool was mutated")
	}
}

func TestAgentRegListEmpty(t *testing.T) {
	reg := NewAgentReg(t.TempDir(), t.TempDir())
	names := reg.List()
	if len(names) != 0 {
		t.Errorf("List() = %v, want empty", names)
	}
}

func TestAgentRegScanGlobal(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `model = "fast"`)
	reg := NewAgentReg(dir, t.TempDir())

	names := reg.List()
	if len(names) != 1 || names[0] != "coder" {
		t.Errorf("List() = %v, want [coder]", names)
	}
}

func TestAgentRegScanLocal(t *testing.T) {
	globalDir := t.TempDir()
	localDir := t.TempDir()
	writeAgentFile(t, localDir, "reviewer.toml", `tools = ["read"]`)

	reg := NewAgentReg(globalDir, localDir)
	names := reg.List()
	if len(names) != 1 || names[0] != "reviewer" {
		t.Errorf("List() = %v, want [reviewer]", names)
	}
}

func TestAgentRegLocalOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	localDir := t.TempDir()
	writeAgentFile(t, globalDir, "coder.toml", `model = "slow"`)
	writeAgentFile(t, localDir, "coder.toml", `model = "fast"`)

	reg := NewAgentReg(globalDir, localDir)
	def, err := reg.Get("coder")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if def == nil {
		t.Fatal("Get returned nil for existing agent")
	}
	if def.Model != "fast" {
		t.Errorf("Model = %q, want %q (local should override global)", def.Model, "fast")
	}
	if def.Source != filepath.Join(localDir, "coder.toml") {
		t.Errorf("Source = %q, want local path", def.Source)
	}
}

func TestAgentRegDotfilesSkipped(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, ".hidden.toml", `model = "x"`)
	writeAgentFile(t, dir, "visible.toml", `model = "y"`)

	reg := NewAgentReg(dir, t.TempDir())
	names := reg.List()
	if len(names) != 1 || names[0] != "visible" {
		t.Errorf("List() = %v, want [visible]", names)
	}
}

func TestAgentRegNonTomlSkipped(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `model = "fast"`)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)

	reg := NewAgentReg(dir, t.TempDir())
	names := reg.List()
	if len(names) != 1 || names[0] != "coder" {
		t.Errorf("List() = %v, want [coder]", names)
	}
}

func TestAgentRegMissingDirNotError(t *testing.T) {
	reg := NewAgentReg("/nonexistent/global", "/nonexistent/local")
	names := reg.List()
	if len(names) != 0 {
		t.Errorf("List() = %v, want empty for missing dirs", names)
	}
}

func TestAgentRegGetResolved(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `tools = ["read"]`)
	cfg := &Config{
		Model:           "big-pickle",
		InstructionFile: "/cfg/instr.md",
		Tools:           Tools{Read: true, Write: true, Edit: true, Bash: true},
	}

	reg := NewAgentReg(dir, t.TempDir())
	resolved, err := reg.GetResolved("coder", cfg, nil)
	if err != nil {
		t.Fatalf("GetResolved: %v", err)
	}
	if resolved.Name != "coder" {
		t.Errorf("Name = %q, want %q", resolved.Name, "coder")
	}
	if resolved.Model != "big-pickle" {
		t.Errorf("Model = %q, want %q (should inherit config)", resolved.Model, "big-pickle")
	}
	if len(resolved.Tools) != 1 || resolved.Tools[0] != "read" {
		t.Errorf("Tools = %v, want [read]", resolved.Tools)
	}
	if resolved.Skills == nil {
		t.Fatal("Skills = nil, want non-nil (inherits empty pool)")
	}
}

func TestAgentRegGetResolvedSkillsInherit(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `model = "fast"`)
	cfg := &Config{Model: "big-pickle"}
	pool := []string{"tdd", "research"}

	reg := NewAgentReg(dir, t.TempDir())
	resolved, err := reg.GetResolved("coder", cfg, pool)
	if err != nil {
		t.Fatalf("GetResolved: %v", err)
	}
	if len(resolved.Skills) != 2 || resolved.Skills[0] != "tdd" || resolved.Skills[1] != "research" {
		t.Errorf("Skills = %v, want [tdd research] (inherited from pool)", resolved.Skills)
	}
}

func TestAgentRegGetResolvedUnknownSkill(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `skills = ["ghost", "tdd"]`)
	cfg := &Config{Model: "big-pickle"}

	reg := NewAgentReg(dir, t.TempDir())
	_, err := reg.GetResolved("coder", cfg, []string{"tdd", "research"})
	if err == nil {
		t.Fatal("GetResolved accepted unknown skill name; want an error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %q, want it to name the unknown skill %q", err, "ghost")
	}
}

func TestAgentRegGetResolvedValidSkills(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "coder.toml", `skills = ["tdd", "research"]`)
	cfg := &Config{Model: "big-pickle"}

	reg := NewAgentReg(dir, t.TempDir())
	resolved, err := reg.GetResolved("coder", cfg, []string{"tdd", "research", "grilling"})
	if err != nil {
		t.Fatalf("GetResolved: %v", err)
	}
	if len(resolved.Skills) != 2 || resolved.Skills[0] != "tdd" || resolved.Skills[1] != "research" {
		t.Errorf("Skills = %v, want [tdd research]", resolved.Skills)
	}
}

func TestAgentRegGetResolvedNotFound(t *testing.T) {
	reg := NewAgentReg(t.TempDir(), t.TempDir())
	_, err := reg.GetResolved("nonexistent", &Config{}, nil)
	if err == nil {
		t.Fatal("GetResolved returned nil error for missing agent")
	}
}

func TestAgentRegInvalidTomlSkipped(t *testing.T) {
	dir := t.TempDir()
	// Write invalid TOML — ParseAgentDef will fail, should be skipped.
	os.WriteFile(filepath.Join(dir, "bad.toml"), []byte(`model =`), 0o644)
	writeAgentFile(t, dir, "good.toml", `model = "fast"`)

	reg := NewAgentReg(dir, t.TempDir())
	names := reg.List()
	if len(names) != 1 || names[0] != "good" {
		t.Errorf("List() = %v, want [good] (bad.toml should be skipped)", names)
	}
}

// writeAgentFile creates a .toml agent definition file in the given directory.
func writeAgentFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeAgentFile: %v", err)
	}
}
