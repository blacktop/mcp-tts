//go:build !windows

package speechlock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ensurePrivateDir creates dir if needed and requires it to be a real
// directory owned by this user with no group or other access, so another
// local user can neither plant nor hold the lock file.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating speech lock directory %s: %w", dir, err)
	}
	var st unix.Stat_t
	if err := unix.Lstat(dir, &st); err != nil {
		return fmt.Errorf("inspecting speech lock directory %s: %w", dir, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Getuid()) || st.Mode&0o077 != 0 {
		return fmt.Errorf("speech lock directory %s must be a directory owned by you with mode 0700; fix it with: chmod 700 %q", dir, dir)
	}
	return nil
}

func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0o077 != 0 {
		unix.Close(fd)
		return nil, errors.New("lock file must be a regular file owned by you with mode 0600")
	}
	return os.NewFile(uintptr(fd), path), nil
}

func tryLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return false, err
}

func releaseFile(file *os.File) {
	// LOCK_UN would also unlock copies inherited by a still-running player.
	file.Close()
}
