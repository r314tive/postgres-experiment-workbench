//go:build darwin || linux

package releaseevidence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAttachGateRejectsFIFOPredecessorWithoutBlocking(t *testing.T) {
	directory := t.TempDir()
	index := openIndex(RecordStatusActive)
	index.SchemaVersion = SchemaVersionV2
	index.Lineage = &Lineage{Revision: 0}
	indexPath := filepath.Join(directory, "index-r0.json")
	if err := syscall.Mkfifo(indexPath, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")
	assertAttachReturnsPromptly(t, func() error {
		_, err := AttachGate(GateAttachOptions{IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath, EvidenceRef: "urn:pgworkbench:evidence:fifo", Output: output})
		return err
	}, "regular non-symlink")
	assertNotExist(t, output)
}

func TestAttachGatePostCommitFIFOReplacementDoesNotBlock(t *testing.T) {
	directory := t.TempDir()
	indexPath, _, index := writeAttachIndex(t, directory, true)
	recordPath, _, _ := writeExternalDriverRecord(t, directory, externalDriverRecordFixture(index.Candidate), true)
	output := filepath.Join(directory, "index-r1.json")
	var result GateAttachResult
	var attachErr error
	var hookErr error
	assertAttachReturnsPromptly(t, func() error {
		result, attachErr = attachGate(GateAttachOptions{
			IndexPath: indexPath, Gate: "draft_external_drivers", EvidenceFile: recordPath,
			EvidenceRef: "urn:pgworkbench:evidence:fifo-swap", Output: output,
		}, gateAttachHooks{beforePublication: func() {
			hookErr = os.Remove(indexPath)
			if hookErr != nil {
				return
			}
			hookErr = syscall.Mkfifo(indexPath, 0o600)
		}})
		if hookErr != nil {
			return fmt.Errorf("replace predecessor with FIFO: %w", hookErr)
		}
		return attachErr
	}, "parsed and hashed inode")
	var committed *CommittedError
	if !errors.As(attachErr, &committed) || result.Digest == "" || committed.Result.Digest != result.Digest {
		t.Fatalf("FIFO swap result=%+v error=%v, want committed successor identity", result, attachErr)
	}
	if verification, err := VerifyFile(output); err != nil || !verification.Valid {
		t.Fatalf("committed successor = %+v, %v", verification, err)
	}
}

func TestAttachGateRejectsFIFODirectoryPathWithoutBlocking(t *testing.T) {
	container := t.TempDir()
	fifo := filepath.Join(container, "evidence")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	assertAttachReturnsPromptly(t, func() error {
		_, err := AttachGate(GateAttachOptions{
			IndexPath: filepath.Join(fifo, "index-r0.json"), Gate: "draft_external_drivers",
			EvidenceFile: filepath.Join(container, "record.json"), EvidenceRef: "urn:pgworkbench:evidence:fifo-dir",
			Output: filepath.Join(fifo, "index-r1.json"),
		})
		return err
	}, "open predecessor index directory")
}

func assertAttachReturnsPromptly(t *testing.T, operation func() error, want string) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("attachment error = %v, want %q", err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attachment blocked on a FIFO")
	}
}
