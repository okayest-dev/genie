package style

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func roleProfile() *RenderProfile {
	return &RenderProfile{
		IsTTY:         true,
		ColourProfile: ColourProfileTrueColor,
		GlyphTier:     GlyphTierNerd,
		Width:         80,
	}
}

func TestRoleWriterStylesBytesOnDisplayProfile(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRoleWriter(&buf, NewRenderer(roleProfile()), Style{Foreground: "cyan"})
	if _, err := rw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := buf.String(), "\x1b[36mhello\x1b[0m"; got != want {
		t.Errorf("styled output = %q, want %q", got, want)
	}
}

func TestRoleWriterHexForegroundDownconverts(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRoleWriter(&buf, NewRenderer(roleProfile()), Style{Foreground: "#ff0000", Attributes: []string{"bold"}})
	if _, err := rw.Write([]byte("boom")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := buf.String(), "\x1b[38;2;255;0;0;1mboom\x1b[0m"; got != want {
		t.Errorf("styled output = %q, want %q", got, want)
	}
}

func TestRoleWriterAsciiProfileDropsColourKeepsAttributes(t *testing.T) {
	var buf bytes.Buffer
	profile := &RenderProfile{IsTTY: true, ColourProfile: ColourProfileAscii, GlyphTier: GlyphTierAscii, Width: 80}
	rw := NewRoleWriter(&buf, NewRenderer(profile), Style{Foreground: "red", Attributes: []string{"bold"}})
	if _, err := rw.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := buf.String(), "\x1b[1mhi\x1b[0m"; got != want {
		t.Errorf("ascii output = %q, want bold only", got)
	}
}

func TestRoleWriterAsciiProfileNoAttributesIsByteIdentical(t *testing.T) {
	var buf bytes.Buffer
	profile := &RenderProfile{IsTTY: true, ColourProfile: ColourProfileAscii, GlyphTier: GlyphTierAscii, Width: 80}
	rw := NewRoleWriter(&buf, NewRenderer(profile), Style{Foreground: "cyan"})
	if _, err := rw.Write([]byte("raw")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "raw" {
		t.Errorf("ascii output = %q, want byte-identical plain", got)
	}
}

func TestRoleWriterZeroStyleRendersPlain(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRoleWriter(&buf, NewRenderer(roleProfile()), Style{})
	if _, err := rw.Write([]byte("raw")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "raw" {
		t.Errorf("zero-style output = %q, want plain passthrough", got)
	}
}

func TestRoleWriterWrapsEachWrite(t *testing.T) {
	var buf bytes.Buffer
	rw := NewRoleWriter(&buf, NewRenderer(roleProfile()), Style{Foreground: "cyan"})
	if _, err := rw.Write([]byte("a")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if _, err := rw.Write([]byte("b")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if got, want := buf.String(), "\x1b[36ma\x1b[0m\x1b[36mb\x1b[0m"; got != want {
		t.Errorf("output = %q, want per-write styling %q", got, want)
	}
}

func TestRoleWriterPropagatesWriteError(t *testing.T) {
	fail := &errWriter{err: errors.New("boom")}
	rw := NewRoleWriter(fail, NewRenderer(roleProfile()), Style{Foreground: "cyan"})
	n, err := rw.Write([]byte("x"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want boom", err)
	}
	if n != 1 {
		t.Errorf("n = %d, want len(p) on error", n)
	}
}

type errWriter struct {
	err error
}

func (w *errWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}