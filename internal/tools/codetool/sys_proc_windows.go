//go:build windows

package codetool

import (
	"os/exec"
)

// setSysProcAttr is a no-op on Windows (no process group support).
func setSysProcAttr(cmd *exec.Cmd) {}