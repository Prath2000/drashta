//go:build windows

package authored

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openLockFile opens the lock file. Windows has no O_NOFOLLOW, so a link is checked first.
func openLockFile(p string) (*os.File, error) {
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, errors.New("lock file is a link")
	}
	return os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
}

// tryLock takes an exclusive LockFileEx lock without blocking. locked is false when it is held.
func tryLock(f *os.File) (locked bool, err error) {
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
