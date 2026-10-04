package permissions

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NotCapable is the structured shape a tool returns mid-call when a permission
// is unavailable at runtime; the harness maps it into the pinned
// status/hint composite (og-uy5.5).
type NotCapable struct {
	Code       string `json:"code"`
	Permission string `json:"permission"`
	Resource   string `json:"resource"`
}

// NotCapableCode is the code a tool emits when its call is denied by a runtime
// permission check.
const NotCapableCode = "ERR_PERMISSION_DENIED"

// MapNotCapable rewrites a tool result carrying a "ERR_PERMISSION_DENIED"
// marker into the pinned status/hint composite, returning (composite, true).
// Any other content returns (text, false) untouched, so the rewrite never
// mangles ordinary tool output or unrelated structured errors.
func MapNotCapable(text string) (string, bool) {
	marker := findNotCapable(text)
	if marker == -1 {
		return text, false
	}
	nc, ok := ParseNotCapable(text[marker:])
	if !ok {
		return text, false
	}
	if nc.Code != NotCapableCode || !IsAxis(nc.Permission) {
		return text, false
	}
	return RenderNotCapableComposite(Axis(nc.Permission)), true
}

// ParseNotCapable parses a NotCapable marker from text that starts with a
// JSON object. It handles quoted strings correctly so a "}" inside a marker's
// strings never truncates the shape. Returns (NotCapable{}, false) if parsing
// fails or the text doesn't contain a valid marker.
func ParseNotCapable(text string) (NotCapable, bool) {
	if len(text) == 0 || text[0] != '{' {
		return NotCapable{}, false
	}
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"':
			i = skipQuoted(text, i)
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var nc NotCapable
				if err := json.Unmarshal([]byte(text[:i+1]), &nc); err != nil {
					return NotCapable{}, false
				}
				return nc, true
			}
		}
	}
	return NotCapable{}, false
}

// RenderNotCapableComposite renders the pinned model-facing composite for a
// mid-call runtime permission denial (og-uy5.5). Unlike the inline escalation
// composites, there is no grant/reject line — the call already lost permission
// mid-execution, so only the status/hint pair is shown.
func RenderNotCapableComposite(axis Axis) string {
	return fmt.Sprintf(
		"status: call not executed — %s unavailable at runtime\n"+
			"hint: inline escalation is not available mid-execution — request %s access in advance via request_permission, or reformulate",
		axis, axis)
}

// RenderNotCapableMarker returns the JSON marker string for a NotCapable
// denial, properly escaped for use as a tool result.
func RenderNotCapableMarker(permission, resource string) string {
	nc := NotCapable{
		Code:       NotCapableCode,
		Permission: permission,
		Resource:   resource,
	}
	b, _ := json.Marshal(nc)
	return string(b)
}

// findNotCapable locates the object holding the first "ERR_PERMISSION_DENIED"
// marker in text, returning the opening brace's index; a bare marker outside a
// JSON object yields -1.
func findNotCapable(text string) int {
	idx := strings.Index(text, NotCapableCode)
	if idx == -1 {
		return -1
	}
	return strings.LastIndex(text[:idx], "{")
}

// FindAndParseNotCapable locates and parses a NotCapable marker in text.
// It searches for the ERR_PERMISSION_DENIED code, finds the enclosing JSON
// object, and parses it. Returns (NotCapable{}, false) if no valid marker is found.
func FindAndParseNotCapable(text string) (NotCapable, bool) {
	marker := findNotCapable(text)
	if marker == -1 {
		return NotCapable{}, false
	}
	return ParseNotCapable(text[marker:])
}

// skipQuoted returns the index of the quote closing the JSON string that opens
// at text[i], honouring backslash escapes; it walks to the end of text if the
// literal never closes.
func skipQuoted(text string, i int) int {
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '"' {
			return i
		}
	}
	return i
}
