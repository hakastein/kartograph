package main_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// kartograph is the binary under test, built with the kartograph_test tag so that KARTOGRAPH_TEST_HOOK and
// KARTOGRAPH_TEST_CRASH act.
var kartograph string

func TestMain(m *testing.M) {
	if os.Getenv("KG_HOOK") != "" {
		os.Exit(hook(os.Args[1]))
	}
	dir, err := os.MkdirTemp("", "kartograph-bin-")
	if err != nil {
		panic(err)
	}
	kartograph = filepath.Join(dir, "kartograph")
	args := []string{"build", "-tags", "kartograph_test", "-o", kartograph}
	if raceEnabled {
		args = append(args, "-race")
	}
	build := exec.Command("go", append(args, "github.com/hakastein/kartograph/cmd/kartograph")...)
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is one test's machine: a home, a user cache and the directory the workspaces live in.
type env struct {
	t     *testing.T
	dir   string
	home  string
	cache string
	hooks string
}

func newEnv(t *testing.T) *env {
	dir := t.TempDir()
	e := &env{t: t, dir: dir, home: filepath.Join(dir, "home"), cache: filepath.Join(dir, "cache"),
		hooks: filepath.Join(dir, "hooks")}
	for _, d := range []string{e.home, e.cache, e.hooks, filepath.Join(dir, "other")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other", "secret.txt"), []byte("secret\n"), 0o644))
	return e
}

// vars is the environment of kartograph and git: an empty git configuration and the test's home and cache.
func (e *env) vars(extra ...string) []string {
	return append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"XDG_CACHE_HOME=" + e.cache,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	}, extra...)
}

// store is the store root of the test's cache.
func (e *env) store() string {
	return filepath.Join(e.cache, "kartograph")
}

type result struct {
	code   int
	stdout string
	stderr string
}

func (e *env) run(dir string, vars []string, args ...string) result {
	cmd := exec.Command(kartograph, args...)
	cmd.Dir = dir
	cmd.Env = vars
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		require.NoError(e.t, err)
	}
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// kg runs kartograph in dir with the test's environment and requires it to answer.
func (e *env) kg(dir string, args ...string) answer {
	r := e.run(dir, e.vars(), args...)
	require.Equal(e.t, 0, r.code, r.stderr)
	require.Empty(e.t, r.stderr)
	return parseAnswer(e.t, r.stdout)
}

func (e *env) git(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always"}, args...)...)
	cmd.Dir = dir
	cmd.Env = e.vars()
	out, err := cmd.CombinedOutput()
	require.NoError(e.t, err, string(out))
	return strings.TrimSpace(string(out))
}

// answer is the document a command prints on stdout.
type answer struct {
	Snapshot snapshot `yaml:"snapshot"`
	Files    *int64   `yaml:"files"`
	Columns  []string `yaml:"columns"`
	Rows     [][]any  `yaml:"rows"`
}

type snapshot struct {
	Workspace  string  `yaml:"workspace"`
	Generation string  `yaml:"generation"`
	Head       *string `yaml:"head"`
	Dirty      bool    `yaml:"dirty"`
}

func parseAnswer(t *testing.T, stdout string) answer {
	var a answer
	require.NoError(t, yaml.Unmarshal([]byte(stdout), &a), stdout)
	return a
}

// failure is the error document on stderr, with the name of its first key.
type failure struct {
	First    string
	Code     string `yaml:"code"`
	Attempts int    `yaml:"attempts"`
}

func parseFailure(t *testing.T, stderr string) failure {
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(stderr), &doc), stderr)
	require.NotEmpty(t, doc.Content, stderr)
	require.NotEmpty(t, doc.Content[0].Content, stderr)
	var f failure
	require.NoError(t, doc.Decode(&f))
	f.First = doc.Content[0].Content[0].Value
	return f
}

// step builds a part of a workspace fixture.
type step func(e *env, ws string)

func write(path, content string) step {
	return func(e *env, ws string) {
		full := filepath.Join(ws, path)
		require.NoError(e.t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(e.t, os.WriteFile(full, []byte(content), 0o644))
	}
}

func commit() step {
	return func(e *env, ws string) {
		e.git(ws, "add", "-A")
		e.git(ws, "commit", "-q", "--allow-empty", "-m", "commit")
	}
}

func remove(path string) step {
	return func(e *env, ws string) {
		require.NoError(e.t, os.Remove(filepath.Join(ws, path)))
	}
}

func symlink(path, target string) step {
	return func(e *env, ws string) {
		require.NoError(e.t, os.Symlink(target, filepath.Join(ws, path)))
	}
}

func executable(path string) step {
	return func(e *env, ws string) {
		require.NoError(e.t, os.Chmod(filepath.Join(ws, path), 0o755))
	}
}

// worktree adds a linked worktree of the workspace's own repository at path inside it.
func worktree(path string) step {
	return func(e *env, ws string) {
		e.git(ws, "worktree", "add", "-q", path)
	}
}

// nestedRepository makes an independent repository with a file at path inside the workspace.
func nestedRepository(path string) step {
	return func(e *env, ws string) {
		write(filepath.Join(path, "nested.txt"), "nested\n")(e, ws)
		e.git(filepath.Join(ws, path), "init", "-q")
	}
}

// submodule adds a repository with sub.txt next to the workspace as a submodule at path, by a relative URL.
func submodule(path string) step {
	return func(e *env, ws string) {
		origin := filepath.Join(e.dir, "origin-"+path)
		require.NoError(e.t, os.MkdirAll(origin, 0o755))
		write("sub.txt", "sub\n")(e, origin)
		e.git(origin, "init", "-q")
		commit()(e, origin)
		e.git(ws, "submodule", "-q", "add", "../origin-"+path, path)
	}
}

// workspace makes the git repository dir/name and applies steps to it; it returns its path with symlinks resolved.
func (e *env) workspace(name string, steps ...step) string {
	ws := filepath.Join(e.dir, name)
	require.NoError(e.t, os.MkdirAll(ws, 0o755))
	e.git(ws, "init", "-q", "-b", "main")
	for _, s := range steps {
		s(e, ws)
	}
	real, err := filepath.EvalSymlinks(ws)
	require.NoError(e.t, err)
	return real
}

func (e *env) head(ws string) string {
	return e.git(ws, "rev-parse", "HEAD")
}

// file is an expected row of the graph: path, kind and the SHA-256 of the content.
type file struct {
	path, kind, content string
}

func rowsOf(files []file) [][]any {
	rows := [][]any{}
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.content))
		rows = append(rows, []any{f.path, f.kind, hex.EncodeToString(sum[:])})
	}
	return rows
}

// tree is every entry under dir, .git included, by lstat: its type and permissions, and its content or link target.
func tree(t *testing.T, dir string) map[string]string {
	entries := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry += " " + hex.EncodeToString(sum[:])
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry += " " + target
		}
		entries[path] = entry
		return nil
	}))
	return entries
}

// fileState is what a reader could notice of a published file: its identity, times, mode and content.
type fileState struct {
	ino   uint64
	mtime int64
	mode  fs.FileMode
	sum   string
}

func stateOf(t *testing.T, path string) fileState {
	info, err := os.Lstat(path)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return fileState{ino: info.Sys().(*syscall.Stat_t).Ino, mtime: info.ModTime().UnixNano(), mode: info.Mode(),
		sum: hex.EncodeToString(sum[:])}
}

func (e *env) graphFile(generation string) string {
	return filepath.Join(e.store(), "generations", generation, "graph.lbug")
}

// entries lists the names in a directory of the store.
func (e *env) entries(dir string) []string {
	des, err := os.ReadDir(filepath.Join(e.store(), dir))
	require.NoError(e.t, err)
	names := []string{}
	for _, d := range des {
		names = append(names, d.Name())
	}
	return names
}

// hookVars makes kartograph call this test binary as its hook, labelled, with actions.
func (e *env) hookVars(label string, actions ...action) []string {
	data, err := json.Marshal(actions)
	require.NoError(e.t, err)
	self, err := os.Executable()
	require.NoError(e.t, err)
	return []string{"KARTOGRAPH_TEST_HOOK=" + self, "KG_HOOK=1", "KG_HOOK_DIR=" + e.hooks, "KG_HOOK_LABEL=" + label,
		"KG_HOOK_ACTIONS=" + string(data)}
}

// action is what the hook does at a point of a labelled kartograph process.
type action struct {
	Label string `json:"label,omitempty"`
	Point string `json:"point"`
	// Pause waits at the point until the test releases it.
	Pause bool `json:"pause,omitempty"`
	// Append adds a line to this file the first Times times the point is reached.
	Append string `json:"append,omitempty"`
	Times  int    `json:"times,omitempty"`
	// Leases records how many lease files of the store have a holder.
	Leases bool `json:"leases,omitempty"`
}

// hook is the test binary run by kartograph at a point. It logs the point and performs the actions for it.
func hook(point string) int {
	dir, label := os.Getenv("KG_HOOK_DIR"), os.Getenv("KG_HOOK_LABEL")
	appendLine(filepath.Join(dir, "log"), label+" "+point)
	var actions []action
	if err := json.Unmarshal([]byte(os.Getenv("KG_HOOK_ACTIONS")), &actions); err != nil {
		panic(err)
	}
	for _, a := range actions {
		if a.Point != point || (a.Label != "" && a.Label != label) {
			continue
		}
		if a.Append != "" {
			counter := filepath.Join(dir, "appended")
			appendLine(counter, "")
			if n := len(readLines(counter)); n <= a.Times {
				appendLine(a.Append, "edit")
			}
		}
		if a.Leases {
			appendLine(filepath.Join(dir, "leases"), fmt.Sprintf("%s %s %d", label, point, heldLeases()))
		}
		if a.Pause {
			appendLine(filepath.Join(dir, label+"."+point+".reached"), "")
			for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
				if _, err := os.Stat(filepath.Join(dir, label+"."+point+".go")); err == nil {
					return 0
				}
				time.Sleep(10 * time.Millisecond)
			}
			return 1
		}
	}
	return 0
}

func heldLeases() int {
	leases, err := filepath.Glob(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "kartograph", "generations", "*", "lease-*"))
	if err != nil {
		panic(err)
	}
	held := 0
	for _, lease := range leases {
		f, err := os.OpenFile(lease, os.O_RDWR, 0)
		if err != nil {
			panic(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); errors.Is(err, syscall.EWOULDBLOCK) {
			held++
		} else if err != nil {
			panic(err)
		}
		f.Close()
	}
	return held
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		panic(err)
	}
}

func readLines(path string) []string {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}
	}
	if err != nil {
		panic(err)
	}
	defer f.Close()
	lines := []string{}
	for s := bufio.NewScanner(f); s.Scan(); {
		lines = append(lines, s.Text())
	}
	return lines
}

// logged is the hook log of the test, only the given points.
func (e *env) logged(points ...string) []string {
	lines := []string{}
	for _, line := range readLines(filepath.Join(e.hooks, "log")) {
		if _, point, _ := strings.Cut(line, " "); slices.Contains(points, point) {
			lines = append(lines, line)
		}
	}
	return lines
}

func (e *env) waitPaused(label, point string) {
	require.Eventually(e.t, func() bool {
		_, err := os.Stat(filepath.Join(e.hooks, label+"."+point+".reached"))
		return err == nil
	}, time.Minute, 10*time.Millisecond)
}

func (e *env) release(label, point string) {
	appendLine(filepath.Join(e.hooks, label+"."+point+".go"), "")
}

// started is a kartograph process that runs in the background.
type started struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func (e *env) start(dir string, vars []string, args ...string) *started {
	s := &started{cmd: exec.Command(kartograph, args...)}
	s.cmd.Dir, s.cmd.Env = dir, vars
	s.cmd.Stdout, s.cmd.Stderr = &s.stdout, &s.stderr
	require.NoError(e.t, s.cmd.Start())
	return s
}

func (s *started) wait(t *testing.T) {
	require.NoError(t, s.cmd.Wait(), s.stderr.String())
}
