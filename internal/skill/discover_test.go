package skill

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const validSkill = `---
name: test-skill
description: A test skill.
---
Body content.
`

const anotherSkill = `---
name: another-skill
description: Another skill.
---
`

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name      string
		dirs      func(t *testing.T) []string
		want      []string
		wantWarns int
		wantErr   string
	}{
		{
			name: "happy path single dir",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", validSkill)
				writeSkill(t, d, "beta", anotherSkill)
				return []string{d}
			},
			want: []string{"test-skill", "another-skill"},
		},
		{
			name: "non-existent dir skipped",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", validSkill)
				return []string{d, "/nonexistent/path/xyz"}
			},
			want: []string{"test-skill"},
		},
		{
			name: "cross-dir dedup first wins",
			dirs: func(t *testing.T) []string {
				d1 := t.TempDir()
				d2 := t.TempDir()
				writeSkill(t, d1, "shared", `---
name: mine
description: From first dir.
---
`)
				writeSkill(t, d2, "shared", `---
name: mine
description: From second dir.
---
`)
				return []string{d1, d2}
			},
			want: []string{"mine"},
		},
		{
			name: "same-dir dedup first wins with warning",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "dup-a", validSkill)
				writeSkill(t, d, "dup-b", validSkill)
				return []string{d}
			},
			want:      []string{"test-skill"},
			wantWarns: 1,
		},
		{
			name: "invalid SKILL.md skipped with warning",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "good", validSkill)
				writeSkill(t, d, "bad", "not valid frontmatter at all")
				return []string{d}
			},
			want:      []string{"test-skill"},
			wantWarns: 1,
		},
		{
			name: "mixed valid and invalid across dirs",
			dirs: func(t *testing.T) []string {
				d1 := t.TempDir()
				d2 := t.TempDir()
				writeSkill(t, d1, "alpha", validSkill)
				writeSkill(t, d1, "broken", "bad content")
				writeSkill(t, d2, "beta", anotherSkill)
				return []string{d1, d2}
			},
			want:      []string{"test-skill", "another-skill"},
			wantWarns: 1,
		},
		{
			name: "empty dirs list",
			dirs: func(t *testing.T) []string {
				return []string{}
			},
			want: nil,
		},
		{
			name: "dir with no subdirs",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				return []string{d}
			},
			want: nil,
		},
		{
			name: "dir with files not in subdirs ignored",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(validSkill), 0o644)
				return []string{d}
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := tt.dirs(t)
			got, warns, err := Discover(dirs)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Discover() error = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			if len(warns) != tt.wantWarns {
				t.Errorf("got %d warnings, want %d", len(warns), tt.wantWarns)
			}
			var names []string
			for _, s := range got {
				names = append(names, s.Name)
			}
			sort.Strings(names)
			sort.Strings(tt.want)
			if len(names) != len(tt.want) {
				t.Fatalf("got %d skills %v, want %d %v", len(names), names, len(tt.want), tt.want)
			}
			for i := range names {
				if names[i] != tt.want[i] {
					t.Errorf("skill[%d] = %q, want %q", i, names[i], tt.want[i])
				}
			}
		})
	}
}

func TestDiscoverCrossDirPriority(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()

	writeSkill(t, d1, "mine", `---
name: mine
description: From first dir.
---
First body.
`)
	writeSkill(t, d2, "mine", `---
name: mine
description: From second dir.
---
Second body.
`)

	got, _, err := Discover([]string{d1, d2})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1", len(got))
	}
	if got[0].Description != "From first dir." {
		t.Errorf("Description = %q, want %q (first dir should win)", got[0].Description, "From first dir.")
	}
	if got[0].Body != "First body." {
		t.Errorf("Body = %q, want %q", got[0].Body, "First body.")
	}
}

func TestDiscoverSameDirDuplicateName(t *testing.T) {
	d := t.TempDir()

	writeSkill(t, d, "skill-a", validSkill)
	writeSkill(t, d, "skill-b", validSkill)

	got, warns, err := Discover([]string{d})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1 (dedup)", len(got))
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Message, "test-skill") {
		t.Errorf("warning = %q, want it to mention 'test-skill'", warns[0].Message)
	}
}

func TestDiscoverInvalidSkillWarning(t *testing.T) {
	d := t.TempDir()
	writeSkill(t, d, "broken", "garbage content no frontmatter")

	_, warns, err := Discover([]string{d})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Message, "broken") {
		t.Errorf("warning = %q, want it to mention 'broken'", warns[0].Message)
	}
}
