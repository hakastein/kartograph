package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/hakastein/kartograph/internal/graph"
	"github.com/hakastein/kartograph/internal/ladybug"
	"github.com/hakastein/kartograph/internal/store"
	"github.com/hakastein/kartograph/internal/testhook"
	"github.com/hakastein/kartograph/internal/workspace"
)

// captureAttempts bounds the captures of a worktree that keeps changing.
const captureAttempts = 3

// manifestVersion is the version of the manifest format.
const manifestVersion = 1

// index captures the workspace under its workspace lock and publishes the snapshot, building its graph only when no
// generation with the same identity is published.
func (c *call) index() (mapping, error) {
	ws, st, err := c.open()
	if err != nil {
		return nil, err
	}
	workspaceLock, err := st.LockWorkspace(ws.ID)
	if err != nil {
		return nil, err
	}
	defer workspaceLock.Unlock()
	testhook.At("workspace-locked")
	captured, err := ws.Capture(st, captureAttempts)
	if err != nil {
		return nil, err
	}
	manifest, err := encodeManifest(captured.Files)
	if err != nil {
		return nil, err
	}
	staging, snapshot, err := st.Prepare(ws.ID, manifest,
		store.Snapshot{Workspace: ws.Root, Head: captured.Head, Dirty: captured.Dirty})
	if err != nil {
		return nil, err
	}
	if staging != nil {
		defer staging.Abandon()
		if err := graph.Build(filepath.Join(staging.Dir(), store.GraphName), captured.Files); err != nil {
			return nil, err
		}
		if snapshot, err = staging.Publish(); err != nil {
			return nil, err
		}
	}
	testhook.At("done")
	return mapping{
		{key: "snapshot", value: snapshotBlock(snapshot)},
		{key: "files", value: int64(len(captured.Files))},
	}, nil
}

type manifest struct {
	Manifest int            `json:"manifest"`
	Graph    int            `json:"graph"`
	Ladybug  string         `json:"ladybug"`
	Files    []manifestFile `json:"files"`
}

type manifestFile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// encodeManifest is the manifest of a generation and the serialization of its snapshot identity: the versions that
// shape the graph and every file of the snapshot sorted by path.
func encodeManifest(files []workspace.File) ([]byte, error) {
	m := manifest{Manifest: manifestVersion, Graph: graph.Version, Ladybug: ladybug.Version, Files: []manifestFile{}}
	for _, f := range files {
		if !utf8.ValidString(f.Path) {
			return nil, fmt.Errorf("the path %q is not UTF-8", f.Path)
		}
		m.Files = append(m.Files, manifestFile{Path: f.Path, Kind: f.Kind, Size: f.Size, SHA256: f.SHA256})
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
