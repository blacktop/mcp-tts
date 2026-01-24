package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
)

const globalTTSLockDir = "/tmp/mcp-tts-global.lock.d"

// lockContent stores structured information in the lock file
type lockContent struct {
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	Hostname  string    `json:"hostname"`
}

// Directory-based file locking for TTS coordination (mkdir is atomic)
type ttsMutexFile struct {
	lockDir     string
	contentFile string
}

// acquireGlobalTTSLock - simple file-based locking for multiple MCP instances
func acquireGlobalTTSLock(ctx context.Context) (release func(), err error) {
	log.Debug("acquireGlobalTTSLock called", "sequentialTTS", sequentialTTS, "pid", os.Getpid())
	if !sequentialTTS {
		log.Debug("Sequential TTS disabled, skipping global lock", "pid", os.Getpid())
		return func() {}, nil
	}

	lockDir := globalTTSLockDir
	if runtime.GOOS == "windows" {
		lockDir = filepath.Join(os.TempDir(), "mcp-tts-global.lock.d")
	}

	lock := &ttsMutexFile{
		lockDir:     lockDir,
		contentFile: filepath.Join(lockDir, "content.json"),
	}

	// Try to acquire lock with context cancellation
	log.Debug("Attempting to acquire global TTS lock", "lockDir", lockDir, "pid", os.Getpid())
	acquired := make(chan error, 1)
	go func() {
		acquired <- lock.acquireLock(ctx)
	}()

	select {
	case err := <-acquired:
		if err != nil {
			log.Debug("Failed to acquire global TTS lock", "lockDir", lockDir, "pid", os.Getpid(), "error", err)
			return nil, err
		}
		log.Debug("Global TTS lock acquired successfully", "lockDir", lockDir, "pid", os.Getpid())
		return func() {
			log.Debug("Releasing global TTS lock", "lockDir", lockDir, "pid", os.Getpid())
			lock.releaseLock()
			log.Debug("Global TTS lock released", "lockDir", lockDir, "pid", os.Getpid())
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acquireLock attempts to get the global lock with retry using atomic directory creation
func (m *ttsMutexFile) acquireLock(ctx context.Context) error {
	for {
		// Try atomic directory creation (mkdir is atomic across all filesystems)
		log.Debug("Attempting atomic directory creation", "lockDir", m.lockDir, "pid", os.Getpid())
		err := os.Mkdir(m.lockDir, 0755)
		if err == nil {
			log.Debug("Successfully created lock directory", "lockDir", m.lockDir, "pid", os.Getpid())
			// Got the lock - write structured lock content to file inside directory
			hostname, _ := os.Hostname()
			content := lockContent{
				PID:       os.Getpid(),
				StartTime: time.Now(),
				Hostname:  hostname,
			}
			if data, err := json.Marshal(content); err == nil {
				os.WriteFile(m.contentFile, data, 0644)
			}
			return nil
		}

		if !os.IsExist(err) {
			log.Debug("Unexpected error creating lock directory", "lockDir", m.lockDir, "pid", os.Getpid(), "error", err)
			return fmt.Errorf("failed to create lock directory: %w", err)
		}

		log.Debug("Lock directory already exists, checking if stale", "lockDir", m.lockDir, "pid", os.Getpid())

		// Lock exists - atomically cleanup if stale
		if m.atomicCleanupStale() {
			log.Debug("Successfully cleaned up stale lock, retrying", "lockDir", m.lockDir, "pid", os.Getpid())
			continue // Successfully cleaned up stale lock, retry immediately
		}

		// Wait and retry with jitter to prevent synchronized attempts
		jitter := time.Duration(25+rand.Intn(50)) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitter):
			continue
		}
	}
}

// releaseLock releases the directory lock with proper error handling
func (m *ttsMutexFile) releaseLock() {
	// Remove the content file first
	os.Remove(m.contentFile)
	// Then remove the lock directory
	if err := os.Remove(m.lockDir); err != nil {
		// Log error but don't fail - stale detection will clean it up
		log.Debug("Failed to remove lock directory", "lockDir", m.lockDir, "error", err)
	}
}

// atomicCleanupStale uses atomic rename to safely clean up stale directory locks
func (m *ttsMutexFile) atomicCleanupStale() bool {
	if !m.isStale() {
		return false
	}

	// Use atomic rename to claim the stale lock directory for cleanup
	staleDir := m.lockDir + ".stale." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.Rename(m.lockDir, staleDir); err != nil {
		// Another process may have already cleaned it up or acquired the lock
		log.Debug("Failed to claim stale lock for cleanup", "lockDir", m.lockDir, "pid", os.Getpid(), "error", err)
		return false
	}

	// Successfully claimed the stale lock directory - remove it completely
	log.Debug("Successfully claimed and cleaning up stale lock", "original", m.lockDir, "temp", staleDir, "pid", os.Getpid())
	os.RemoveAll(staleDir)
	return true
}

// isStale determines whether the existing lock directory should be considered stale.
// A lock is stale only if:
//   - The recorded PID is not running anymore, OR
//   - The lock metadata is missing/invalid AND the directory is older than a grace period.
//
// We deliberately do NOT time out active locks by age alone to avoid breaking
// long-running speech operations.
func (m *ttsMutexFile) isStale() bool {
	// If we can read valid lock metadata, prefer process liveness over age.
	if data, err := os.ReadFile(m.contentFile); err == nil {
		var content lockContent
		if json.Unmarshal(data, &content) == nil {
			if isProcessAlive(content.PID) {
				// Process is alive: not stale regardless of age.
				return false
			}
			// Process is not running: stale.
			return true
		}
		// Corrupt JSON: fall through to age heuristic below.
	}

	// No readable/valid metadata: use directory age as a conservative heuristic.
	// Give plenty of time in case another process just created the dir but
	// hasn't written content yet.
	const grace = 5 * time.Minute
	if fi, err := os.Stat(m.lockDir); err == nil {
		return time.Since(fi.ModTime()) > grace
	}
	// If we cannot stat the directory, assume stale so callers can clean up.
	return true
}
