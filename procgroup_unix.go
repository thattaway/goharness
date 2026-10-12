//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group and makes
// cancellation kill that whole group. Killing only the shell leaves anything
// it started (a `journalctl -f`, a server launched with &) running, still
// holding the output pipe, which blocks the caller for as long as it lives.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
