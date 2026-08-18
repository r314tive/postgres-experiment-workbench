//go:build darwin || linux

package strictjson

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadFileRejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(path, 1024)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("ReadFile(FIFO) error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadFile blocked on a FIFO")
	}
}

func TestNoFollowPathOpenItselfDoesNotBlockOnFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swapped-record.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		file, err := openReadOnlyPath(path)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no-follow path open blocked on a FIFO")
	}
}
