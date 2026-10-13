//go:build windows

package claude

import "os"

// fileInode returns 0: Windows has no cheap inode in os.FileInfo.
func fileInode(fi os.FileInfo) uint64 { return 0 }
