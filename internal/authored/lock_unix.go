//go:build !windows

package authored

import (
	"os"
	"syscall"
)

// openLockFile opens the lock file and refuses a symlink as the last path part.
func openLockFile(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o644)
}

// tryLock takes the exclusive flock without blocking. locked is false when someone else holds it.
func tryLock(f *os.File) (locked bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EINTR {
		return false, nil
	}
	return false, err
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
