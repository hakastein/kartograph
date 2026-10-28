package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hakastein/kartograph/internal/testhook"
)

// ErrNoSnapshot is a workspace without a published snapshot.
var ErrNoSnapshot = errors.New("the workspace has no published snapshot")

// Lease is a reader's hold on a generation: a lease file in it with the reader's flock on it.
type Lease struct {
	s        *Store
	file     *os.File
	Snapshot Snapshot
	// Graph is the graph file of the generation, to be opened read-only.
	Graph string
}

// Read dereferences the pointer of workspace id once and takes a lease on its generation, both under the store lock.
// The graph file is opened after Read returns.
func (s *Store) Read(id string) (*Lease, error) {
	storeLock, err := s.lockStore()
	if err != nil {
		return nil, err
	}
	defer storeLock.Close()
	dir := filepath.Join(s.root, workspacesDir, id)
	target, err := os.Readlink(filepath.Join(dir, pointerName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoSnapshot
	}
	if err != nil {
		return nil, err
	}
	hash, ok := strings.CutPrefix(target, pointerTarget(""))
	if !ok || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("the pointer of workspace %s names %q", id, target)
	}
	testhook.At("leasing")
	data, err := os.ReadFile(filepath.Join(dir, recordName(hash)))
	if err != nil {
		return nil, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("the record of workspace %s: %w", id, err)
	}
	generation := filepath.Join(s.root, generationsDir, hash)
	f, err := os.OpenFile(filepath.Join(generation, leasePrefix+randomName()), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		os.Remove(f.Name())
		f.Close()
		return nil, err
	}
	return &Lease{s: s, file: f, Snapshot: snapshot, Graph: filepath.Join(generation, GraphName)}, nil
}

// Release removes the lease file and lets the flock go, under the store lock. The graph file must be closed.
func (l *Lease) Release() {
	storeLock, err := l.s.lockStore()
	if err == nil {
		os.Remove(l.file.Name())
	}
	l.file.Close()
	if err == nil {
		storeLock.Close()
	}
}
