package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Warning records a non-fatal issue encountered during skill discovery.
type Warning struct {
	Message string
}

// Discover scans ordered directories for <name>/SKILL.md files, parses each
// one, and deduplicates by directory priority. Non-existent directories are
// silently skipped. Invalid SKILL.md files produce a warning and are skipped.
// Within the same directory, the first skill by name wins; across directories,
// the lowest-indexed directory wins.
func Discover(dirs []string) ([]ParsedSkill, []Warning, error) {
	var (
		skills         []ParsedSkill
		warns          []Warning
		firstDirByName = map[string]string{} // dedup registry: skill name -> first directory seen in
	)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("skill: discover: reading %s: %w", dir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			raw, err := os.ReadFile(skillPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, nil, fmt.Errorf("skill: discover: reading %s: %w", skillPath, err)
			}

			parsed, err := Parse(string(raw))
			if err != nil {
				warns = append(warns, Warning{
					Message: fmt.Sprintf("skill: discover: skipping %s: %v", skillPath, err),
				})
				slog.Warn("skill: discover: invalid SKILL.md skipped", "path", skillPath, "error", err)
				continue
			}

			if firstDir, ok := firstDirByName[parsed.Name]; ok {
				if firstDir == dir {
					warns = append(warns, Warning{
						Message: fmt.Sprintf("skill: discover: duplicate skill %q in %s, skipped", parsed.Name, skillPath),
					})
					slog.Warn("skill: discover: duplicate skipped", "name", parsed.Name, "path", skillPath)
				}
				continue
			}

			firstDirByName[parsed.Name] = dir
			skills = append(skills, parsed)
		}
	}

	return skills, warns, nil
}
