package workspace

import (
	"io/fs"
	"syscall"
)

func stampOf(info fs.FileInfo) stamp {
	st := info.Sys().(*syscall.Stat_t)
	return stamp{
		present: true,
		mode:    info.Mode(),
		size:    info.Size(),
		mtime:   info.ModTime().UnixNano(),
		ctime:   st.Ctim.Nano(),
		ino:     st.Ino,
	}
}
