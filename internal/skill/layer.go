package skill

import "strings"

// BuildSkillLayer assembles the ## Skills section for the instruction
// pipeline. The layer is injected between the instruction file and AGENTS.md.
// When bound is empty or nil, returns "" (no layer injected).
//
// The layer is index-only: one line per bound skill, no bodies. A body costs
// nothing until the model asks for it, at which point the skill tool returns it
// as a tool result and it becomes part of the conversation from then on. The
// index costs a line per skill and is what lets the model decide.
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

	return b.String()
}
