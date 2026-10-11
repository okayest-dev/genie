package style

import "io"

// RoleWriter is the role-styled writer seam (og-2s0): every Write renders its
// bytes through one resolved role style, then forwards the styled output to the
// underlying writer. Styling is entirely a function of the render profile —
// under a plain profile (non-TTY, NO_COLOR, TERM=dumb, CI) the bytes drop
// through verbatim, byte-identical to an unwrapped writer. A zero style (a
// theme that leaves the role unmapped) renders plain too, so roll-out is
// purely additive.
type RoleWriter struct {
	w        io.Writer
	renderer *Renderer
	style    Style
}

// NewRoleWriter returns a writer that renders every byte it receives through
// style s on the renderer's profile.
func NewRoleWriter(w io.Writer, renderer *Renderer, s Style) *RoleWriter {
	return &RoleWriter{w: w, renderer: renderer, style: s}
}

// Write renders p through the role style and writes the result to the
// underlying writer. It reports len(p) on success, satisfying the io.Writer
// contract even though the styled encoding is typically longer than p.
func (rw *RoleWriter) Write(p []byte) (int, error) {
	styled := rw.renderer.Render(rw.style, string(p))
	if _, err := io.WriteString(rw.w, styled); err != nil {
		return len(p), err
	}
	return len(p), nil
}