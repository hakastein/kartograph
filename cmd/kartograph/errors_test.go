package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// refusal is what kartograph prints when it does not answer.
type refusal struct {
	Exit   int
	Stdout string
	First  string
	Code   string
}

// refuse runs kartograph in a place of the fixture: "outside" a plain directory, "git" the .git directory of a
// workspace, "fresh" a workspace never indexed, "indexed" a workspace with a published snapshot.
func (e *env) refuse(place string, args ...string) refusal {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	outside := filepath.Join(e.dir, "plain")
	require.NoError(e.t, os.MkdirAll(outside, 0o755))
	dirs := map[string]string{"outside": outside, "git": filepath.Join(ws, ".git"), "fresh": ws, "indexed": ws}
	if place == "indexed" {
		e.kg(ws, "index")
	}
	r := e.run(dirs[place], e.vars(), args...)
	f := parseFailure(e.t, r.stderr)
	return refusal{Exit: r.code, Stdout: r.stdout, First: f.First, Code: f.Code}
}

func TestErrorIsADocumentOnStderr(t *testing.T) {
	cases := []struct {
		name  string
		place string
		args  []string
		exit  int
		code  string
	}{
		{name: "no command", place: "indexed", exit: 2, code: "bad_usage"},
		{name: "unknown command", place: "indexed", args: []string{"draw"}, exit: 2, code: "bad_usage"},
		{name: "unknown flag", place: "indexed", args: []string{"status", "--fast"}, exit: 2, code: "bad_usage"},
		{name: "query without a statement", place: "indexed", args: []string{"query"}, exit: 2, code: "bad_usage"},
		{name: "query with two statements", place: "indexed", args: []string{"query", "RETURN 1", "RETURN 2"}, exit: 2,
			code: "bad_usage"},
		{name: "index outside a worktree", place: "outside", args: []string{"index"}, exit: 1, code: "not_a_workspace"},
		{name: "status outside a worktree", place: "outside", args: []string{"status"}, exit: 1,
			code: "not_a_workspace"},
		{name: "index in the git directory", place: "git", args: []string{"index"}, exit: 1, code: "not_a_workspace"},
		{name: "status before index", place: "fresh", args: []string{"status"}, exit: 1, code: "not_found"},
		{name: "query before index", place: "fresh", args: []string{"query", "RETURN 1"}, exit: 1, code: "not_found"},
		{name: "query that does not bind", place: "indexed", args: []string{"query", "MATCH (x:Missing) RETURN x"},
			exit: 1, code: "query_rejected"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := refusal{Exit: c.exit, First: "code", Code: c.code}
			require.Equal(t, want, newEnv(t).refuse(c.place, c.args...))
		})
	}
}
