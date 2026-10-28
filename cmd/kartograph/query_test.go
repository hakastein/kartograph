package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryAnswersFromTheSnapshot(t *testing.T) {
	cases := []struct {
		name      string
		statement string
		columns   []string
		rows      [][]any
	}{
		{
			name:      "scalars and lists",
			statement: "RETURN 1 AS one, 'a b' AS s, [1, 2] AS l, NULL AS n, true AS b, 1.5 AS f",
			columns:   []string{"one", "s", "l", "n", "b", "f"},
			rows:      [][]any{{1, "a b", []any{1, 2}, nil, true, 1.5}},
		},
		{
			name: "numeric types and a date",
			statement: "RETURN CAST(-1 AS INT8) AS i8, CAST(-2 AS INT16) AS i16, CAST(-3 AS INT32) AS i32, " +
				"CAST(4 AS UINT8) AS u8, CAST(5 AS UINT16) AS u16, CAST(6 AS UINT32) AS u32, CAST(7 AS UINT64) AS u64, " +
				"CAST(0.5 AS FLOAT) AS f32, date('2026-01-02') AS d",
			columns: []string{"i8", "i16", "i32", "u8", "u16", "u32", "u64", "f32", "d"},
			rows:    [][]any{{-1, -2, -3, 4, 5, 6, 7, 0.5, "2026-01-02"}},
		},
		{
			name:      "a node",
			statement: "MATCH (f:File) RETURN f",
			columns:   []string{"f"},
			rows: [][]any{{map[string]any{"_ID": "0:0", "_LABEL": "File", "path": "a.txt", "kind": "file", "size": 4,
				"sha256": rowsOf([]file{{"a.txt", "file", "one\n"}})[0][2]}}},
		},
		{
			name:      "no rows",
			statement: "MATCH (f:File) WHERE f.path = 'missing' RETURN f.path",
			columns:   []string{"f.path"},
			rows:      [][]any{},
		},
		{
			name:      "the buffer pool of a reader",
			statement: "CALL bm_info() RETURN mem_limit",
			columns:   []string{"mem_limit"},
			rows:      [][]any{{256 << 20}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			ws := e.workspace("ws", write("a.txt", "one\n"), commit())
			e.kg(ws, "index")
			got := e.kg(ws, "query", c.statement)
			require.Equal(t, answer{Snapshot: e.kg(ws, "status").Snapshot, Columns: c.columns, Rows: c.rows}, got)
		})
	}
}

// rejection is what a refused query leaves: the exit code, stdout, the error code, the files of the published
// generation that changed, appeared or disappeared, and what appeared in the directory the statement names.
type rejection struct {
	Exit    int
	Stdout  string
	Code    string
	Changed []string
	Outside []string
}

func (e *env) reject(statement string) rejection {
	ws := e.workspace("ws", write("a.txt", "one\n"), commit())
	generation := e.kg(ws, "index").Snapshot.Generation
	outside := filepath.Join(e.dir, "out")
	require.NoError(e.t, os.MkdirAll(outside, 0o755))
	before := e.generationState(generation)
	r := e.run(ws, e.vars(), "query", strings.ReplaceAll(statement, "$OUT", outside))
	return rejection{
		Exit:    r.code,
		Stdout:  r.stdout,
		Code:    parseFailure(e.t, r.stderr).Code,
		Changed: changed(before, e.generationState(generation)),
		Outside: e.list(outside),
	}
}

// changed names the files whose state differs between two listings of a directory.
func changed(before, after map[string]fileState) []string {
	names := []string{}
	for name, state := range before {
		if got, ok := after[name]; !ok || got != state {
			names = append(names, name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// generationState is every file of a generation.
func (e *env) generationState(generation string) map[string]fileState {
	dir := filepath.Join(e.store(), "generations", generation)
	state := map[string]fileState{}
	for _, name := range e.list(dir) {
		state[name] = stateOf(e.t, filepath.Join(dir, name))
	}
	return state
}

func (e *env) list(dir string) []string {
	des, err := os.ReadDir(dir)
	require.NoError(e.t, err)
	names := []string{}
	for _, d := range des {
		names = append(names, d.Name())
	}
	return names
}

func TestQueryRejectsStatementsThatWrite(t *testing.T) {
	cases := []struct {
		name      string
		statement string
	}{
		{name: "create a node", statement: "CREATE (:File {path: 'x', kind: 'file', size: 1, sha256: 'x'})"},
		{name: "merge a node", statement: "MERGE (:File {path: 'x'})"},
		{name: "set a property", statement: "MATCH (f:File) SET f.size = 0"},
		{name: "delete a node", statement: "MATCH (f:File) DELETE f"},
		{name: "create a table", statement: "CREATE NODE TABLE T(id INT64 PRIMARY KEY)"},
		{name: "drop a table", statement: "DROP TABLE File"},
		{name: "alter a table", statement: "ALTER TABLE File ADD extra INT64"},
		{name: "create a macro", statement: "CREATE MACRO m(x) AS x + 1"},
		{name: "copy into a table", statement: "COPY File FROM '$OUT/in.csv'"},
		{name: "copy to a file", statement: "COPY (MATCH (f:File) RETURN f.path) TO '$OUT/out.csv'"},
		{name: "export the database", statement: "EXPORT DATABASE '$OUT/export'"},
		{name: "checkpoint", statement: "CHECKPOINT"},
		{name: "begin a transaction", statement: "BEGIN TRANSACTION"},
		{name: "set an option", statement: "CALL threads=1"},
		{name: "attach a database", statement: "ATTACH '$OUT/other.lbug' AS other (dbtype lbug)"},
		{name: "two statements", statement: "RETURN 1; RETURN 2"},
		{name: "a syntax error", statement: "MATCH (f:File RETURN f"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			want := rejection{Exit: 1, Code: "query_rejected", Changed: []string{}, Outside: []string{}}
			require.Equal(t, want, newEnv(t).reject(c.statement))
		})
	}
}
