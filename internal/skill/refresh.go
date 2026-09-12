package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Refresh checks mtimes of known SKILL.md files and re-parses only changed
// ones, integrating with the discovery cache. The cache is a simple map of
// path to modification time that lives for the session lifetime.
//
// Returns three lists: the names of changed skills (re-parsed because their
// file's mtime moved past the cached value), the names of newly discovered
// skills (in a scanned directory but not in the cache), and the paths of
// removed SKILL.md files (in the cache but no longer on disk). Non-fatal
// issues are reported as warnings. Non-existent directories are skipped.
func Refresh(cache map[string]time.Time, dirs []string) (changed []string, newSkills []string, removed []string, warnings []Warning) {
	seen := make(map[string]bool)
	byName := make(map[string]ParsedSkill)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, Warning{
				Message: fmt.Sprintf("skill: refresh: reading %s: %v", dir, err),
			})
			slog.Warn("skill: refresh: cannot read directory", "path", dir, "error", err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")

			info, err := os.Stat(skillPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				warnings = append(warnings, Warning{
					Message: fmt.Sprintf("skill: refresh: stat %s: %v", skillPath, err),
				})
				slog.Warn("skill: refresh: cannot stat", "path", skillPath, "error", err)
				continue
			}
			seen[skillPath] = true

			currentMtime := info.ModTime()
			cachedMtime, inCache := cache[skillPath]

			if inCache && !currentMtime.After(cachedMtime) {
				continue
			}

			raw, err := os.ReadFile(skillPath)
			if err != nil {
				warnings = append(warnings, Warning{
					Message: fmt.Sprintf("skill: refresh: reading %s: %v", skillPath, err),
				})
				slog.Warn("skill: refresh: cannot read", "path", skillPath, "error", err)
				continue
			}

			cache[skillPath] = currentMtime

			parsed, err := Parse(string(raw))
			if err != nil {
				warnings = append(warnings, Warning{
					Message: fmt.Sprintf("skill: refresh: skipping %s: %v", skillPath, err),
				})
				slog.Warn("skill: refresh: invalid SKILL.md", "path", skillPath, "error", err)
				continue
			}

			if _, ok := byName[parsed.Name]; ok {
				warnings = append(warnings, Warning{
					Message: fmt.Sprintf("skill: refresh: duplicate skill %q in %s, skipped", parsed.Name, skillPath),
				})
				slog.Warn("skill: refresh: duplicate skipped", "name", parsed.Name, "path", skillPath)
				continue
			}

			byName[parsed.Name] = parsed

			if inCache {
				changed = append(changed, parsed.Name)
			} else {
				newSkills = append(newSkills, parsed.Name)
			}
		}
	}

	for path := range cache {
		if !seen[path] {
			delete(cache, path)
			removed = append(removed, path)
			warnings = append(warnings, Warning{
				Message: fmt.Sprintf("skill: refresh: removed SKILL.md: %s", path),
			})
			slog.Warn("skill: refresh: SKILL.md removed", "path", path)
		}
	}

	return changed, newSkills, removed, warnings
}