//go:build windows

package bashtool

import (
	"os/exec"
)

// setSysProcAttr sets the SysProcAttr on the command (no-op on Windows).
func setSysProcAttr(cmd *exec.Cmd) {}

// killProcessGroup kills only the process on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}