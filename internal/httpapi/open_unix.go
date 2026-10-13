//go:build !windows

package httpapi

import (
	"os"
	"syscall"
)

// openNoFollow opens a file read-only and refuses a link at the last step.
func openNoFollow(abs string) (*os.File, error) {
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
