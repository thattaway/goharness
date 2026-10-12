package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Optional output compression with rtk (github.com/rtk-ai/rtk), a CLI proxy
// that shrinks the output of common commands (git, ls, grep, docker, ...)
// before the model reads it. When `rtk` is installed, bash commands are run
// through it by default, which matters on small context windows.
//
// Permissions are checked against the command exactly as the model wrote it;
// the rewrite happens only just before running. The model can get the
// unfiltered output by passing raw=true, and GOHARNESS_RTK=off disables the
// whole thing. Anything that goes wrong falls back to the original command.

const rtkNote = "\n(output compressed by rtk; call bash again with raw=true for the complete, unfiltered output)"

func rtkEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOHARNESS_RTK"))) {
	case "off", "0", "false", "no":
		return false
	}
	_, err := exec.LookPath("rtk")
	return err == nil
}

// rtkRewrite asks rtk how it would run command (it understands pipelines and
// && chains and only rewrites commands it has a filter for). rtk exits 3 with
// the rewritten command on stdout, despite its help text saying 0, and 1
// when it has no equivalent; both of those are accepted as "rewritten" only
// when a different, non-empty command comes back.
func rtkRewrite(ctx context.Context, command string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "rtk", "rewrite", command).Output()
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 3 {
			return "", false
		}
	}
	rewritten := strings.TrimSpace(string(out))
	if rewritten == "" || rewritten == strings.TrimSpace(command) {
		return "", false
	}
	return rewritten, true
}

// toolBashCompressed runs command through rtk when that is available and
// applicable, and otherwise exactly as written.
func toolBashCompressed(ctx context.Context, root, command string) string {
	if !rtkEnabled() {
		return toolBash(ctx, root, command)
	}
	rewritten, ok := rtkRewrite(ctx, command)
	if !ok {
		return toolBash(ctx, root, command)
	}
	out := toolBash(ctx, root, rewritten)
	if strings.HasPrefix(out, "Timed out after") || out == "Cancelled by the user." {
		return out
	}
	return out + rtkNote
}
