package skill

import "strings"

// BuildSkillLayer assembles the ## Skills section for the instruction
// pipeline. The layer is injected between the instruction file and AGENTS.md.
// When bound is empty or nil, returns "" (no layer injected).
func BuildSkillLayer(bound []ParsedSkill) string {
	if len(bound) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Skills\n")
	b.WriteString("Available skills — engage a skill when its description matches the current task:\n")

	for _, s := range bound {
		line := "- " + s.Name + ": " + s.Description
		if s.ArgumentHint != "" {
			line += " [argument: " + s.ArgumentHint + "]"
		}
		b.WriteString(line + "\n")
	}

	for _, s := range bound {
		if s.Body == "" {
			continue
		}
		b.WriteString("### Skill: " + s.Name + "\n")
		b.WriteString(s.Body)
		b.WriteString("\n")
	}

	return b.String()
}
