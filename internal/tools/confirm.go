package tools

// Confirmer decides whether a tool mutation should proceed. The tool
// presents a human-readable prompt (e.g. "overwrite foo.go?"); the
// Confirmer returns true to accept or false to decline. In headless
// mode (-p) the Confirmer always returns false.
type Confirmer interface {
	Confirm(prompt string) bool
}

// AutoDeny is a Confirmer that always declines. Use in non-interactive
// mode so that overwrites and bash never run without a human.
type AutoDeny struct{}

func (AutoDeny) Confirm(string) bool { return false }
