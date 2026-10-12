//go:build !unix

package main

import "os/exec"

// setProcessGroup is a no-op where process groups aren't available; the
// WaitDelay set by the caller still keeps a timeout from blocking forever.
func setProcessGroup(cmd *exec.Cmd) {}
