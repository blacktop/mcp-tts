//go:build !windows

package speechlock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirRejectsSharedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(dir); err == nil {
		t.Fatal("ensurePrivateDir accepted a group/world-readable directory")
	}
}

func TestEnsurePrivateDirRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(link); err == nil {
		t.Fatal("ensurePrivateDir accepted a symlink")
	}
}

func TestAcquireRejectsSymlinkedLockFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, lockFileName)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if lock, err := Acquire(context.Background(), path, "test"); err == nil {
		lock.Release()
		t.Fatal("Acquire followed a symlinked lock file")
	}
}

func TestAcquireRejectsSharedLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFileName)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if lock, err := Acquire(context.Background(), path, "test"); err == nil {
		lock.Release()
		t.Fatal("Acquire accepted a world-writable lock file")
	}
}
