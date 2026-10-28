package cli

import (
	"github.com/hakastein/kartograph/internal/graph"
	"github.com/hakastein/kartograph/internal/ladybug"
	"github.com/hakastein/kartograph/internal/store"
	"github.com/hakastein/kartograph/internal/testhook"
)

func (c *call) status() (mapping, error) {
	snapshot, result, err := c.read("MATCH (f:File) RETURN count(f)")
	if err != nil {
		return nil, err
	}
	return mapping{
		{key: "snapshot", value: snapshotBlock(snapshot)},
		{key: "files", value: result.Rows[0][0]},
	}, nil
}

func (c *call) query(statement string) (mapping, error) {
	snapshot, result, err := c.read(statement)
	if err != nil {
		return nil, rejected(err)
	}
	columns := make([]any, len(result.Columns))
	for i, name := range result.Columns {
		columns[i] = name
	}
	rows := make([]any, len(result.Rows))
	for i, row := range result.Rows {
		rows[i] = row
	}
	return mapping{
		{key: "snapshot", value: snapshotBlock(snapshot)},
		{key: "columns", value: columns},
		{key: "rows", value: rows},
	}, nil
}

// read takes a lease on the published snapshot of the workspace, then opens its graph file read-only and runs one
// query; the lease is held until the file is closed.
func (c *call) read(statement string) (store.Snapshot, *ladybug.Result, error) {
	ws, st, err := c.open()
	if err != nil {
		return store.Snapshot{}, nil, err
	}
	lease, err := st.Read(ws.ID)
	if err != nil {
		return store.Snapshot{}, nil, err
	}
	defer lease.Release()
	testhook.At("opening")
	db, err := ladybug.Open(lease.Graph, graph.ReadConfig())
	if err != nil {
		return store.Snapshot{}, nil, err
	}
	defer db.Close()
	result, err := db.Query(statement)
	if err != nil {
		return store.Snapshot{}, nil, err
	}
	testhook.At("closing")
	return lease.Snapshot, result, nil
}
