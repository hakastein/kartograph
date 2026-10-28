package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/hakastein/kartograph/internal/testhook"
)

// File is one entry of a snapshot. Content is the bytes of a regular file or the target of a symbolic link.
type File struct {
	// Path is slash-separated and relative to the worktree root.
	Path string
	// Kind is "file", "executable" or "symlink".
	Kind   string
	Size   int64
	SHA256 string
}

// Capture is a snapshot of a worktree: its files sorted by path, the commit of HEAD ("" before the first commit)
// and whether git status showed changes when it was taken.
type Capture struct {
	Head  string
	Dirty bool
	Files []File
}

// Blobs is the content-addressed store the captured bytes go to.
type Blobs interface {
	PutBlob(digest string, data []byte) error
}

// Unstable is a worktree that kept changing during every attempt to capture it.
type Unstable struct {
	Attempts int
}

func (u *Unstable) Error() string {
	return fmt.Sprintf("the worktree changed during each of %d attempts to capture it", u.Attempts)
}

// Capture reads the worktree into blobs and checks the listing, the file stamps and HEAD again afterwards; when they
// differ it tries again, at most attempts times.
func (w *Workspace) Capture(blobs Blobs, attempts int) (*Capture, error) {
	for range attempts {
		c, err := w.attempt(blobs)
		if err != nil {
			return nil, err
		}
		if c != nil {
			return c, nil
		}
	}
	return nil, &Unstable{Attempts: attempts}
}

// attempt returns nil without an error when the worktree changed while it was read.
func (w *Workspace) attempt(blobs Blobs) (*Capture, error) {
	before, err := w.look()
	if err != nil {
		return nil, err
	}
	files, err := w.read(before, blobs)
	if errors.Is(err, errChanged) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dirty, err := w.dirty()
	if err != nil {
		return nil, err
	}
	testhook.At("captured")
	after, err := w.look()
	if err != nil {
		return nil, err
	}
	if !before.equal(after) {
		return nil, nil
	}
	return &Capture{Head: before.head, Dirty: dirty, Files: files}, nil
}

// look is the state of a worktree that a capture compares before and after reading it.
type look struct {
	head   string
	paths  []string
	stamps map[string]stamp
}

func (l look) equal(other look) bool {
	return l.head == other.head && slices.Equal(l.paths, other.paths) && maps.Equal(l.stamps, other.stamps)
}

// stamp is what lstat says about a path; a path that is gone or lies beyond a symbolic link is absent.
type stamp struct {
	present bool
	mode    fs.FileMode
	size    int64
	mtime   int64
	ctime   int64
	ino     uint64
}

func (w *Workspace) look() (look, error) {
	head, err := w.head()
	if err != nil {
		return look{}, err
	}
	paths, err := w.paths(w.Root, "")
	if err != nil {
		return look{}, err
	}
	slices.Sort(paths)
	dirs := map[string]bool{}
	stamps := make(map[string]stamp, len(paths))
	for _, p := range paths {
		s, err := w.stamp(p, dirs)
		if err != nil {
			return look{}, err
		}
		stamps[p] = s
	}
	return look{head: head, paths: paths, stamps: stamps}, nil
}

// paths lists the worktree or submodule checkout dir at prefix and descends into checked-out submodules.
func (w *Workspace) paths(dir, prefix string) ([]string, error) {
	listed, err := w.list(dir, prefix)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range listed {
		full := w.full(p)
		info, err := os.Lstat(full)
		if err != nil || !info.IsDir() {
			paths = append(paths, p)
			continue
		}
		if _, err := os.Lstat(filepath.Join(full, ".git")); err != nil {
			continue
		}
		sub, err := w.paths(full, p+"/")
		if err != nil {
			return nil, err
		}
		paths = append(paths, sub...)
	}
	return paths, nil
}

func (w *Workspace) stamp(p string, dirs map[string]bool) (stamp, error) {
	for dir := pathDir(p); dir != ""; dir = pathDir(dir) {
		real, seen := dirs[dir]
		if !seen {
			info, err := os.Lstat(w.full(dir))
			real = err == nil && info.IsDir()
			dirs[dir] = real
		}
		if !real {
			return stamp{}, nil
		}
	}
	info, err := os.Lstat(w.full(p))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return stamp{}, nil
	}
	if err != nil {
		return stamp{}, err
	}
	return stampOf(info), nil
}

func pathDir(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

func (w *Workspace) full(p string) string {
	return filepath.Join(w.Root, filepath.FromSlash(p))
}

var errChanged = errors.New("a file changed while it was read")

func (w *Workspace) read(l look, blobs Blobs) ([]File, error) {
	var files []File
	for _, p := range l.paths {
		s := l.stamps[p]
		var data []byte
		var kind string
		switch {
		case !s.present:
			continue
		case s.mode.IsRegular():
			var err error
			if data, err = readFile(w.full(p), s); err != nil {
				return nil, err
			}
			kind = "file"
			if s.mode.Perm()&0o111 != 0 {
				kind = "executable"
			}
		case s.mode&fs.ModeSymlink != 0:
			target, inside, err := w.link(p)
			if err != nil {
				return nil, err
			}
			if !inside {
				continue
			}
			data, kind = []byte(target), "symlink"
		default:
			continue
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		if err := blobs.PutBlob(digest, data); err != nil {
			return nil, err
		}
		files = append(files, File{Path: p, Kind: kind, Size: int64(len(data)), SHA256: digest})
	}
	return files, nil
}

// readFile reads a regular file without following a symbolic link put in its place after lstat.
func readFile(path string, s stamp) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
		return nil, errChanged
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if opened := stampOf(info); opened.ino != s.ino || opened.size != s.size || opened.mtime != s.mtime {
		return nil, errChanged
	}
	return io.ReadAll(f)
}

// link returns the target of a symbolic link and whether it resolves to a path inside the worktree; a link that
// does not resolve or leads into another tree is not part of the snapshot.
func (w *Workspace) link(p string) (string, bool, error) {
	full := w.full(p)
	target, err := os.Readlink(full)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.EINVAL) {
		return "", false, errChanged
	}
	if err != nil {
		return "", false, err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", false, nil
	}
	inside := resolved == w.Root || strings.HasPrefix(resolved, w.Root+string(filepath.Separator))
	return target, inside, nil
}
