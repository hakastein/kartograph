package main_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// editedCapture is what index leaves when a.txt gets a line after each of the first times captures.
type editedCapture struct {
	Exit     int
	Answered bool
	Code     string
	Attempts int
	// Status is the error code of status afterwards, "" when it answered.
	Status string
	Rows   [][]any
}

func (e *env) indexWhileEditing(times int) editedCapture {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	edit := action{Point: "captured", Append: filepath.Join(ws, "a.txt"), Times: times}
	r := e.run(ws, e.vars(e.hookVars("I", edit)...), "index")
	got := editedCapture{Exit: r.code, Answered: r.stdout != ""}
	if r.code != 0 {
		f := parseFailure(e.t, r.stderr)
		got.Code, got.Attempts = f.Code, f.Attempts
	}
	status := e.run(ws, e.vars(), "status")
	if status.code != 0 {
		got.Status = parseFailure(e.t, status.stderr).Code
		return got
	}
	got.Rows = e.kg(ws, "query", filesQuery).Rows
	return got
}

func TestCaptureRetriesAndGivesUpOnAChangingTree(t *testing.T) {
	cases := []struct {
		name  string
		times int
		want  editedCapture
	}{
		{
			name:  "changed during every attempt",
			times: 3,
			want:  editedCapture{Exit: 1, Code: "capture_unstable", Attempts: 3, Status: "not_found"},
		},
		{
			name:  "changed during the first attempt",
			times: 1,
			want:  editedCapture{Answered: true, Rows: rowsOf([]file{{"a.txt", "file", "one\nedit\n"}})},
		},
		{
			name:  "changed during the first two attempts",
			times: 2,
			want:  editedCapture{Answered: true, Rows: rowsOf([]file{{"a.txt", "file", "one\nedit\nedit\n"}})},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, newEnv(t).indexWhileEditing(c.times))
		})
	}
}

// indexWhileSwapping indexes a workspace whose directory d is swapped, between listing and reading, for a link to a
// directory outside with a file of the same name, and put back before the listing is checked again; it returns the
// files of the published snapshot.
func (e *env) indexWhileSwapping() [][]any {
	ws := e.workspace("ws", write("d/a.txt", "mine\n"), commit())
	foreign := filepath.Join(e.dir, "foreign")
	write("a.txt", "foreign\n")(e, foreign)
	d, aside := filepath.Join(ws, "d"), filepath.Join(e.dir, "aside")
	swap := []action{{Point: "listed", Swap: []string{d, aside, foreign}}, {Point: "captured", Unswap: []string{d, aside}}}
	r := e.run(ws, e.vars(e.hookVars("I", swap...)...), "index")
	require.Equal(e.t, 0, r.code, r.stderr)
	return e.kg(ws, "query", filesQuery).Rows
}

func TestCaptureReadsOnlyTheFileAtItsPathInTheWorktree(t *testing.T) {
	cases := []struct{ name string }{{name: "a parent swapped for a link outside while it is read"}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, rowsOf([]file{{"d/a.txt", "file", "mine\n"}}), newEnv(t).indexWhileSwapping())
		})
	}
}
