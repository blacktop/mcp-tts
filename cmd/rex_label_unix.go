//go:build !windows

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/unix"
)

const rexLabelStateLimit = 64 << 10
const rexLabelLeaseLimit = 128

type rexLabelState struct {
	Original string   `json:"original"`
	Owned    string   `json:"owned"`
	Leases   []string `json:"leases"`
}

func rexLabelDirectory(dir string) (string, error) {
	if dir == "" {
		// Unlike TMPDIR, this location is shared by all local launch contexts.
		dir = filepath.Join("/tmp", fmt.Sprintf("mcp-tts-rex-%d", os.Getuid()))
	}
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	var st unix.Stat_t
	if err := unix.Lstat(dir, &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 {
		return "", errors.New("Rex label state directory is not private and owned by this user")
	}
	return dir, nil
}

func openRexLabelFile(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0077 != 0 {
		unix.Close(fd)
		return nil, errors.New("Rex label state file is not private and owned by this user")
	}
	return os.NewFile(uintptr(fd), path), nil
}

func lockRexLabels(ctx context.Context, dir string) (*os.File, error) {
	f, err := openRexLabelFile(filepath.Join(dir, "lock"), unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func validRexLease(name string) bool {
	suffix, ok := strings.CutPrefix(name, "lease-")
	return ok && len(suffix) > 0 && len(suffix) <= 32 && strings.IndexFunc(suffix, func(r rune) bool {
		return !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", r)
	}) < 0
}

func rexLabelStatePath(dir string, target rexLabelTarget) string {
	key := sha256.Sum256([]byte(target.host + "\x00" + target.session + "\x00" + target.window))
	return filepath.Join(dir, fmt.Sprintf("%x.json", key))
}

func readRexLabelState(path, window string) (*rexLabelState, error) {
	f, err := openRexLabelFile(path, unix.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, rexLabelStateLimit+1))
	if err != nil {
		return nil, err
	}
	var state rexLabelState
	if len(data) > rexLabelStateLimit || json.Unmarshal(data, &state) != nil ||
		!validRexLabel(state.Original) || !validRexLabel(state.Owned) ||
		(state.Owned != "" && state.Owned != markedRexLabel(state.Original) &&
			!(automaticRexLabel(window, state.Original) && strings.HasPrefix(state.Owned, rexLabelPrefix))) ||
		len(state.Leases) > rexLabelLeaseLimit {
		return nil, errors.New("invalid Rex label state")
	}
	for _, lease := range state.Leases {
		if !validRexLease(lease) {
			return nil, errors.New("invalid Rex label lease")
		}
	}
	return &state, nil
}

func writeRexLabelState(path string, state *rexLabelState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > rexLabelStateLimit {
		return errors.New("Rex label state is too large")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// A lease is live exactly while its file is locked. The OS releases that lock
// on process exit, so crash recovery does not depend on PID reuse or timeouts.
func pruneRexLabelLeases(dir string, leases []string, remove string) ([]string, error) {
	live := make([]string, 0, len(leases))
	for _, name := range leases {
		if name == remove {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := openRexLabelFile(path, unix.O_RDWR)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		f.Close()
		switch {
		case err == nil:
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			live = append(live, name)
		default:
			return nil, err
		}
	}
	return live, nil
}

func (r *rexLabelClient) update(ctx context.Context, dir string, target rexLabelTarget, lease string, starting bool) error {
	path := rexLabelStatePath(dir, target)
	state, err := readRexLabelState(path, target.window)
	if err != nil || (!starting && (state == nil || !slices.Contains(state.Leases, lease))) {
		return err
	}
	if state != nil {
		remove := ""
		if !starting {
			remove = lease
		}
		state.Leases, err = pruneRexLabelLeases(dir, state.Leases, remove)
		if err != nil {
			return err
		}
		if state.Owned != "" && target.label != state.Owned {
			state.Owned = ""
		}
	}
	if starting {
		if state == nil || (len(state.Leases) == 0 && state.Owned == "") {
			state = &rexLabelState{Original: target.label}
			if !strings.HasPrefix(target.label, rexLabelPrefix) {
				state.Owned, err = r.speakingLabel(ctx, target)
				if err != nil {
					return err
				}
			}
		} else if len(state.Leases) == 0 && automaticRexLabel(target.window, state.Original) {
			// Refresh an abandoned automatic period, including v0.1.48's
			// marker made from the raw default label, without losing its reset.
			displayTarget := target
			displayTarget.label = state.Original
			owned, titleErr := r.speakingLabel(ctx, displayTarget)
			if titleErr != nil {
				if err := writeRexLabelState(path, state); err != nil {
					return errors.Join(titleErr, err)
				}
				if err := r.rename(ctx, target, ""); err != nil {
					return errors.Join(titleErr, err)
				}
				return errors.Join(titleErr, os.Remove(path))
			}
			state.Owned = owned
		}
		if len(state.Leases) >= rexLabelLeaseLimit {
			return errors.New("too many simultaneous Rex label leases")
		}
		state.Leases = append(state.Leases, lease)
		// Persist ownership before the RPC: a timeout may still have renamed.
		if err := writeRexLabelState(path, state); err != nil {
			return err
		}
		if state.Owned != "" && target.label != state.Owned {
			return r.rename(ctx, target, state.Owned)
		}
		return nil
	}
	if len(state.Leases) > 0 {
		return writeRexLabelState(path, state)
	}
	if state.Owned != "" {
		if err := writeRexLabelState(path, state); err != nil {
			return err
		}
		// Rex has no compare-and-set. A manual rename can race this last read.
		restore := state.Original
		if automaticRexLabel(target.window, restore) {
			restore = ""
		}
		if err := r.rename(ctx, target, restore); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func beginRexTabLabel(ctx context.Context, r *rexLabelClient, id string) func() {
	ctx, cancel := context.WithTimeout(ctx, rexLabelTimeout)
	defer cancel()
	dir, err := rexLabelDirectory(r.stateDir)
	if err != nil {
		log.Warn("Rex tab label unavailable", "error", err)
		return func() {}
	}
	lock, err := lockRexLabels(ctx, dir)
	if err != nil {
		log.Warn("Rex tab label unavailable", "error", err)
		return func() {}
	}
	defer lock.Close()
	target, err := r.locate(ctx, "")
	if err != nil {
		log.Warn("Rex tab label target unavailable", "error", err)
		return func() {}
	}
	name := "lease-" + strings.TrimPrefix(id, "mcp-tts/")
	if !validRexLease(name) {
		return func() {}
	}
	path := filepath.Join(dir, name)
	lease, err := openRexLabelFile(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
	if err == nil {
		err = unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		if lease != nil {
			lease.Close()
			os.Remove(path)
		}
		log.Warn("Rex tab label lease unavailable", "error", err)
		return func() {}
	}
	if err := r.update(ctx, dir, target, name, true); err != nil {
		log.Warn("Could not mark Rex tab", "error", err)
	}
	return func() {
		defer os.Remove(path)
		defer lease.Close()
		ctx, cancel := context.WithTimeout(context.Background(), rexLabelTimeout)
		defer cancel()
		lock, err := lockRexLabels(ctx, dir)
		if err == nil {
			defer lock.Close()
			var current rexLabelTarget
			current, err = r.locate(ctx, target.window)
			if err == nil && current.host != target.host {
				err = errors.New("Rex server identity changed")
			}
			if err == nil {
				err = r.update(ctx, dir, current, name, false)
			}
		}
		if err != nil {
			log.Warn("Could not restore Rex tab label", "error", err)
		}
	}
}
