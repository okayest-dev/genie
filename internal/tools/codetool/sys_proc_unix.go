//go:build !windows

package codetool

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr sets the SysProcAttr for creating a new process group on Unix.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}