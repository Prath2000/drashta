//go:build !windows

package claude

import (
	"os"
	"syscall"
)

// fileInode returns the inode number, or 0 when it is not known.
func fileInode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
