package llm_test

import (
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
)

func TestNewScanner_ReadsLines(t *testing.T) {
	input := "line 1\nline 2\nline 3\n"
	sc := llm.NewScanner(strings.NewReader(input))

	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("unexpected scanner error: %v", err)
	}
	want := []string{"line 1", "line 2", "line 3"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewScanner_HandlesLongLines(t *testing.T) {
	// 512 KiB line — well under the 1 MiB max but over the default 64 KiB.
	long := strings.Repeat("x", 512<<10)
	sc := llm.NewScanner(strings.NewReader(long + "\n"))

	if !sc.Scan() {
		t.Fatalf("expected to scan long line, err: %v", sc.Err())
	}
	if len(sc.Text()) != 512<<10 {
		t.Errorf("got line length %d, want %d", len(sc.Text()), 512<<10)
	}
}

func TestNewScanner_EmptyInput(t *testing.T) {
	sc := llm.NewScanner(strings.NewReader(""))
	if sc.Scan() {
		t.Fatal("expected no lines from empty input")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
