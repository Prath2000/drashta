//go:build !windows

package tree

import (
	"os"
	"syscall"
)

// openNoFollow opens a file read-only without following a link and without blocking on a pipe.
func openNoFollow(abs string) (*os.File, error) {
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
