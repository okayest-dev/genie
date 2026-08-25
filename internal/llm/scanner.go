package llm

import (
	"bufio"
	"io"
)

// NewScanner creates a line-oriented bufio.Scanner with headroom for long SSE
// data lines: 64 KiB initial buffer, 1 MiB max line length.
func NewScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return sc
}
