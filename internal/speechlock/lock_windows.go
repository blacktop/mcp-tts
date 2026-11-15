//go:build windows

package speechlock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffsetHigh places the one locked byte at offset 1<<32, far past the
// holder record. Windows byte-range locks are mandatory, so locking the
// record itself would stop other processes from reading it.
const lockOffsetHigh = 1

// ensurePrivateDir creates dir. It lives under %LocalAppData%, whose inherited
// ACL already limits access to the user, SYSTEM and Administrators.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating speech lock directory %s: %w", dir, err)
	}
	return nil
}

func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}

func lockRange() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockOffsetHigh}
}

func tryLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, lockRange(),
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func releaseFile(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, lockRange())
	file.Close()
}
