// Package graph builds the graph file of a snapshot in its staging. The graph holds one File node per file of the
// snapshot.
package graph

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hakastein/kartograph/internal/ladybug"
	"github.com/hakastein/kartograph/internal/testhook"
	"github.com/hakastein/kartograph/internal/workspace"
)

// Version is the version of the graph schema and of what the build puts into it; it is part of snapshot identity.
const Version = 1

// The buffer pools are set explicitly: the engine's default takes 80% of RAM.
const (
	buildBufferPool = 512 << 20
	readBufferPool  = 256 << 20
	maxSize         = 1 << 36
)

// ReadConfig opens a published graph file: read-only, so the engine opens the file without write access.
func ReadConfig() ladybug.Config {
	return ladybug.Config{BufferPool: readBufferPool, Threads: uint64(runtime.NumCPU()), MaxSize: maxSize, ReadOnly: true}
}

const schema = "CREATE NODE TABLE File(path STRING PRIMARY KEY, kind STRING, size INT64, sha256 STRING)"

// Build writes the graph of files to path in the staging dir and closes it cleanly: a checkpoint, then close, and no
// write-ahead log or shadow file is left next to it.
func Build(path string, files []workspace.File) error {
	db, err := ladybug.Open(path, ladybug.Config{
		BufferPool: buildBufferPool, Threads: uint64(runtime.NumCPU()), MaxSize: maxSize,
	})
	if err != nil {
		return err
	}
	if err := load(db, filepath.Dir(path), files); err != nil {
		db.Close()
		return err
	}
	testhook.At("graph-open")
	db.Close()
	for _, suffix := range []string{".wal", ".shadow"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("the graph file %s was not closed cleanly: %s is left", path, path+suffix)
		}
	}
	return nil
}

func load(db *ladybug.DB, dir string, files []workspace.File) error {
	if err := db.Exec(schema); err != nil {
		return err
	}
	if len(files) > 0 {
		rows := filepath.Join(dir, "files.csv")
		if err := writeRows(rows, files); err != nil {
			return err
		}
		statement := "COPY File FROM " + literal(rows) + " (HEADER=false, PARALLEL=false, QUOTE='\"', ESCAPE='\"', DELIM=',')"
		if err := db.Exec(statement); err != nil {
			return err
		}
		if err := os.Remove(rows); err != nil {
			return err
		}
	}
	return db.Exec("CHECKPOINT")
}

// literal is a Cypher string literal of s.
func literal(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func writeRows(path string, files []workspace.File) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	for _, file := range files {
		if err := w.Write([]string{file.Path, file.Kind, strconv.FormatInt(file.Size, 10), file.SHA256}); err != nil {
			f.Close()
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
