package tracker

import "testing"

func TestIsolationString(t *testing.T) {
	for i, want := range []string{"unknown", "shared", "per_checkout"} {
		if got := Isolation(i).String(); got != want {
			t.Errorf("Isolation(%d).String() = %q, want %q", i, got, want)
		}
	}
	if got := Isolation(99).String(); got != "unknown" {
		t.Errorf("invalid Isolation(99).String() = %q, want %q", got, "unknown")
	}
}

func TestIsolationWorktreeSafe(t *testing.T) {
	cases := []struct {
		i    Isolation
		want bool
	}{
		{IsolationUnknown, false},
		{IsolationShared, true},
		{IsolationPerCheckout, false},
	}
	for _, c := range cases {
		if got := c.i.WorktreeSafe(); got != c.want {
			t.Errorf("Isolation(%d).WorktreeSafe() = %v, want %v", c.i, got, c.want)
		}
	}
}

func TestParseIsolation(t *testing.T) {
	cases := []struct {
		in   string
		want Isolation
	}{
		{"shared", IsolationShared},
		{"per_checkout", IsolationPerCheckout},
		{"", IsolationUnknown},
		{"unknown", IsolationUnknown},
		{"remote", IsolationUnknown},
	}
	for _, c := range cases {
		if got := ParseIsolation(c.in); got != c.want {
			t.Errorf("ParseIsolation(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}