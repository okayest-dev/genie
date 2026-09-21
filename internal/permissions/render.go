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

// MechanismText is the terse negotiation mechanism paragraph carried in the
// instruction. It deliberately uses zero tier vocabulary — it never names
// once/session/permanent and never labels base-vs-acquired grants (og-73l.4).
// It states the negotiation lines, the scope-matching semantics, the
// request_permission pre-negotiation fallback, and the re-ask-after-alternative
// rule.
const MechanismText = "When a tool call needs a permission you do not already have, the turn pauses for negotiation and returns a Permission granted: or Permission rejected: result line. A granted scope is reused — paths match by prefix, hosts by subdomain wildcard, executables and environment variables exactly. To request access in advance, call request_permission rather than the tool itself. After a rejection, pursue a materially different alternative before re-asking."

// RenderBaseSnapshot renders the flat, provenance-free session-start snapshot of
// the resolved base policy: one line per axis in fixed rendering order
// (read → write → net → run → env), scopes comma-joined, empty axes reading
// "nothing is authorized". No tier or base-vs-acquired labels ever appear
// (og-73l.4).
func RenderBaseSnapshot(base map[Axis][]string) string {
	var b strings.Builder
	b.WriteString("Current permissions:")
	for _, a := range allAxes {
		scopes := base[a]
		b.WriteString("\n- ")
		b.WriteString(string(a))
		b.WriteString(": ")
		if len(scopes) == 0 {
			b.WriteString("nothing is authorized")
			continue
		}
		b.WriteString(strings.Join(scopes, ", "))
	}
	return b.String()
}

// RenderPermissionsSection renders the session-start base-policy snapshot
// followed by the negotiation mechanism paragraph — the instruction pieces the
// resolved base policy feeds (og-73l.4 / og-uy5.5). base must be the store's
// BaseSnapshot() so tiers never leak into the model's view.
func RenderPermissionsSection(base map[Axis][]string) string {
	return RenderBaseSnapshot(base) + "\n\n" + MechanismText
}
