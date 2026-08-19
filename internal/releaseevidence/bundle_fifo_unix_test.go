//go:build darwin || linux

package releaseevidence

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBundleRejectsFIFOWithoutBlocking(t *testing.T) {
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusPassed)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(head, archive); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"index-r1.json", BundleInventoryName} {
		t.Run("verify "+name, func(t *testing.T) {
			root := extractBundleForTest(t, archive, t.TempDir())
			path := filepath.Join(root, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Skipf("FIFO unavailable: %v", err)
			}
			done := make(chan error, 1)
			go func() {
				verification, err := VerifyBundle(root)
				if err == nil && (verification.Valid || !sliceContainsSubstring(verification.Issues, "regular")) {
					err = &bundleFIFOTestError{issues: verification.Issues}
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("VerifyBundle blocked on FIFO")
			}
		})
	}

	t.Run("create", func(t *testing.T) {
		path := filepath.Join(chain, "index-r1.json")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(path, 0o644); err != nil {
			t.Skipf("FIFO unavailable: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := CreateBundle(head, filepath.Join(t.TempDir(), "fifo.tar.gz"))
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "regular") {
				t.Fatalf("CreateBundle FIFO error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("CreateBundle blocked on FIFO")
		}
	})
}

type bundleFIFOTestError struct {
	issues []string
}

func (err *bundleFIFOTestError) Error() string {
	return "FIFO bundle was accepted: " + strings.Join(err.issues, "; ")
}
