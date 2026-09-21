package permissions

import (
	"strings"
	"testing"
)

// marker is the canonical NotCapable marker a stub emitter returns mid-call.
const marker = `{"code":"ERR_PERMISSION_DENIED","permission":"net","resource":"api.example.com"}`

func TestMapNotCapableRewritesMarkerToComposite(t *testing.T) {
	want := golden(t, "notcapable_composite.golden")
	got, mapped := MapNotCapable(marker)
	if !mapped {
		t.Fatalf("MapNotCapable(%q) reported no map; want the composite", marker)
	}
	if got != want {
		t.Errorf("not-capable composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestMapNotCapableWholeTextReplaced(t *testing.T) {
	// The marker may sit inside a longer tool result; the whole result is
	// replaced so the model always sees a clean composite.
	text := "2026/09/20 10:00:00 tool completing\n" + marker + "\n(1 more bytes)"
	got, mapped := MapNotCapable(text)
	if !mapped {
		t.Fatalf("MapNotCapable(%q) reported no map; want the composite", text)
	}
	if strings.Contains(got, marker) {
		t.Errorf("composite still carries the raw marker: %q", got)
	}
	want := golden(t, "notcapable_composite.golden")
	if got != want {
		t.Errorf("composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestMapNotCapableCompositePerAxis(t *testing.T) {
	for _, a := range []Axis{AxisRead, AxisWrite, AxisNet, AxisRun, AxisEnv} {
		nc := `{"code":"ERR_PERMISSION_DENIED","permission":"` + string(a) + `","resource":"x"}`
		got, mapped := MapNotCapable(nc)
		if !mapped {
			t.Fatalf("MapNotCapable(%q) reported no map", nc)
		}
		if !strings.Contains(got, string(a)) {
			t.Errorf("composite for axis %q missing axis name: %q", a, got)
		}
		if !strings.HasPrefix(got, "status: call not executed") {
			t.Errorf("composite missing status line: %q", got)
		}
	}
}

func TestMapNotCapableKeyOrderInsensitive(t *testing.T) {
	// The code key may appear before or after the permission key; both are
	// valid structured markers.
	nc := `{"permission":"read","resource":"/etc/hosts","code":"ERR_PERMISSION_DENIED"}`
	got, mapped := MapNotCapable(nc)
	if !mapped {
		t.Fatalf("MapNotCapable(%q) reported no map", nc)
	}
	if !strings.HasPrefix(got, "status: call not executed") {
		t.Errorf("composite missing status line: %q", got)
	}
}

func TestMapNotCapableWhitespaceTolerant(t *testing.T) {
	nc := "{\n  \"permission\": \"read\",\n  \"resource\": \"/etc/hosts\",\n  \"code\": \"ERR_PERMISSION_DENIED\"\n}"
	got, mapped := MapNotCapable(nc)
	if !mapped {
		t.Fatalf("MapNotCapable(%q) reported no map", nc)
	}
	if !strings.HasPrefix(got, "status: call not executed") {
		t.Errorf("composite missing status line: %q", got)
	}
}

func TestMapNotCapablePlainTextUntouched(t *testing.T) {
	for _, text := range []string{"", "created /work/x.go", "Error: read /etc/hosts: permission denied"} {
		got, mapped := MapNotCapable(text)
		if mapped {
			t.Errorf("MapNotCapable(%q) reported a map; want untouched", text)
		}
		if got != text {
			t.Errorf("MapNotCapable(%q) = %q, want untouched", text, got)
		}
	}
}

func TestMapNotCapableDifferentCodeUntouched(t *testing.T) {
	// The marker code must match exactly; a tool error that merely mentions
	// the shape but not the code is left alone.
	nc := `{"code":"ERR_OTHER","permission":"read","resource":"/etc/hosts"}`
	got, mapped := MapNotCapable(nc)
	if mapped {
		t.Errorf("MapNotCapable(%q) reported a map; want untouched", nc)
	}
	if got != nc {
		t.Errorf("MapNotCapable(%q) = %q, want untouched", nc, got)
	}
}

func TestMapNotCapableUnknownAxisUntouched(t *testing.T) {
	nc := `{"code":"ERR_PERMISSION_DENIED","permission":"magic","resource":"/etc/hosts"}`
	got, mapped := MapNotCapable(nc)
	if mapped {
		t.Errorf("MapNotCapable(%q) reported a map; want untouched", nc)
	}
	if got != nc {
		t.Errorf("MapNotCapable(%q) = %q, want untouched", nc, got)
	}
}

func TestMapNotCapableMalformedJSONUntouched(t *testing.T) {
	nc := `{"code":"ERR_PERMISSION_DENIED","permission":`
	got, mapped := MapNotCapable(nc)
	if mapped {
		t.Errorf("MapNotCapable(%q) reported a map; want untouched", nc)
	}
	if got != nc {
		t.Errorf("MapNotCapable(%q) = %q, want untouched", nc, got)
	}
}

func TestParseNotCapableUnterminatedJSON(t *testing.T) {
	// parseNotCapable must reject text whose opening brace never closes; the
	// harness treats it as ordinary output rather than a NotCapable marker.
	if nc, ok := parseNotCapable(`{"code":"ERR_PERMISSION_DENIED"`); ok {
		t.Errorf("parseNotCapable on unterminated JSON reported ok, got %+v", nc)
	}
}

func TestParseNotCapableQuotedBrace(t *testing.T) {
	// A "}" inside the marker's strings must not truncate the shape.
	nc, ok := parseNotCapable(`{"code":"ERR_PERMISSION_DENIED","permission":"net","resource":"a}b"}`)
	if !ok {
		t.Fatalf("parseNotCapable on quoted-brace JSON reported no map")
	}
	if nc.Resource != "a}b" {
		t.Errorf("resource = %q, want %q", nc.Resource, "a}b")
	}
	got, mapped := MapNotCapable(`result="{"code":"ERR_PERMISSION_DENIED","permission":"net","resource":"a}b"}" text`)
	if !mapped {
		t.Fatalf("MapNotCapable on quoted-brace marker reported no map")
	}
	if want := golden(t, "notcapable_composite.golden"); got != want {
		t.Errorf("composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestMapNotCapableEscapedQuote(t *testing.T) {
	marker := `{"code":"ERR_PERMISSION_DENIED","permission":"net","resource":"a\"b"}`
	nc, ok := parseNotCapable(marker)
	if !ok {
		t.Fatalf("parseNotCapable on escaped-quote marker reported no map")
	}
	if nc.Resource != `a"b` {
		t.Errorf("resource = %q, want %q", nc.Resource, `a"b`)
	}
	got, mapped := MapNotCapable(marker)
	if !mapped {
		t.Fatalf("MapNotCapable on escaped-quote marker reported no map")
	}
	if want := golden(t, "notcapable_composite.golden"); got != want {
		t.Errorf("composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestSkipQuotedUnterminated(t *testing.T) {
	// skipQuoted must stop at the end of text when the literal never closes.
	if i := skipQuoted(`"abc`, 0); i != 4 {
		t.Errorf("skipQuoted index = %d, want 4", i)
	}
	if i := skipQuoted(`"a\"`, 0); i != 4 {
		t.Errorf("skipQuoted on trailing escape index = %d, want 4", i)
	}
}

func TestParseNotCapableNonJSONObject(t *testing.T) {
	// Content between braces that is not a JSON object must not parse.
	if _, ok := parseNotCapable(`{not json at all}`); ok {
		t.Errorf("parseNotCapable on non-JSON braces reported ok")
	}
}

func TestRenderNotCapableCompositeMatchesGolden(t *testing.T) {
	want := golden(t, "notcapable_composite.golden")
	if got := RenderNotCapableComposite(AxisNet); got != want {
		t.Errorf("composite mismatch:\n got: %q\nwant: %q", got, want)
	}
}
