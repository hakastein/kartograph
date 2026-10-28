package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hakastein/kartograph/internal/testhook"
)

// Snapshot is the snapshot block of an answer and the workspace record that the pointer selects: which worktree,
// which generation, the commit of HEAD at capture ("" before the first commit) and whether the tree was dirty.
type Snapshot struct {
	Workspace  string `json:"workspace"`
	Generation string `json:"generation"`
	Head       string `json:"head"`
	Dirty      bool   `json:"dirty"`
}

// Staging is a generation under construction and the build's staging lock on it.
type Staging struct {
	s        *Store
	id       string
	dir      string
	manifest []byte
	snapshot Snapshot
	lock     *os.File
}

// Prepare starts the publication of a captured snapshot of workspace id, holding its workspace lock. When
// generations/<hash> of the manifest is already published, the pointer is set to it at once, and Prepare returns
// the snapshot without a staging. Otherwise it creates a staging and takes its staging lock.
func (s *Store) Prepare(id string, manifest []byte, snapshot Snapshot) (*Staging, Snapshot, error) {
	snapshot.Generation = Hash(manifest)
	storeLock, err := s.lockStore()
	if err != nil {
		return nil, Snapshot{}, err
	}
	defer storeLock.Close()
	state, err := s.generation(snapshot.Generation, manifest)
	if err != nil {
		return nil, Snapshot{}, err
	}
	if state == published {
		return nil, snapshot, s.point(id, snapshot)
	}
	dir := filepath.Join(s.root, stagingDir, randomName())
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, Snapshot{}, err
	}
	buildLock, err := lock(filepath.Join(dir, buildLockName))
	if err != nil {
		return nil, Snapshot{}, err
	}
	return &Staging{s: s, id: id, dir: dir, manifest: manifest, snapshot: snapshot, lock: buildLock}, snapshot, nil
}

// Dir is the staging directory; the build writes the graph file there under GraphName.
func (st *Staging) Dir() string {
	return st.dir
}

// Publish writes the manifest into the staging and publishes it: under the store lock the staging becomes
// generations/<hash>, or an equal published generation is reused and the staging goes to trash; then the pointer
// is replaced. The graph file must be closed cleanly.
func (st *Staging) Publish() (Snapshot, error) {
	s := st.s
	graph := filepath.Join(st.dir, GraphName)
	if err := os.Chmod(graph, 0o444); err != nil {
		return Snapshot{}, err
	}
	if err := syncPath(graph); err != nil {
		return Snapshot{}, err
	}
	if err := writeSynced(filepath.Join(st.dir, ManifestName), st.manifest, 0o444); err != nil {
		return Snapshot{}, err
	}
	if err := syncPath(st.dir); err != nil {
		return Snapshot{}, err
	}
	storeLock, err := s.lockStore()
	if err != nil {
		return Snapshot{}, err
	}
	trashed, err := st.publish()
	storeLock.Close()
	erase(trashed)
	return st.snapshot, err
}

func (st *Staging) publish() ([]string, error) {
	s := st.s
	testhook.At("publish-locked")
	hash := st.snapshot.Generation
	generation := filepath.Join(s.root, generationsDir, hash)
	state, err := s.generation(hash, st.manifest)
	if err != nil {
		return nil, err
	}
	var trashed []string
	if state == published {
		st.lock.Close()
		dir, err := s.toTrash(st.dir)
		if err != nil {
			return trashed, err
		}
		trashed = append(trashed, dir)
	} else {
		if state == notSnapshot {
			dir, err := s.toTrash(generation)
			if err != nil {
				return trashed, err
			}
			trashed = append(trashed, dir)
		}
		if err := os.Rename(st.dir, generation); err != nil {
			return trashed, err
		}
		if err := syncPath(filepath.Join(s.root, generationsDir)); err != nil {
			return trashed, err
		}
	}
	testhook.At("moved")
	if err := s.point(st.id, st.snapshot); err != nil {
		return trashed, err
	}
	testhook.At("pointer-swapped")
	if state != published {
		st.lock.Close()
		// a lock file left without a holder does not hold the generation (ADR-0004); the snapshot is published already
		os.Remove(filepath.Join(generation, buildLockName))
	}
	st.lock = nil
	return trashed, nil
}

// Abandon moves a staging that was not published to trash: the build failed before publication.
func (st *Staging) Abandon() {
	if st.lock == nil {
		return
	}
	storeLock, err := st.s.lockStore()
	if err != nil {
		return
	}
	st.lock.Close()
	st.lock = nil
	var trashed []string
	if _, err := os.Lstat(st.dir); err == nil {
		if dir, err := st.s.toTrash(st.dir); err == nil {
			trashed = append(trashed, dir)
		}
	}
	storeLock.Close()
	erase(trashed)
}

type generationState int

const (
	absent generationState = iota
	published
	notSnapshot
)

// generation tells whether generations/<hash> is published by this protocol with this manifest. A directory there
// without a manifest is not a snapshot. Another manifest under the same hash is an error, never a snapshot to reuse.
func (s *Store) generation(hash string, manifest []byte) (generationState, error) {
	dir := filepath.Join(s.root, generationsDir, hash)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return absent, nil
	} else if err != nil {
		return absent, err
	}
	stored, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return notSnapshot, nil
	}
	if err != nil {
		return absent, err
	}
	if !bytes.Equal(stored, manifest) {
		return absent, fmt.Errorf("generation %s holds another manifest", hash)
	}
	return published, nil
}

// point writes the workspace record of the snapshot and replaces the pointer of the workspace with a symbolic link to
// its generation, both by rename, then removes the records of other generations. It runs under the store lock.
func (s *Store) point(id string, snapshot Snapshot) error {
	dir := filepath.Join(s.root, workspacesDir, id)
	record, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".record-"+randomName())
	if err := writeSynced(tmp, append(record, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, recordName(snapshot.Generation))); err != nil {
		return err
	}
	pointer := filepath.Join(dir, pointerName)
	target := pointerTarget(snapshot.Generation)
	if current, err := os.Readlink(pointer); err != nil || current != target {
		tmp := filepath.Join(dir, ".pointer-"+randomName())
		if err := os.Symlink(target, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, pointer); err != nil {
			return err
		}
	}
	if err := syncPath(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".json") && name != recordName(snapshot.Generation) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func recordName(hash string) string {
	return hash + ".json"
}

func pointerTarget(hash string) string {
	return "../../" + generationsDir + "/" + hash
}
