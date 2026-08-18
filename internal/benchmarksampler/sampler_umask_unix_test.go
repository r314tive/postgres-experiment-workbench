//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package benchmarksampler

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCreateReadyMarkerEstablishesExactModeIndependentOfUmask(t *testing.T) {
	for _, test := range []struct {
		name  string
		umask int
	}{
		{name: "permissive", umask: 0o000},
		{name: "restrictive", umask: 0o077},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, ReadyRelativePath)
			previous := syscall.Umask(test.umask)
			defer syscall.Umask(previous)

			if err := createReadyMarker(path); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
				t.Fatalf("readiness token mode = %s, want non-symlink directory 0700", info.Mode())
			}
		})
	}
}

func TestCreateReadyMarkerWithOwnerMaskIsExactOrFailsClosed(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, ReadyRelativePath)
	previous := syscall.Umask(0o777)
	defer syscall.Umask(previous)

	err := createReadyMarker(path)
	info, statErr := os.Lstat(path)
	if err == nil {
		if statErr != nil {
			t.Fatal(statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			t.Fatalf("successful owner-masked readiness token mode = %s, want directory 0700", info.Mode())
		}
		return
	}
	if !os.IsNotExist(statErr) {
		t.Fatalf("failed readiness publication left a token behind: %v", statErr)
	}
}

func TestCreateReadyMarkerDoesNotFollowExistingSymlink(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ReadyRelativePath)
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}

	if err := createReadyMarker(path); err == nil {
		t.Fatal("createReadyMarker accepted an existing symlink")
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "unchanged\n" {
		t.Fatalf("readiness symlink target changed: %q", content)
	}
}

func TestEstablishReadyMarkerModeDoesNotFollowRacedSymlink(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, ReadyRelativePath)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	marker, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer marker.Close()

	if err := establishReadyMarkerMode(path, created, marker); err == nil {
		t.Fatal("raced readiness symlink was accepted")
	}
	info, err := os.Lstat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("raced symlink target mode changed to %04o", info.Mode().Perm())
	}
}
