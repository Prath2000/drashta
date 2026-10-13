//go:build windows

package httpapi

import (
	"errors"
	"os"
)

// openNoFollow opens a file read-only. Windows has no O_NOFOLLOW, so a link is checked first.
func openNoFollow(abs string) (*os.File, error) {
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, &os.PathError{Op: "open", Path: abs, Err: errors.New("is a link")}
	}
	return os.OpenFile(abs, os.O_RDONLY, 0)
}
