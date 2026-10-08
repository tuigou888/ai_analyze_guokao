//go:build unix

package distill

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockRunFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrRunActive
	}
	return err
}

func unlockRunFile(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
