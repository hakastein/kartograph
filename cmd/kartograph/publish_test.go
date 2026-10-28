package main_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// crashed is what a build killed at a point leaves behind, seen by the next status and index.
type crashed struct {
	Exit   int
	Stdout string
	// Pointer names the generation status reads after the crash: "old", or "new" for the one the next index
	// publishes.
	Pointer string
	// Generations counts the generations after the crash.
	Generations int
	// OldChanged lists the files of the old generation that changed.
	OldChanged []string
	// Unclean lists write-ahead logs and shadow files in generations/.
	Unclean []string
	// Modes are the modes of the graph file and the manifest of the generation status reads.
	Modes []string
	// Recovered names the generation the pointer names after the next index.
	Recovered string
	// Records counts the workspace records left after the next index.
	Records int
}

func (e *env) crashAt(point string) crashed {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	old := e.kg(ws, "index").Snapshot.Generation
	before := e.generationState(old)
	write("a.txt", "two\n")(e, ws)
	r := e.run(ws, e.vars("KARTOGRAPH_TEST_CRASH="+point), "index")
	afterCrash := e.kg(ws, "status").Snapshot.Generation
	generations := len(e.entries("generations"))
	modes := e.modes(afterCrash)
	recovered := e.kg(ws, "index").Snapshot.Generation
	names := map[string]string{old: "old", recovered: "new"}
	records, err := filepath.Glob(filepath.Join(e.store(), "workspaces", "*", "*.json"))
	require.NoError(e.t, err)
	return crashed{
		Exit:        r.code,
		Stdout:      r.stdout,
		Pointer:     names[afterCrash],
		Generations: generations,
		OldChanged:  changed(before, e.generationState(old)),
		Unclean:     e.unclean(),
		Modes:       modes,
		Recovered:   names[e.kg(ws, "status").Snapshot.Generation],
		Records:     len(records),
	}
}

func (e *env) modes(generation string) []string {
	dir := filepath.Join(e.store(), "generations", generation)
	return []string{stateOf(e.t, filepath.Join(dir, "graph.lbug")).mode.String(),
		stateOf(e.t, filepath.Join(dir, "manifest.json")).mode.String()}
}

func (e *env) unclean() []string {
	found := []string{}
	for _, pattern := range []string{"*.wal", "*.shadow"} {
		matches, err := filepath.Glob(filepath.Join(e.store(), "generations", "*", pattern))
		require.NoError(e.t, err)
		found = append(found, matches...)
	}
	return found
}

func TestInterruptedBuildLeavesAWholeSnapshotPublished(t *testing.T) {
	cases := []struct {
		point       string
		pointer     string
		generations int
	}{
		{point: "graph-open", pointer: "old", generations: 1},
		{point: "publish-locked", pointer: "old", generations: 1},
		{point: "moved", pointer: "old", generations: 2},
		{point: "pointer-swapped", pointer: "new", generations: 2},
	}
	for _, c := range cases {
		t.Run(c.point, func(t *testing.T) {
			t.Parallel()
			want := crashed{Exit: -1, Pointer: c.pointer, Generations: c.generations, OldChanged: []string{},
				Unclean: []string{}, Modes: []string{"-r--r--r--", "-r--r--r--"}, Recovered: "new", Records: 1}
			require.Equal(t, want, newEnv(t).crashAt(c.point))
		})
	}
}

// buildsOfOneWorkspace runs two index of one workspace, the second started while the first holds the workspace lock,
// and returns the hook log of both.
func (e *env) buildsOfOneWorkspace() []string {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	pause := action{Label: "A", Point: "workspace-locked", Pause: true}
	a := e.start(ws, e.vars(e.hookVars("A", pause)...), "index")
	e.waitPaused("A", "workspace-locked")
	b := e.start(ws, e.vars(e.hookVars("B", pause)...), "index")
	time.Sleep(500 * time.Millisecond)
	e.release("A", "workspace-locked")
	a.wait(e.t)
	b.wait(e.t)
	return e.logged("workspace-locked", "publish-locked", "done")
}

func TestBuildsOfOneWorkspaceTakeTurns(t *testing.T) {
	want := []string{"A workspace-locked", "A publish-locked", "A done", "B workspace-locked", "B done"}
	require.Equal(t, want, newEnv(t).buildsOfOneWorkspace())
}

// overlap pauses the process paused at point inside the store lock, starts the other one, gives it a second to run
// into the lock and releases the first; it returns the hook log of both at points and whether status shows the new
// generation afterwards.
func (e *env) overlap(paused, other []string, point string, points []string) ([]string, bool) {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	old := e.kg(ws, "index").Snapshot.Generation
	write("a.txt", "two\n")(e, ws)
	pause := action{Label: "P", Point: point, Pause: true}
	p := e.start(ws, e.vars(e.hookVars("P", pause)...), paused...)
	e.waitPaused("P", point)
	o := e.start(ws, e.vars(e.hookVars("O", pause)...), other...)
	time.Sleep(time.Second)
	e.release("P", point)
	p.wait(e.t)
	o.wait(e.t)
	return e.logged(points...), e.kg(ws, "status").Snapshot.Generation != old
}

func TestStoreLockSerializesPublicationAndReaders(t *testing.T) {
	cases := []struct {
		name          string
		paused, other []string
		point         string
		points        []string
		log           []string
	}{
		{name: "a reader waits for a publication", paused: []string{"index"}, other: []string{"status"},
			point: "moved", points: []string{"moved", "released", "opening"},
			log: []string{"P moved", "T released", "O opening"}},
		{name: "a publication waits for a reader taking its lease", paused: []string{"status"},
			other: []string{"index"}, point: "leasing", points: []string{"leasing", "released", "publish-locked"},
			log: []string{"P leasing", "T released", "O publish-locked"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			log, published := newEnv(t).overlap(c.paused, c.other, c.point, c.points)
			require.Equal(t, c.log, log)
			require.True(t, published)
		})
	}
}

// failed is what a build that fails before publication leaves in the store, and whether status still shows the
// previous generation.
type failed struct {
	Staging  []string
	Trash    []string
	Previous bool
}

func (e *env) failAt(point string) failed {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	old := e.kg(ws, "index").Snapshot.Generation
	write("a.txt", "two\n")(e, ws)
	r := e.run(ws, e.vars(e.hookVars("A", action{Point: point, Fail: true})...), "index")
	require.NotEqual(e.t, 0, r.code)
	return failed{Staging: e.entries("staging"), Trash: e.entries("trash"),
		Previous: e.kg(ws, "status").Snapshot.Generation == old}
}

func TestFailedBuildMovesItsStagingAway(t *testing.T) {
	cases := []struct{ point string }{{point: "graph-open"}}
	for _, c := range cases {
		t.Run(c.point, func(t *testing.T) {
			t.Parallel()
			want := failed{Staging: []string{}, Trash: []string{}, Previous: true}
			require.Equal(t, want, newEnv(t).failAt(c.point))
		})
	}
}

// tampered is what index answers when the published generation of its identity was changed behind its back, and the
// files of that generation afterwards.
type tampered struct {
	Exit  int
	Code  string
	Files []string
}

func (e *env) tamper(change func(e *env, manifest string)) tampered {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	generation := e.kg(ws, "index").Snapshot.Generation
	dir := filepath.Join(e.store(), "generations", generation)
	change(e, filepath.Join(dir, "manifest.json"))
	r := e.run(ws, e.vars(), "index")
	t := tampered{Exit: r.code, Files: e.list(dir)}
	if r.code != 0 {
		t.Code = parseFailure(e.t, r.stderr).Code
	}
	return t
}

func removeManifest(e *env, manifest string) {
	require.NoError(e.t, os.Remove(manifest))
}

func replaceManifest(e *env, manifest string) {
	require.NoError(e.t, os.Chmod(manifest, 0o644))
	require.NoError(e.t, os.WriteFile(manifest, []byte("{}\n"), 0o444))
}

func TestGenerationIsReusedOnlyWithItsOwnManifest(t *testing.T) {
	cases := []struct {
		name   string
		change func(e *env, manifest string)
		want   tampered
	}{
		{name: "a generation without its manifest is built again", change: removeManifest,
			want: tampered{Files: []string{"graph.lbug", "manifest.json"}}},
		{name: "another manifest under the hash is an error", change: replaceManifest,
			want: tampered{Exit: 1, Code: "internal", Files: []string{"graph.lbug", "manifest.json"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, newEnv(t).tamper(c.change))
		})
	}
}

// reuse is what the index of a second workspace with the same identity leaves.
type reuse struct {
	SameGeneration bool
	// Changed lists the files of the generation that the second index changed.
	Changed     []string
	Generations int
	Staging     int
	Trash       int
	// Heads tells, for each workspace, whether status shows its own HEAD.
	Heads []bool
	// Dirty is the dirty flag status shows for each workspace.
	Dirty []bool
}

func (e *env) indexTwo(second func(e *env, ws string) string) reuse {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit(), write("u.txt", "untracked\n"))
	first := e.kg(ws, "index").Snapshot.Generation
	before := e.generationState(first)
	other := second(e, ws)
	again := e.kg(other, "index").Snapshot.Generation
	statuses := []snapshot{e.kg(ws, "status").Snapshot, e.kg(other, "status").Snapshot}
	return reuse{
		SameGeneration: again == first,
		Changed:        changed(before, e.generationState(first)),
		Generations:    len(e.entries("generations")),
		Staging:        len(e.entries("staging")),
		Trash:          len(e.entries("trash")),
		Heads:          []bool{*statuses[0].Head == e.head(ws), *statuses[1].Head == e.head(other)},
		Dirty:          []bool{statuses[0].Dirty, statuses[1].Dirty},
	}
}

func sameWorkspace(_ *env, ws string) string {
	return ws
}

// commitChanges commits the untracked file of the workspace: the content stays, HEAD moves and the tree is clean.
func commitChanges(e *env, ws string) string {
	commit()(e, ws)
	return ws
}

// clone clones the workspace next to it with its untracked file, with commits more empty commits on top.
func clone(commits int) func(e *env, ws string) string {
	return func(e *env, ws string) string {
		other := filepath.Join(e.dir, "clone")
		e.git(e.dir, "clone", "-q", ws, other)
		write("u.txt", "untracked\n")(e, other)
		for range commits {
			e.git(other, "commit", "-q", "--allow-empty", "-m", "empty")
		}
		real, err := filepath.EvalSymlinks(other)
		require.NoError(e.t, err)
		return real
	}
}

func TestSnapshotWithTheSameIdentityIsReused(t *testing.T) {
	cases := []struct {
		name   string
		second func(e *env, ws string) string
		dirty  []bool
	}{
		{name: "the same workspace again", second: sameWorkspace, dirty: []bool{true, true}},
		{name: "the same workspace after a commit", second: commitChanges, dirty: []bool{false, false}},
		{name: "a clone on the same commit", second: clone(0), dirty: []bool{true, true}},
		{name: "a clone on another commit", second: clone(1), dirty: []bool{true, true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := reuse{SameGeneration: true, Changed: []string{}, Generations: 1, Heads: []bool{true, true},
				Dirty: c.dirty}
			require.Equal(t, want, newEnv(t).indexTwo(c.second))
		})
	}
}

// leases is what the hook saw of the leases of the store while a reader ran, and what is left in the generation
// afterwards.
type leases struct {
	Exit int
	Held []string
	Left []string
}

func (e *env) leasesOfReader(args ...string) leases {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	generation := e.kg(ws, "index").Snapshot.Generation
	look := []action{{Point: "opening", Leases: true}, {Point: "closing", Leases: true}}
	r := e.run(ws, e.vars(e.hookVars("R", look...)...), args...)
	return leases{
		Exit: r.code,
		Held: readLines(filepath.Join(e.hooks, "leases")),
		Left: e.list(filepath.Join(e.store(), "generations", generation)),
	}
}

func TestReaderHoldsALeaseWhileTheSnapshotIsOpen(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "status", args: []string{"status"}},
		{name: "query", args: []string{"query", filesQuery}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := leases{Held: []string{"R opening 1", "R closing 1"}, Left: []string{"graph.lbug", "manifest.json"}}
			require.Equal(t, want, newEnv(t).leasesOfReader(c.args...))
		})
	}
}

// storeRoot is where index puts the store with XDG_CACHE_HOME set to xdg, or unset when xdg is nil: the root relative
// to the test's directory.
func (e *env) storeRoot(xdg *string) []string {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	vars := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.home, "GIT_CONFIG_NOSYSTEM=1"}
	if xdg != nil {
		vars = append(vars, "XDG_CACHE_HOME="+*xdg)
	}
	r := e.run(ws, vars, "index")
	require.Equal(e.t, 0, r.code, r.stderr)
	generation := parseAnswer(e.t, r.stdout).Snapshot.Generation
	roots := []string{}
	for _, root := range []string{"home/.cache/kartograph", "cache/kartograph", "ws/relative/kartograph"} {
		if _, err := os.Stat(filepath.Join(e.dir, root, "generations", generation)); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

func TestStoreRootFollowsTheUserCache(t *testing.T) {
	relative, empty := "relative", ""
	cases := []struct {
		name string
		xdg  func(e *env) *string
		want []string
	}{
		{name: "XDG_CACHE_HOME", xdg: func(e *env) *string { return &e.cache }, want: []string{"cache/kartograph"}},
		{name: "XDG_CACHE_HOME unset", xdg: func(*env) *string { return nil }, want: []string{"home/.cache/kartograph"}},
		{name: "XDG_CACHE_HOME empty", xdg: func(*env) *string { return &empty }, want: []string{"home/.cache/kartograph"}},
		{name: "XDG_CACHE_HOME relative", xdg: func(*env) *string { return &relative },
			want: []string{"home/.cache/kartograph"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			require.Equal(t, c.want, e.storeRoot(c.xdg(e)))
		})
	}
}
