package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Checkpoints let a turn be undone, files included. Each project gets a
// "shadow" git repository under ~/.goharness/checkpoints whose work tree is
// the project: the project's own .git (if any) is never touched, it works
// in folders that aren't repositories, the project's .gitignore is
// respected, and changes made through bash are captured too - not just the
// file tools'.

// checkpointTimeout bounds each git step. A var so tests can shorten it.
var checkpointTimeout = 30 * time.Second

// errCheckpointTooSlow means the project is too big to snapshot each turn.
var errCheckpointTooSlow = errors.New("the folder is too large to snapshot each turn")

// Heavy, regenerable directories that are never snapshotted.
var checkpointExcludes = []string{
	"node_modules/", ".venv/", "venv/", "__pycache__/", ".mypy_cache/", ".pytest_cache/",
	".next/", ".cache/", ".gradle/", ".terraform/",
}

type checkpointer struct {
	root   string
	gitDir string
}

func checkpointsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goharness", "checkpoints"), nil
}

// newCheckpointer returns nil with a reason when checkpoints can't work
// for this project.
func newCheckpointer(root string) (*checkpointer, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("git is not installed")
	}
	home, _ := os.UserHomeDir()
	if root == "/" || root == home {
		return nil, fmt.Errorf("won't snapshot %s (too large); run goharness inside a project folder", root)
	}
	dir, err := checkpointsDir()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(root))
	return &checkpointer{root: root, gitDir: filepath.Join(dir, hex.EncodeToString(sum[:8]))}, nil
}

func (c *checkpointer) git(ctx context.Context, args ...string) (string, error) {
	base := []string{
		"--git-dir", c.gitDir, "--work-tree", c.root,
		"-c", "user.name=goharness", "-c", "user.email=goharness@localhost",
		"-c", "core.hooksPath=/dev/null", "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false",
		"-c", "core.quotepath=false",
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = c.root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%w (git %s took over %s)", errCheckpointTooSlow, args[0], checkpointTimeout)
		}
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (c *checkpointer) init(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(c.gitDir, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(c.gitDir, 0o700); err != nil {
		return err
	}
	if _, err := c.git(ctx, "init", "--quiet"); err != nil {
		return err
	}
	exclude := "# goharness checkpoint excludes\n" + strings.Join(checkpointExcludes, "\n") + "\n"
	return os.WriteFile(filepath.Join(c.gitDir, "info", "exclude"), []byte(exclude), 0o644)
}

// snapshot records the project's current files and returns the commit.
func (c *checkpointer) snapshot(label string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	if err := c.init(ctx); err != nil {
		return "", err
	}
	if _, err := c.git(ctx, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	if _, err := c.git(ctx, "commit", "--quiet", "--allow-empty", "--no-verify", "-m", label); err != nil {
		return "", err
	}
	return c.git(ctx, "rev-parse", "HEAD")
}

// restore puts the project's files back as they were at commit: changed
// and deleted files come back, and files created since are removed.
// Ignored files are left alone. The state being replaced is snapshotted
// first, so it can still be recovered from the shadow repository.
func (c *checkpointer) restore(commit string) error {
	if _, err := c.snapshot("before undo"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	_, err := c.git(ctx, "read-tree", "--reset", "-u", commit)
	return err
}

// changedSince lists files that differ between commit and now.
func (c *checkpointer) changedSince(commit string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	if _, err := c.git(ctx, "add", "--all", "--", "."); err != nil {
		return nil, err
	}
	out, err := c.git(ctx, "diff", "--cached", "--name-only", commit)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// FileChange is one file's change between two checkpoints.
type FileChange struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"` // when renamed
	Status    string     `json:"status"`            // added, modified, deleted, renamed
	Added     int        `json:"added"`
	Removed   int        `json:"removed"`
	Binary    bool       `json:"binary,omitempty"`
	Truncated bool       `json:"truncated,omitempty"` // Diff was cut short
	Diff      []DiffLine `json:"diff"`                 // Op "@" marks a hunk header
}

const (
	maxDiffLinesPerFile = 800
	maxDiffLinesTotal   = 6000
)

// countChanged returns how many files differ between two commits.
func (c *checkpointer) countChanged(from, to string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	out, err := c.git(ctx, "diff", "--name-only", "--no-renames", from, to)
	if err != nil || out == "" {
		return 0, err
	}
	return len(strings.Split(out, "\n")), nil
}

// diff returns the per-file changes between two commits, parsed from git's
// unified diff and capped so a huge change can't swamp the browser.
func (c *checkpointer) diff(from, to string) ([]FileChange, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	defer cancel()
	out, err := c.git(ctx, "diff", "--no-color", "--no-ext-diff", "-M", "-U3", from, to)
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

func parseUnifiedDiff(out string) []FileChange {
	files := []FileChange{}
	var cur *FileChange
	total := 0
	inHunk := false // after a file's first @@, lines are content even if they look like headers
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
		}
	}
	unquote := func(p string) string { return strings.TrimPrefix(strings.TrimPrefix(p, "a/"), "b/") }
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			parts := strings.SplitN(strings.TrimPrefix(line, "diff --git "), " b/", 2)
			path := unquote(parts[0])
			if len(parts) == 2 {
				path = parts[1]
			}
			cur = &FileChange{Path: path, Status: "modified", Diff: []DiffLine{}}
			inHunk = false
		case cur == nil:
			continue
		case !inHunk && strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case !inHunk && strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case !inHunk && strings.HasPrefix(line, "rename from "):
			cur.Status, cur.OldPath = "renamed", strings.TrimPrefix(line, "rename from ")
		case !inHunk && strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
		case !inHunk && strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case !inHunk && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "similarity index") || strings.HasPrefix(line, "old mode") || strings.HasPrefix(line, "new mode")):
			// headers the view doesn't need
		case strings.HasPrefix(line, `\ No newline`):
		case line == "":
		default:
			op := line[:1]
			switch op {
			case "+":
				cur.Added++
			case "-":
				cur.Removed++
			case "@":
				inHunk = true
			case " ":
			default:
				continue
			}
			if len(cur.Diff) >= maxDiffLinesPerFile || total >= maxDiffLinesTotal {
				cur.Truncated = true
				continue
			}
			text := line[1:]
			if op == "@" {
				text = line
			}
			cur.Diff = append(cur.Diff, DiffLine{Op: op, Text: text})
			total++
		}
	}
	flush()
	return files
}
