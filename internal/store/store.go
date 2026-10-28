// Package store keeps snapshots under the store root by the protocol of ADR-0004: generations behind workspace
// pointers, staging, trash, leases, the store and workspace locks, and the content-addressed blobs of captures.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const (
	generationsDir = "generations"
	stagingDir     = "staging"
	trashDir       = "trash"
	workspacesDir  = "workspaces"
	blobsDir       = "blobs"
	lockName       = "lock"
	buildLockName  = "build.lock"
	pointerName    = "current"
	leasePrefix    = "lease-"

	// ManifestName and GraphName are the files of a generation.
	ManifestName = "manifest.json"
	GraphName    = "graph.lbug"
)

// Store is the root of the store.
type Store struct {
	root string
}

// Root returns the store root: $XDG_CACHE_HOME/kartograph, or $HOME/.cache/kartograph when XDG_CACHE_HOME is unset
// or not absolute.
func Root(getenv func(string) string) (string, error) {
	if cache := getenv("XDG_CACHE_HOME"); filepath.IsAbs(cache) {
		return filepath.Join(cache, "kartograph"), nil
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".cache", "kartograph"), nil
	}
	return "", errors.New("neither XDG_CACHE_HOME nor HOME is an absolute path")
}

func Open(root string) (*Store, error) {
	for _, dir := range []string{"", generationsDir, stagingDir, trashDir, workspacesDir, blobsDir} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

// Hash is the name of the generation of a manifest: the hex SHA-256 of its bytes.
func Hash(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

// PutBlob stores the captured bytes under their digest. A blob is written to a temporary name and renamed, so a
// partial blob never has a digest name; blobs are not synced, and a reader checks a blob against its digest.
func (s *Store) PutBlob(digest string, data []byte) error {
	dir := filepath.Join(s.root, blobsDir, digest[:2])
	final := filepath.Join(dir, digest)
	if _, err := os.Lstat(final); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), final)
}

// lockStore takes the store lock; closing the file releases it.
func (s *Store) lockStore() (*os.File, error) {
	return lock(filepath.Join(s.root, lockName))
}

// WorkspaceLock is held by a build from capture to publication.
type WorkspaceLock struct {
	f *os.File
}

// LockWorkspace waits for the workspace lock. It is never taken under the store lock.
func (s *Store) LockWorkspace(id string) (*WorkspaceLock, error) {
	dir := filepath.Join(s.root, workspacesDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := lock(filepath.Join(dir, lockName))
	if err != nil {
		return nil, err
	}
	return &WorkspaceLock{f: f}, nil
}

func (l *WorkspaceLock) Unlock() {
	l.f.Close()
}

func lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func flock(f *os.File, how int) error {
	for {
		if err := syscall.Flock(int(f.Fd()), how); !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func randomName() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// writeSynced creates path with data and syncs it; the file must not exist.
func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// the mode must not depend on the umask of whoever runs kartograph
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// toTrash moves a directory of generations/ or staging/ to trash/ under the store lock and returns its new path.
func (s *Store) toTrash(dir string) (string, error) {
	trashed := filepath.Join(s.root, trashDir, randomName())
	return trashed, os.Rename(dir, trashed)
}

// erase removes trashed directories after the store lock is released.
func erase(trashed []string) {
	for _, dir := range trashed {
		os.RemoveAll(dir)
	}
}
