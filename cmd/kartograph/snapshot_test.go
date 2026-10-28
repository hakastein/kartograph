package main_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const filesQuery = "MATCH (f:File) RETURN f.path, f.kind, f.sha256 ORDER BY f.path"

// published is what index, status and query print for one workspace.
type published struct {
	Blocks []snapshot
	Files  []int64
	Rows   [][]any
}

func (e *env) publish(ws string) published {
	indexed := e.kg(ws, "index")
	status := e.kg(ws, "status")
	query := e.kg(ws, "query", filesQuery)
	return published{
		Blocks: []snapshot{indexed.Snapshot, status.Snapshot, query.Snapshot},
		Files:  []int64{*indexed.Files, *status.Files},
		Rows:   query.Rows,
	}
}

// expected is the published view of a workspace whose snapshot holds files.
func (e *env) expected(ws string, generation string, head *string, dirty bool, files []file) published {
	block := snapshot{Workspace: ws, Generation: generation, Head: head, Dirty: dirty}
	return published{
		Blocks: []snapshot{block, block, block},
		Files:  []int64{int64(len(files)), int64(len(files))},
		Rows:   rowsOf(files),
	}
}

func (e *env) headOf(ws string) *string {
	head := e.head(ws)
	return &head
}

func removeAll(path string) step {
	return func(e *env, ws string) {
		require.NoError(e.t, os.RemoveAll(filepath.Join(ws, path)))
	}
}

// symlinkToOther links path to the tree next to the workspace, by an absolute target.
func symlinkToOther(path string) step {
	return func(e *env, ws string) {
		require.NoError(e.t, os.Symlink(filepath.Join(e.dir, "other"), filepath.Join(ws, path)))
	}
}

func TestIndexPublishesTheSnapshotOfTheWorktree(t *testing.T) {
	cases := []struct {
		name  string
		steps []step
		files []file
		head  bool
		dirty bool
	}{
		{
			name:  "committed files",
			steps: []step{write("a.txt", "one\n"), write("dir/b.txt", "two\n"), commit()},
			files: []file{{"a.txt", "file", "one\n"}, {"dir/b.txt", "file", "two\n"}},
			head:  true,
		},
		{
			name: "untracked files outside ignore",
			steps: []step{write(".gitignore", "*.log\nbuild/\n"), write("a.txt", "one\n"), commit(),
				write("u.txt", "u\n"), write("x.log", "log\n"), write("build/out.txt", "out\n")},
			files: []file{{".gitignore", "file", "*.log\nbuild/\n"}, {"a.txt", "file", "one\n"}, {"u.txt", "file", "u\n"}},
			head:  true,
			dirty: true,
		},
		{
			name:  "uncommitted edit",
			steps: []step{write("a.txt", "one\n"), commit(), write("a.txt", "two\n")},
			files: []file{{"a.txt", "file", "two\n"}},
			head:  true,
			dirty: true,
		},
		{
			name:  "uncommitted deletion",
			steps: []step{write("a.txt", "one\n"), write("b.txt", "two\n"), commit(), remove("b.txt")},
			files: []file{{"a.txt", "file", "one\n"}},
			head:  true,
			dirty: true,
		},
		{
			name:  "paths with spaces",
			steps: []step{write("dir with space/file name.txt", "x\n"), write("top level.txt", "y\n"), commit()},
			files: []file{{"dir with space/file name.txt", "file", "x\n"}, {"top level.txt", "file", "y\n"}},
			head:  true,
		},
		{
			name:  "empty workspace",
			files: []file{},
		},
		{
			name:  "untracked file before the first commit",
			steps: []step{write("u.txt", "u\n")},
			files: []file{{"u.txt", "file", "u\n"}},
			dirty: true,
		},
		{
			name:  "executable file",
			steps: []step{write("run.sh", "#!/bin/sh\n"), executable("run.sh"), commit()},
			files: []file{{"run.sh", "executable", "#!/bin/sh\n"}},
			head:  true,
		},
		{
			name: "symlinks into the worktree and into another tree",
			steps: []step{write("a.txt", "one\n"), write("dir/b.txt", "two\n"), symlink("lnk", "a.txt"),
				symlink("dirlink", "dir"), symlink("ext", "../other"), symlinkToOther("abs"), commit()},
			files: []file{{"a.txt", "file", "one\n"}, {"dir/b.txt", "file", "two\n"}, {"dirlink", "symlink", "dir"},
				{"lnk", "symlink", "a.txt"}},
			head: true,
		},
		{
			name: "tracked directory replaced by a symlink into another tree",
			steps: []step{write("a.txt", "one\n"), write("d/secret.txt", "mine\n"), commit(), removeAll("d"),
				symlink("d", "../other")},
			files: []file{{"a.txt", "file", "one\n"}},
			head:  true,
			dirty: true,
		},
		{
			name:  "nested worktree and nested repository",
			steps: []step{write("a.txt", "one\n"), commit(), worktree("inner"), nestedRepository("nest")},
			files: []file{{"a.txt", "file", "one\n"}},
			head:  true,
			dirty: true,
		},
		{
			name:  "submodule",
			steps: []step{write("a.txt", "one\n"), commit(), submodule("sub"), commit()},
			files: []file{{".gitmodules", "file", "[submodule \"sub\"]\n\tpath = sub\n\turl = ../origin-sub\n"},
				{"a.txt", "file", "one\n"}, {"sub/sub.txt", "file", "sub\n"}},
			head: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			ws := e.workspace("ws", c.steps...)
			got := e.publish(ws)
			require.Equal(t, e.expected(ws, got.Blocks[0].Generation, e.headIf(ws, c.head), c.dirty, c.files), got)
		})
	}
}

func (e *env) headIf(ws string, exists bool) *string {
	if !exists {
		return nil
	}
	return e.headOf(ws)
}

// touch moves the modification time of a file forward without changing it, so that git sees its index entry as
// stale.
func touch(path string) step {
	return func(e *env, ws string) {
		later := time.Now().Add(time.Hour)
		require.NoError(e.t, os.Chtimes(filepath.Join(ws, path), later, later))
	}
}

func TestCommandsDoNotWriteToTheSourceTree(t *testing.T) {
	fixture := []step{write(".gitignore", "*.log\n"), write("a.txt", "one\n"), write("b.txt", "two\n"),
		symlink("lnk", "a.txt"), commit(), touch("b.txt"), write("a.txt", "edited\n"), write("u.txt", "u\n"),
		write("x.log", "log\n"), symlink("ext", "../other"), worktree("inner"), nestedRepository("nest")}
	cases := []struct {
		name     string
		commands [][]string
	}{
		{name: "index", commands: [][]string{{"index"}}},
		{name: "index twice", commands: [][]string{{"index"}, {"index"}}},
		{name: "status", commands: [][]string{{"index"}, {"status"}}},
		{name: "query", commands: [][]string{{"index"}, {"query", filesQuery}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			ws := e.workspace("ws", fixture...)
			before := tree(t, ws)
			e.runAll(ws, c.commands)
			require.Equal(t, before, tree(t, ws))
		})
	}
}

func (e *env) runAll(dir string, commands [][]string) {
	for _, args := range commands {
		e.kg(dir, args...)
	}
}
