package permissions

import (
	"fmt"
	"strings"
)

// PromptKey letters accepted by the terse prompt, in display order. Full
// words (once/session/permanent/reject) are also accepted.
const (
	KeyOnce      = "o"
	KeySession   = "s"
	KeyPermanent = "p"
	KeyReject    = "r"
)

// PromptSuffix is the uniform option tail every escalation prompt carries.
const PromptSuffix = "(o)nce/(s)ession/(p)ermanent/(r)eject: "

// AxesLegend lists every axis with its full-word description; shown in the
// negotiation surface so a user never needs to recall an abbreviation.
func AxesLegend() string {
	return "read=file read · write=file write · net=network · run=process · env=environment"
}

// RenderPrompt produces the terse escalation frame for a tool requirement:
//
//	allow write /work/x.go? (o)nce/(s)ession/(p)ermanent/(r)eject:
//	allow net? (o)nce/(s)ession/(p)ermanent/(r)eject:
//
// The frame carries the exact normalized scope on the axis so the request and
// the grant are the same expression. A blanket (axis-only) requirement omits
// the scope entirely.
func RenderPrompt(axis Axis, scope string) string {
	if scope == "" {
		return fmt.Sprintf("allow %s? %s", axis, PromptSuffix)
	}
	return fmt.Sprintf("allow %s %s? %s", axis, scope, PromptSuffix)
}

// RenderUnknownHint is the error the negotiator prints before re-prompting when
// the user's choice was neither a terse key nor a full word.
func RenderUnknownHint() string {
	return ":: unknown choice - o/s/p/r"
}

// RenderGrant renders one grant line, omitting the scope for a blanket grant.
func RenderGrant(axis Axis, scope string) string {
	if scope == "" {
		return fmt.Sprintf("Permission granted: %s", axis)
	}
	return fmt.Sprintf("Permission granted: %s %s", axis, scope)
}

// RenderReject renders one reject line with the constant alternative hint,
// omitting the scope for a blanket requirement.
func RenderReject(axis Axis, scope string) string {
	if scope == "" {
		return fmt.Sprintf("Permission rejected: %s — consider an alternative", axis)
	}
	return fmt.Sprintf("Permission rejected: %s %s — consider an alternative", axis, scope)
}

// RenderGrantLines renders the newly-negotiated grant lines (fixed order as
// given), newline-joined, with no trailing newline. Empty input yields "".
func RenderGrantLines(grants []Grant) string {
	var lines []string
	for _, g := range grants {
		lines = append(lines, RenderGrant(g.Axis, g.Scope))
	}
	return strings.Join(lines, "\n")
}

// RenderGrantedComposite is the model-facing result when the deny point grants
// the call: newly-negotiated grant lines followed by the tool output. There is
// deliberately no separate status line.
func RenderGrantedComposite(grants []Grant, output string) string {
	lines := RenderGrantLines(grants)
	if lines == "" {
		return output
	}
	return lines + "\n" + output
}

// RenderDeniedComposite is the model-facing result when the deny point rejects
// the call: grant lines, reject lines, then the status + hint pair. The call
// never reaches the tool.
func RenderDeniedComposite(grants, rejects []Grant) string {
	var b strings.Builder
	if lines := RenderGrantLines(grants); lines != "" {
		b.WriteString(lines)
		b.WriteByte('\n')
	}
	for _, r := range rejects {
		b.WriteString(RenderReject(r.Axis, r.Scope))
		b.WriteByte('\n')
	}
	b.WriteString("status: call not executed\n")
	b.WriteString("hint: granted axes remain available — reformulate without the denied axis.")
	return b.String()
}

// RenderBlanketScope renders the axis-only (blanket) scope expression, used
// when a requirement declares no concrete scope.
func RenderBlanketScope(axis Axis) string {
	return string(axis)
}
