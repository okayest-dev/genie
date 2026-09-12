package skill

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func statMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func touch(t *testing.T, path string, ttl time.Duration) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime().Add(ttl)
	if err := os.Chtimes(path, time.Now(), mtime); err != nil {
		t.Fatal(err)
	}
}

func sortedStrings(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestRefresh(t *testing.T) {
	const changedSkill = `---
name: changed-skill
description: A changed skill.
---
Original body.
`
	const changedContent = `---
name: changed-skill
description: A changed skill.
---
New body.
`

	tests := []struct {
		name        string
		cachePaths  func(t *testing.T, dirs []string) map[string]time.Time
		dirs        func(t *testing.T) []string
		mutate      func(t *testing.T, dirs []string)
		wantChanged []string
		wantNew     []string
		wantRemoved func(t *testing.T, dirs []string) []string
		wantWarns   int
	}{
		{
			name: "no changes",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", validSkill)
				writeSkill(t, d, "beta", anotherSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
					filepath.Join(dirs[0], "beta", "SKILL.md"):  statMtime(t, filepath.Join(dirs[0], "beta", "SKILL.md")),
				}
			},
		},
		{
			name: "mtime changed re-parsed",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", changedSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
				}
			},
			mutate: func(t *testing.T, dirs []string) {
				path := filepath.Join(dirs[0], "alpha", "SKILL.md")
				if err := os.WriteFile(path, []byte(changedContent), 0o644); err != nil {
					t.Fatal(err)
				}
				touch(t, path, time.Hour)
			},
			wantChanged: []string{"changed-skill"},
		},
		{
			name: "same mtime not re-parsed",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", changedSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
				}
			},
			mutate: func(t *testing.T, dirs []string) {
				path := filepath.Join(dirs[0], "alpha", "SKILL.md")
				originalMtime := statMtime(t, path)
				os.WriteFile(path, []byte(changedContent), 0o644)
				if err := os.Chtimes(path, time.Now(), originalMtime); err != nil {
					t.Fatal(err)
				}
			},
			wantChanged: nil,
		},
		{
			name: "new file discovered",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", validSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
				}
			},
			mutate: func(t *testing.T, dirs []string) {
				writeSkill(t, dirs[0], "beta", anotherSkill)
			},
			wantNew: []string{"another-skill"},
		},
		{
			name: "file removed",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", validSkill)
				writeSkill(t, d, "beta", anotherSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
					filepath.Join(dirs[0], "beta", "SKILL.md"):  statMtime(t, filepath.Join(dirs[0], "beta", "SKILL.md")),
				}
			},
			mutate: func(t *testing.T, dirs []string) {
				if err := os.Remove(filepath.Join(dirs[0], "alpha", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			wantRemoved: func(t *testing.T, dirs []string) []string {
				return []string{filepath.Join(dirs[0], "alpha", "SKILL.md")}
			},
			wantWarns: 1,
		},
		{
			name: "mixed changes",
			dirs: func(t *testing.T) []string {
				d := t.TempDir()
				writeSkill(t, d, "alpha", changedSkill)
				writeSkill(t, d, "beta", anotherSkill)
				return []string{d}
			},
			cachePaths: func(t *testing.T, dirs []string) map[string]time.Time {
				return map[string]time.Time{
					filepath.Join(dirs[0], "alpha", "SKILL.md"): statMtime(t, filepath.Join(dirs[0], "alpha", "SKILL.md")),
					filepath.Join(dirs[0], "beta", "SKILL.md"):  statMtime(t, filepath.Join(dirs[0], "beta", "SKILL.md")),
				}
			},
			mutate: func(t *testing.T, dirs []string) {
				alpha := filepath.Join(dirs[0], "alpha", "SKILL.md")
				os.WriteFile(alpha, []byte(changedContent), 0o644)
				touch(t, alpha, time.Hour)

				writeSkill(t, dirs[0], "gamma", validSkill)

				if err := os.Remove(filepath.Join(dirs[0], "beta", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			wantChanged: []string{"changed-skill"},
			wantNew:     []string{"test-skill"},
			wantRemoved: func(t *testing.T, dirs []string) []string {
				return []string{filepath.Join(dirs[0], "beta", "SKILL.md")}
			},
			wantWarns: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := tt.dirs(t)
			cache := tt.cachePaths(t, dirs)
			if tt.mutate != nil {
				tt.mutate(t, dirs)
			}

			changed, newSkills, removed, warns := Refresh(cache, dirs)

			if !equalStrings(sortedStrings(changed), sortedStrings(tt.wantChanged)) {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if !equalStrings(sortedStrings(newSkills), sortedStrings(tt.wantNew)) {
				t.Errorf("newSkills = %v, want %v", newSkills, tt.wantNew)
			}
			var wantRemoved []string
			if tt.wantRemoved != nil {
				wantRemoved = tt.wantRemoved(t, dirs)
			}
			if !equalStrings(sortedStrings(removed), sortedStrings(wantRemoved)) {
				t.Errorf("removed = %v, want %v", removed, wantRemoved)
			}
			if len(warns) != tt.wantWarns {
				t.Errorf("got %d warnings, want %d", len(warns), tt.wantWarns)
			}
		})
	}
}

func TestRefreshCacheUpdatedForChangedFile(t *testing.T) {
	d := t.TempDir()
	writeSkill(t, d, "alpha", validSkill)

	path := filepath.Join(d, "alpha", "SKILL.md")
	cache := map[string]time.Time{path: statMtime(t, path)}

	changed, newSkills, _, _ := Refresh(cache, []string{d})
	if len(changed) != 0 || len(newSkills) != 0 {
		t.Fatalf("first refresh should report no changes, got changed=%v new=%v", changed, newSkills)
	}

	os.WriteFile(path, []byte(validSkill), 0o644)
	touch(t, path, time.Hour)

	changed, _, _, _ = Refresh(cache, []string{d})
	if len(changed) != 1 || changed[0] != "test-skill" {
		t.Fatalf("second refresh should report change, got %v", changed)
	}

	changed, _, _, _ = Refresh(cache, []string{d})
	if len(changed) != 0 {
		t.Fatalf("third refresh should report no changes, got %v", changed)
	}
}

func TestRefreshRemovedFilePurgedFromCache(t *testing.T) {
	d := t.TempDir()
	writeSkill(t, d, "alpha", validSkill)

	path := filepath.Join(d, "alpha", "SKILL.md")
	cache := map[string]time.Time{path: statMtime(t, path)}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	_, _, removed, _ := Refresh(cache, []string{d})
	if len(removed) != 1 || removed[0] != path {
		t.Fatalf("removed = %v, want [%s]", removed, path)
	}
	if _, ok := cache[path]; ok {
		t.Errorf("cache still contains removed path %s", path)
	}

	_, _, removed, _ = Refresh(cache, []string{d})
	if len(removed) != 0 {
		t.Fatalf("second refresh should not re-report removal, got %v", removed)
	}
}

func TestRefreshNonExistentDirSkipped(t *testing.T) {
	cache := map[string]time.Time{}
	changed, newSkills, removed, warns := Refresh(cache, []string{"/nonexistent/path/abc"})
	if len(changed) != 0 || len(newSkills) != 0 || len(removed) != 0 || len(warns) != 0 {
		t.Fatalf("non-existent dir should be silently skipped, got changed=%v new=%v removed=%v warns=%v",
			changed, newSkills, removed, warns)
	}
}

func TestRefreshRemovedWarningMentionsPath(t *testing.T) {
	d := t.TempDir()
	writeSkill(t, d, "alpha", validSkill)

	path := filepath.Join(d, "alpha", "SKILL.md")
	cache := map[string]time.Time{path: statMtime(t, path)}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	_, _, _, warns := Refresh(cache, []string{d})
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warns))
	}
	if !strings.Contains(warns[0].Message, "removed") {
		t.Errorf("warning = %q, want it to mention 'removed'", warns[0].Message)
	}
	if !strings.Contains(warns[0].Message, path) {
		t.Errorf("warning = %q, want it to mention %q", warns[0].Message, path)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}