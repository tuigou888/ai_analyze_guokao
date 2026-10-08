//go:build windows

package distill

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

// All processes lock the same first byte. Closing the handle (including process
// termination) releases the OS lock; the shared lock file is never unlinked.
func lockRunFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrRunActive
	}
	return err
}

func unlockRunFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
