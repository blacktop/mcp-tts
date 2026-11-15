// Package speechlock serializes speech across mcp-tts processes with a kernel
// advisory file lock.
//
// The kernel releases the lock when its last holder exits, so there is no stale
// state to detect or reclaim. The lock file is never unlinked, renamed or
// replaced: every participant must lock the same inode.
package speechlock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

const (
	lockFileName   = "speech.lock"
	holderMaxBytes = 4 << 10
	pollBase       = 25 * time.Millisecond
	pollJitter     = 50 * time.Millisecond
)

// Holder is what the current lock owner recorded about itself. It is advisory
// information for error messages: the record can be stale or missing, and it
// says who holds the lock, not why.
type Holder struct {
	PID   int       `json:"pid"`
	Label string    `json:"label"`
	Since time.Time `json:"since"`
	// PlayerPID is the playback child that inherited the lock, when there is
	// one. It can outlive PID and is then the process still holding the lock.
	PlayerPID int `json:"player_pid,omitempty"`
}

// Lock is an acquired speech lock. A nil *Lock is valid and holds nothing, so
// callers that run without serialization need no special casing.
type Lock struct {
	file   *os.File
	holder Holder
}

// DefaultPath returns the lock file shared by every mcp-tts process of this
// user, creating its private directory when needed.
//
// The directory lives under the user cache directory rather than TMPDIR: it is
// the same in every launch context and is not swept by temp cleaners.
func DefaultPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache directory: %w", err)
	}
	return pathIn(base)
}

func pathIn(base string) (string, error) {
	dir := filepath.Join(base, "mcp-tts")
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, lockFileName), nil
}

// Acquire waits for the lock at path until ctx is done. It returns ctx.Err()
// when the wait is cancelled or times out, and never leaves the lock held in
// that case.
func Acquire(ctx context.Context, path, label string) (*Lock, error) {
	file, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening speech lock %s: %w", path, err)
	}
	for {
		locked, err := tryLock(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("locking speech lock %s: %w", path, err)
		}
		if locked {
			// The caller may have given up while this attempt was in flight.
			if err := ctx.Err(); err != nil {
				releaseFile(file)
				return nil, err
			}
			lock := &Lock{file: file, holder: Holder{PID: os.Getpid(), Label: label, Since: time.Now()}}
			lock.record()
			return lock, nil
		}
		retry := time.NewTimer(pollBase + rand.N(pollJitter))
		select {
		case <-ctx.Done():
			retry.Stop()
			file.Close()
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}

// File returns the locked file so a playback child can inherit it. It is nil
// for a nil Lock.
func (l *Lock) File() *os.File {
	if l == nil {
		return nil
	}
	return l.file
}

// Release closes this holder's file. On Unix an inherited descriptor keeps
// the lock held until the player exits, even if supervision has timed out.
// The last holder record is retained because that player may still be alive.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	releaseFile(l.file)
	l.file = nil
}

// RecordPlayer adds the playback child's PID to the holder record.
func (l *Lock) RecordPlayer(pid int) {
	if l == nil || l.file == nil {
		return
	}
	l.holder.PlayerPID = pid
	l.record()
}

// record stores the holder for diagnostics. A failed write only costs the
// detail in a later "busy" message, so it must not fail the acquisition.
func (l *Lock) record() {
	data, err := json.Marshal(l.holder)
	if err != nil {
		return
	}
	if err := l.file.Truncate(0); err != nil {
		return
	}
	_, _ = l.file.WriteAt(data, 0)
}

// ReadHolder returns the recorded holder of the lock at path, if a readable
// record exists.
func ReadHolder(path string) (Holder, bool) {
	file, err := os.Open(path)
	if err != nil {
		return Holder{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, holderMaxBytes))
	if err != nil || len(data) == 0 {
		return Holder{}, false
	}
	var holder Holder
	if json.Unmarshal(data, &holder) != nil || holder.PID == 0 {
		return Holder{}, false
	}
	return holder, true
}
