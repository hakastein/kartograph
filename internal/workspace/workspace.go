// Package workspace finds the git worktree kartograph works on and captures its snapshot: tracked files and untracked
// files outside ignore, as they are in the worktree, submodules included. Nothing is written to the worktree.
package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotWorkspace is a directory outside any git worktree.
var ErrNotWorkspace = errors.New("the directory is not inside a git worktree")

type Workspace struct {
	// Root is the absolute path of the worktree with symlinks resolved.
	Root string
	// ID names the workspace in the store: the first 32 hex digits of the SHA-256 of Root.
	ID  string
	env []string
}

// Find returns the worktree that holds dir. env is the environment git runs with.
func Find(dir string, env []string) (*Workspace, error) {
	out, err := git(env, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, ErrNotWorkspace
		}
		return nil, err
	}
	root, err := filepath.EvalSymlinks(strings.TrimSuffix(string(out), "\n"))
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("git named a relative worktree root %q", root)
	}
	sum := sha256.Sum256([]byte(root))
	return &Workspace{Root: root, ID: hex.EncodeToString(sum[:16]), env: env}, nil
}

// git runs git in dir without the optional locks, so that git status does not refresh the index of the worktree.
func git(env []string, dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append([]string{}, env...), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// head returns the commit of HEAD, or "" when the branch has no commit yet.
func (w *Workspace) head() (string, error) {
	out, err := git(w.env, w.Root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// dirty tells whether git status shows a change of a tracked file or an untracked file outside ignore.
func (w *Workspace) dirty() (bool, error) {
	out, err := git(w.env, w.Root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

// list returns the slash-separated paths of tracked files and untracked files outside ignore under dir, a worktree
// or a submodule checkout at prefix. Untracked nested repositories and worktrees, which git lists as a directory, are
// not included; submodules are listed by lstat (see paths).
func (w *Workspace) list(dir, prefix string) ([]string, error) {
	out, err := git(w.env, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var paths []string
	seen := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || strings.HasSuffix(p, "/") || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, prefix+p)
	}
	return paths, nil
}
