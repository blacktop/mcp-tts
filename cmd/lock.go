package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const globalTTSLockFile = "/tmp/mcp-tts-global.lock"

// Simple cross-platform file locking for TTS coordination
type ttsMutexFile struct {
	path string
	file *os.File
}

// acquireGlobalTTSLock - simple file-based locking for multiple MCP instances
func acquireGlobalTTSLock(ctx context.Context) (release func(), err error) {
	if !sequentialTTS {
		return func() {}, nil
	}

	lockPath := globalTTSLockFile
	if runtime.GOOS == "windows" {
		lockPath = filepath.Join(os.TempDir(), "mcp-tts-global.lock")
	}

	lock := &ttsMutexFile{path: lockPath}

	// Try to acquire lock with context cancellation
	acquired := make(chan error, 1)
	go func() {
		acquired <- lock.acquireLock(ctx)
	}()

	select {
	case err := <-acquired:
		if err != nil {
			return nil, err
		}
		return func() { lock.releaseLock() }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acquireLock attempts to get the global lock with retry
func (m *ttsMutexFile) acquireLock(ctx context.Context) error {
	dir := filepath.Dir(m.path)
	os.MkdirAll(dir, 0755)

	for {
		// Try exclusive file creation
		file, err := os.OpenFile(m.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			// Got the lock - write PID for debugging
			fmt.Fprintf(file, "PID: %d\nTime: %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			file.Sync()
			m.file = file
			return nil
		}

		if !os.IsExist(err) {
			return fmt.Errorf("failed to create lock file: %w", err)
		}

		// Lock exists - check if stale (older than 2 minutes)
		if m.isStale() {
			os.Remove(m.path) // Remove stale lock and retry
			continue
		}

		// Wait and retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			continue
		}
	}
}

// releaseLock releases the file lock
func (m *ttsMutexFile) releaseLock() {
	if m.file != nil {
		m.file.Close()
		os.Remove(m.path)
		m.file = nil
	}
}

// isStale checks if lock is older than 2 minutes (likely from crashed process)
func (m *ttsMutexFile) isStale() bool {
	info, err := os.Stat(m.path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > 2*time.Minute
}
