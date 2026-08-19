package releaseevidence

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/pathguard"
	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

func TestCreateBundleIsDeterministicRelocatableAndPreservesNoGo(t *testing.T) {
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusPassed)
	firstOutput := filepath.Join(t.TempDir(), "first.tar.gz")
	secondOutput := filepath.Join(t.TempDir(), "second.tar.gz")

	first, err := CreateBundle(head, firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range bundleIndexNames(first.HeadRevision) {
		if err := os.Chtimes(filepath.Join(chain, name), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	second, err := CreateBundle(head, secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("bundle is not reproducible: %s != %s", first.Digest, second.Digest)
	}
	if first.RootName != BundleRootName || first.Records != 2 || first.ArchiveFiles != 3 {
		t.Fatalf("unexpected bundle result: %+v", first)
	}
	if first.IndexVerification.Decision != DecisionNoGo || first.IndexVerification.AuthorizationEligible {
		t.Fatalf("bundle promoted an unqualified head: %+v", first.IndexVerification)
	}

	extracted := extractBundleForTest(t, firstOutput, filepath.Join(t.TempDir(), "deep", "relocated"))
	renamed := filepath.Join(filepath.Dir(extracted), "renamed-root")
	if err := os.Rename(extracted, renamed); err != nil {
		t.Fatal(err)
	}
	verification, err := VerifyBundle(renamed)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid || verification.HeadDigest != first.HeadDigest || verification.TreeDigest != first.TreeDigest {
		t.Fatalf("relocated bundle failed: %+v", verification)
	}
	if verification.IndexVerification.Status != StatusOpen || verification.IndexVerification.Decision != DecisionNoGo || verification.IndexVerification.AuthorizationEligible {
		t.Fatalf("relocated no-go outcome changed: %+v", verification.IndexVerification)
	}
	sourceHead, err := os.ReadFile(head)
	if err != nil {
		t.Fatal(err)
	}
	relocatedHead, err := os.ReadFile(filepath.Join(renamed, first.HeadIndex))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sourceHead, relocatedHead) {
		t.Fatal("bundle rewrote release evidence index bytes or durable references")
	}
}

func TestBundleAcceptsConsistentFailedNoGoHead(t *testing.T) {
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusFailed)
	output := filepath.Join(t.TempDir(), "failed.tar.gz")
	result, err := CreateBundle(head, output)
	if err != nil {
		t.Fatal(err)
	}
	root := extractBundleForTest(t, output, t.TempDir())
	verification, err := VerifyBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.Valid || verification.IndexVerification.Status != StatusFailed || verification.IndexVerification.Decision != DecisionNoGo || result.IndexVerification.Status != StatusFailed {
		t.Fatalf("failed no-go chain was not preserved: create=%+v verify=%+v", result.IndexVerification, verification)
	}
}

func TestCreateBundleRejectsUnsafePublicationAndSourceChanges(t *testing.T) {
	t.Run("existing output", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		if err := os.WriteFile(output, []byte("sentinel\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := CreateBundle(head, output)
		if !errors.Is(err, pathguard.ErrOutputExists) {
			t.Fatalf("CreateBundle existing output error = %v", err)
		}
		content, readErr := os.ReadFile(output)
		if readErr != nil || string(content) != "sentinel\n" {
			t.Fatalf("existing output changed: %q %v", content, readErr)
		}
	})

	t.Run("output inside source", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		_, err := CreateBundle(head, filepath.Join(chain, "bundle.tar.gz"))
		if !errors.Is(err, pathguard.ErrOutputWithinSource) {
			t.Fatalf("CreateBundle inside source error = %v", err)
		}
	})

	t.Run("output through source alias", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		alias := filepath.Join(t.TempDir(), "chain-alias")
		if err := os.Symlink(chain, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := CreateBundle(head, filepath.Join(alias, "bundle.tar.gz"))
		if !errors.Is(err, pathguard.ErrOutputWithinSource) {
			t.Fatalf("CreateBundle aliased source output error = %v", err)
		}
	})

	t.Run("source index has special mode bits", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		requireBundleSpecialModeBits(t, chain)
		if err := os.Chmod(filepath.Join(chain, "index-r0.json"), 0o644|os.ModeSetuid); err != nil {
			t.Fatal(err)
		}
		_, err := CreateBundle(head, filepath.Join(t.TempDir(), "bundle.tar.gz"))
		if err == nil || !strings.Contains(err.Error(), "mode 0644") {
			t.Fatalf("source special mode bits were accepted: %v", err)
		}
	})

	t.Run("explicit historical prefix", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		content, err := os.ReadFile(head)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chain, "index-r2.json"), content, 0o644); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		result, err := CreateBundle(head, output)
		if err != nil {
			t.Fatalf("explicit complete prefix was rejected: %v", err)
		}
		if result.HeadRevision != 1 || result.Records != 2 {
			t.Fatalf("historical prefix result = %+v", result)
		}
	})

	t.Run("source changes after snapshot", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{beforeSourceConfirm: func() error {
			return os.WriteFile(filepath.Join(chain, "index-r0.json"), []byte("{}\n"), 0o644)
		}})
		if err == nil || !strings.Contains(err.Error(), "source bytes changed") {
			t.Fatalf("source mutation was accepted: %v", err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("source mutation published output: %v", statErr)
		}
	})

	for _, mutation := range []struct {
		name string
		run  func(string) error
	}{
		{name: "source inode replaced after snapshot", run: func(path string) error {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(path, content, 0o644)
		}},
		{name: "source mode changes after snapshot", run: func(path string) error {
			return os.Chmod(path, 0o600)
		}},
		{name: "source gains hardlink after snapshot", run: func(path string) error {
			return os.Link(path, filepath.Join(t.TempDir(), "linked-index.json"))
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			chain := t.TempDir()
			head := createBundleTestChain(t, chain, GateStatusPassed)
			output := filepath.Join(t.TempDir(), "bundle.tar.gz")
			_, err := createBundle(head, output, bundleCreateHooks{beforeSourceConfirm: func() error {
				return mutation.run(filepath.Join(chain, "index-r0.json"))
			}})
			if err == nil || !strings.Contains(err.Error(), "source inode or mode changed") {
				t.Fatalf("source identity mutation was accepted: %v", err)
			}
			if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatalf("source identity mutation published output: %v", statErr)
			}
		})
	}

	t.Run("output parent redirects into source", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		container := t.TempDir()
		outputParent := filepath.Join(container, "output")
		if err := os.Mkdir(outputParent, 0o755); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(outputParent, "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{beforeSourceConfirm: func() error {
			if err := os.Rename(outputParent, filepath.Join(container, "moved")); err != nil {
				return err
			}
			return os.Symlink(chain, outputParent)
		}})
		if !errors.Is(err, pathguard.ErrOutputWithinSource) {
			t.Fatalf("redirected output parent error = %v", err)
		}
		if _, statErr := os.Lstat(filepath.Join(chain, "bundle.tar.gz")); !os.IsNotExist(statErr) {
			t.Fatalf("redirected output mutated source chain: %v", statErr)
		}
	})

	t.Run("staged tree changes before verification", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{beforeStageVerify: func(stage string) error {
			return os.WriteFile(filepath.Join(stage, "extra.json"), []byte("{}\n"), 0o644)
		}})
		if err == nil || !strings.Contains(err.Error(), "staged evidence bundle is invalid") {
			t.Fatalf("staged mutation was accepted: %v", err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("staged mutation published output: %v", statErr)
		}
	})

	t.Run("stage remains private", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{beforeStageVerify: func(stage string) error {
			info, err := os.Stat(stage)
			if err != nil {
				return err
			}
			if info.Mode().Perm() != 0o700 {
				return errors.New("bundle stage is not private 0700")
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("coherent staged rewrite after verification", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{afterStageVerify: func(stage string) error {
			path := filepath.Join(stage, "index-r1.json")
			index := readBundleTestIndex(t, path)
			note := "locally rewritten after semantic verification"
			index.Gates.DraftExternalDrivers.Note = &note
			writeCanonicalBundleTestIndex(t, path, index)
			refreshBundleTestInventory(t, stage)
			return nil
		}})
		if err == nil || !strings.Contains(err.Error(), "verify exact staged release evidence bundle archive") {
			t.Fatalf("post-verification staged rewrite was accepted: %v", err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("post-verification staged rewrite published output: %v", statErr)
		}
	})

	t.Run("stage cleanup failure precedes publication", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{removeStage: func(string) error {
			return errors.New("injected stage cleanup failure")
		}})
		if err == nil || !strings.Contains(err.Error(), "injected stage cleanup failure") {
			t.Fatalf("stage cleanup failure was hidden: %v", err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("stage cleanup failure published output: %v", statErr)
		}
	})

	t.Run("archive stage cleanup failure precedes publication", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		_, err := createBundle(head, output, bundleCreateHooks{removeArchiveStage: func(string) error {
			return errors.New("injected archive stage cleanup failure")
		}})
		if err == nil || !strings.Contains(err.Error(), "injected archive stage cleanup failure") {
			t.Fatalf("archive stage cleanup failure was hidden: %v", err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("archive stage cleanup failure published output: %v", statErr)
		}
	})

	t.Run("output parent redirects after pin", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		container := t.TempDir()
		outputParent := filepath.Join(container, "output")
		if err := os.Mkdir(outputParent, 0o755); err != nil {
			t.Fatal(err)
		}
		moved := filepath.Join(container, "moved")
		output := filepath.Join(outputParent, "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{beforeOutputPublish: func() error {
			if err := os.Rename(outputParent, moved); err != nil {
				return err
			}
			return os.Symlink(chain, outputParent)
		}})
		if err == nil || result.Digest != "" {
			t.Fatalf("redirected pinned output was not rejected before publication: result=%+v err=%v", result, err)
		}
		if _, statErr := os.Lstat(filepath.Join(chain, "bundle.tar.gz")); !os.IsNotExist(statErr) {
			t.Fatalf("redirected output mutated source chain: %v", statErr)
		}
		if _, statErr := os.Lstat(filepath.Join(moved, "bundle.tar.gz")); !os.IsNotExist(statErr) {
			t.Fatalf("redirected pinned destination received an archive: %v", statErr)
		}
	})

	t.Run("source changes before output publication", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{beforeOutputPublish: func() error {
			return os.WriteFile(filepath.Join(chain, "index-r0.json"), []byte("{}\n"), 0o644)
		}})
		if err == nil || result.Digest != "" || !strings.Contains(err.Error(), "source bytes changed") {
			t.Fatalf("pre-publication source mutation result=%+v err=%v", result, err)
		}
		if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
			t.Fatalf("pre-publication source mutation published output: %v", statErr)
		}
	})

	t.Run("original source is redirected into output before pin", func(t *testing.T) {
		chainContainer := t.TempDir()
		chain := filepath.Join(chainContainer, "chain")
		if err := os.Mkdir(chain, 0o755); err != nil {
			t.Fatal(err)
		}
		head := createBundleTestChain(t, chain, GateStatusPassed)
		container := t.TempDir()
		outputParent := filepath.Join(container, "output")
		if err := os.Mkdir(outputParent, 0o755); err != nil {
			t.Fatal(err)
		}
		movedOutput := filepath.Join(container, "moved-output")
		output := filepath.Join(outputParent, "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{beforeOutputPrepare: func() error {
			if err := os.Rename(outputParent, movedOutput); err != nil {
				return err
			}
			if err := os.Rename(chain, outputParent); err != nil {
				return err
			}
			return os.Mkdir(chain, 0o755)
		}})
		if err == nil || result.Digest != "" {
			t.Fatalf("source-to-output identity swap was accepted: result=%+v err=%v", result, err)
		}
		if _, statErr := os.Lstat(filepath.Join(outputParent, "bundle.tar.gz")); !os.IsNotExist(statErr) {
			t.Fatalf("source-to-output identity swap mutated the original source: %v", statErr)
		}
	})

	t.Run("published output directory moves into source", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		container := t.TempDir()
		outputParent := filepath.Join(container, "output")
		if err := os.Mkdir(outputParent, 0o755); err != nil {
			t.Fatal(err)
		}
		insideSource := filepath.Join(chain, "relocated-output")
		output := filepath.Join(outputParent, "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{afterOutputPublish: func() error {
			if err := os.Rename(outputParent, insideSource); err != nil {
				return err
			}
			return os.Symlink(insideSource, outputParent)
		}})
		var committed *BundleCommittedError
		if !errors.As(err, &committed) || result.Digest == "" || !errors.Is(err, pathguard.ErrOutputWithinSource) {
			t.Fatalf("post-publication containment move result=%+v err=%v", result, err)
		}
		if _, statErr := os.Stat(filepath.Join(insideSource, "bundle.tar.gz")); statErr != nil {
			t.Fatalf("committed archive identity was lost: %v", statErr)
		}
	})

	t.Run("published archive bytes change before terminal confirmation", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{afterOutputPublish: func() error {
			return os.WriteFile(output, []byte("replacement archive bytes\n"), 0o644)
		}})
		var committed *BundleCommittedError
		if !errors.As(err, &committed) || result.Digest == "" || !strings.Contains(err.Error(), "bytes differ from the verified staged payload") {
			t.Fatalf("post-publication archive replacement result=%+v err=%v", result, err)
		}
	})

	t.Run("source changes after output publication", func(t *testing.T) {
		chain := t.TempDir()
		head := createBundleTestChain(t, chain, GateStatusPassed)
		output := filepath.Join(t.TempDir(), "bundle.tar.gz")
		result, err := createBundle(head, output, bundleCreateHooks{afterOutputPublish: func() error {
			path := filepath.Join(chain, "index-r0.json")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(path, content, 0o644)
		}})
		var committed *BundleCommittedError
		if !errors.As(err, &committed) || result.Digest == "" || !strings.Contains(err.Error(), "source inode or mode changed") {
			t.Fatalf("post-publication source mutation result=%+v err=%v", result, err)
		}
		if info, statErr := os.Stat(output); statErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("committed archive identity was lost: info=%v err=%v", info, statErr)
		}
	})
}

func TestVerifyBundleRejectsTamperMatrix(t *testing.T) {
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusPassed)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(head, archive); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		issue string
		edit  func(*testing.T, string)
	}{
		{
			name:  "missing inventory",
			issue: "bundle inventory",
			edit: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, BundleInventoryName)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "missing revision",
			issue: "missing entry",
			edit: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "index-r0.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "extra file",
			issue: "unexpected entry",
			edit: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "extra.json"), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "extra directory",
			issue: "unexpected entry",
			edit: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "extra"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "index mode drift",
			issue: "mode, type, or hardlink",
			edit: func(t *testing.T, root string) {
				if err := os.Chmod(filepath.Join(root, "index-r1.json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "inventory mode drift",
			issue: "mode 0644",
			edit: func(t *testing.T, root string) {
				if err := os.Chmod(filepath.Join(root, BundleInventoryName), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "index special mode bits",
			issue: "mode, type, or hardlink",
			edit: func(t *testing.T, root string) {
				requireBundleSpecialModeBits(t, root)
				if err := os.Chmod(filepath.Join(root, "index-r1.json"), 0o644|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "inventory special mode bits",
			issue: "mode 0644",
			edit: func(t *testing.T, root string) {
				requireBundleSpecialModeBits(t, root)
				if err := os.Chmod(filepath.Join(root, BundleInventoryName), 0o644|os.ModeSetuid); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "index bytes changed",
			issue: "digest does not match",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "noncanonical index encoding with rebound inventory",
			issue: "not in canonical project encoding",
			edit: func(t *testing.T, root string) {
				index := readBundleTestIndex(t, filepath.Join(root, "index-r1.json"))
				content, err := json.Marshal(index)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "index-r1.json"), content, 0o644); err != nil {
					t.Fatal(err)
				}
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "broken predecessor with rebound inventory",
			issue: "predecessor digest does not match",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				wrong := "sha256:" + strings.Repeat("f", 64)
				index.Lineage.PreviousIndexDigest = &wrong
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "candidate transplant with rebound inventory",
			issue: "candidate identity differs",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Candidate.Version = "9.9.9"
				index.Candidate.Tag = "v9.9.9"
				index.Candidate.GitCommit = strings.Repeat("9", 40)
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "noncanonical revision zero note",
			issue: "exact canonical candidate initialization",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r0.json")
				index := readBundleTestIndex(t, path)
				note := "not emitted by candidate initialization"
				index.Gates.SourceCompatibility.Note = &note
				writeCanonicalBundleTestIndex(t, path, index)
				rebindBundleTestSuccessor(t, root)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "noncanonical revision zero control",
			issue: "exact canonical candidate initialization",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r0.json")
				index := readBundleTestIndex(t, path)
				index.PreventiveControls.ImmutableReleases.Enabled = boolPointer(true)
				writeCanonicalBundleTestIndex(t, path, index)
				rebindBundleTestSuccessor(t, root)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "noncanonical revision zero decision reasons",
			issue: "exact canonical candidate initialization",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r0.json")
				index := readBundleTestIndex(t, path)
				index.Decision.Reasons = []string{"arbitrary but individually valid reason"}
				writeCanonicalBundleTestIndex(t, path, index)
				rebindBundleTestSuccessor(t, root)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "gate transition adds note",
			issue: "exact registered gate attachment transition",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				note := "attachment CLI cannot add this note"
				index.Gates.DraftExternalDrivers.Note = &note
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "gate transition rewrites decision reasons",
			issue: "exact registered gate attachment transition",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Decision.Reasons = []string{"arbitrary but individually valid reason"}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "gate transition rewrites decision time",
			issue: "exact registered gate attachment transition",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Decision.RecordedAt = "2026-08-18T12:01:00Z"
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "pass-only adapter records failed status",
			issue: "illegally reopens, supersedes, or mutates gate",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Status = GateStatusFailed
				if err := finalizeDerivedDecision(&index, index.Gates.DraftExternalDrivers.Evidence.CapturedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "noncanonical attachment timestamp",
			issue: "captured_at is not canonical UTC",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Evidence.CapturedAt = "2026-08-18T17:00:01+05:00"
				if err := finalizeDerivedDecision(&index, index.Gates.DraftExternalDrivers.Evidence.CapturedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "workflow attachment omits run identity",
			issue: "retain its canonical run_id and run_attempt",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Evidence.RunID = nil
				index.Gates.DraftExternalDrivers.Evidence.RunAttempt = nil
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "attachment omits typed record and assurance",
			issue: "retain its typed record and assurance metadata",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Evidence.Record = nil
				index.Gates.DraftExternalDrivers.Evidence.Assurance = nil
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "attachment omits optional verifier adapter",
			issue: "retain the exact gate adapter identity",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Evidence.Record.Adapter = ""
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "aggregate attempt two precedes attempt one",
			issue: "requires an already attached registered attempt-one predecessor",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers = Gate{Status: GateStatusOpen}
				index.Gates.AggregateAttempt2 = Gate{Status: GateStatusPassed, Evidence: typedAggregateBundleEvidence(2)}
				if err := finalizeDerivedDecision(&index, index.Gates.AggregateAttempt2.Evidence.CapturedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "gate evidence predates candidate",
			issue: "captured_at precedes candidate initialization",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers.Evidence.CapturedAt = "2026-08-18T11:59:59Z"
				if err := finalizeDerivedDecision(&index, index.Gates.DraftExternalDrivers.Evidence.CapturedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "unregistered preventive controls transition",
			issue: "preventive control transitions are not registered",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers = Gate{Status: GateStatusOpen}
				evidence := &Evidence{Ref: "urn:pgworkbench:evidence:synthetic-controls", Digest: "sha256:" + strings.Repeat("e", 64), CapturedAt: "2026-08-18T12:00:01Z"}
				reviewer := "admin"
				reviewedAt := "2026-08-18T12:00:01Z"
				updatedAt := "2026-08-18T11:00:00Z"
				rulesetID := int64(1)
				index.PreventiveControls.TagRuleset.Status = ControlStatusVerified
				index.PreventiveControls.TagRuleset.APIEvidence = evidence
				index.PreventiveControls.TagRuleset.BypassReview = AdminReview{Status: ReviewStatusAdminReviewed, Reviewer: &reviewer, ReviewedAt: &reviewedAt, RulesetID: &rulesetID, RulesetUpdatedAt: &updatedAt, Evidence: evidence}
				index.PreventiveControls.ImmutableReleases = ImmutableReleases{Status: ControlStatusVerified, Enabled: boolPointer(true), APIEvidence: evidence}
				if err := finalizeDerivedDecision(&index, evidence.CapturedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "multiple gate transition with rebound inventory",
			issue: "must close exactly one",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.SourceCompatibility = Gate{Status: GateStatusPassed, Evidence: typedCompatibilityBundleEvidence()}
				if err := finalizeDerivedDecision(&index, "2026-08-18T12:00:02Z"); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "schema regression with rebound inventory",
			issue: "schema version regresses",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.SchemaVersion = SchemaVersionV2
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "no-op successor with rebound inventory",
			issue: "must close exactly one",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				index := readBundleTestIndex(t, path)
				index.Gates.DraftExternalDrivers = Gate{Status: GateStatusOpen}
				if err := finalizeDerivedDecision(&index, index.Decision.RecordedAt); err != nil {
					t.Fatal(err)
				}
				writeCanonicalBundleTestIndex(t, path, index)
				refreshBundleTestInventory(t, root)
			},
		},
		{
			name:  "duplicate inventory row",
			issue: "canonical revision",
			edit: func(t *testing.T, root string) {
				inventory := readBundleTestInventory(t, root)
				inventory.Files = append(inventory.Files, inventory.Files[0])
				inventory.FileCount = int64(len(inventory.Files))
				inventory.TreeDigest, _ = bundleTreeDigest(inventory.Files)
				writeBundleTestInventory(t, root, inventory)
			},
		},
		{
			name:  "unknown inventory field",
			issue: "unknown field",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, BundleInventoryName)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				trimmed := bytes.TrimSpace(content)
				trimmed = append(trimmed[:len(trimmed)-1], []byte(",\"unknown\":true}\n")...)
				if err := os.WriteFile(path, trimmed, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "index symlink",
			issue: "regular non-symlink",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("index-r0.json", path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name:  "inventory symlink",
			issue: "regular non-symlink",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, BundleInventoryName)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(root, "inventory-target.json")
				if err := os.WriteFile(target, content, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(target), path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name:  "hardlinked index",
			issue: "hardlink",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, "index-r1.json")
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external-index.json")
				if err := os.WriteFile(external, content, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, path); err != nil {
					t.Skipf("hardlink unavailable: %v", err)
				}
			},
		},
		{
			name:  "hardlinked inventory",
			issue: "non-hardlinked",
			edit: func(t *testing.T, root string) {
				path := filepath.Join(root, BundleInventoryName)
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(t.TempDir(), "external-inventory.json")
				if err := os.WriteFile(external, content, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, path); err != nil {
					t.Skipf("hardlink unavailable: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := extractBundleForTest(t, archive, t.TempDir())
			test.edit(t, root)
			verification, err := VerifyBundle(root)
			if err != nil {
				t.Fatal(err)
			}
			if verification.Valid || !sliceContainsSubstring(verification.Issues, test.issue) {
				t.Fatalf("tamper accepted or wrong issue; want %q: %+v", test.issue, verification)
			}
		})
	}
}

func TestVerifyBundleDetectsEntrySwapBeforeReportingSuccess(t *testing.T) {
	chain := t.TempDir()
	head := createBundleTestChain(t, chain, GateStatusPassed)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(head, archive); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"index-r1.json", BundleInventoryName} {
		t.Run(name, func(t *testing.T) {
			root := extractBundleForTest(t, archive, t.TempDir())
			verification, err := verifyBundle(root, bundleVerifyHooks{beforeFinalConfirm: func() error {
				path := filepath.Join(root, name)
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if err := os.Remove(path); err != nil {
					return err
				}
				return os.WriteFile(path, content, 0o644)
			}})
			if err != nil {
				t.Fatal(err)
			}
			if verification.Valid || !sliceContainsSubstring(verification.Issues, "no longer identifies the verified inode") {
				t.Fatalf("entry swap was accepted: %+v", verification)
			}
		})
	}

	for _, entry := range []struct {
		name string
		add  func(string) error
	}{
		{name: "late extra file", add: func(root string) error {
			return os.WriteFile(filepath.Join(root, "late-extra.json"), []byte("{}\n"), 0o644)
		}},
		{name: "late extra directory", add: func(root string) error {
			return os.Mkdir(filepath.Join(root, "late-extra"), 0o755)
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			root := extractBundleForTest(t, archive, t.TempDir())
			verification, err := verifyBundle(root, bundleVerifyHooks{beforeFinalConfirm: func() error {
				return entry.add(root)
			}})
			if err != nil {
				t.Fatal(err)
			}
			if verification.Valid || !sliceContainsSubstring(verification.Issues, "unexpected entry after verification") {
				t.Fatalf("late entry was accepted: %+v", verification)
			}
		})
	}

	t.Run("entry added after inode confirmations", func(t *testing.T) {
		root := extractBundleForTest(t, archive, t.TempDir())
		verification, err := verifyBundle(root, bundleVerifyHooks{afterEntryConfirm: func() error {
			return os.WriteFile(filepath.Join(root, "terminal-extra.json"), []byte("{}\n"), 0o644)
		}})
		if err != nil {
			t.Fatal(err)
		}
		if verification.Valid || !sliceContainsSubstring(verification.Issues, "unexpected entry at terminal confirmation") {
			t.Fatalf("terminal entry addition was accepted: %+v", verification)
		}
	})

	t.Run("entry replaced after first inode confirmations", func(t *testing.T) {
		root := extractBundleForTest(t, archive, t.TempDir())
		verification, err := verifyBundle(root, bundleVerifyHooks{afterEntryConfirm: func() error {
			path := filepath.Join(root, "index-r1.json")
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.WriteFile(path, content, 0o644)
		}})
		if err != nil {
			t.Fatal(err)
		}
		if verification.Valid || !sliceContainsSubstring(verification.Issues, "no longer identifies the verified inode") {
			t.Fatalf("terminal entry replacement was accepted: %+v", verification)
		}
	})

	t.Run("root directory", func(t *testing.T) {
		root := extractBundleForTest(t, archive, t.TempDir())
		verification, err := verifyBundle(root, bundleVerifyHooks{beforeFinalConfirm: func() error {
			if err := os.Rename(root, root+"-moved"); err != nil {
				return err
			}
			return os.Mkdir(root, 0o755)
		}})
		if err != nil {
			t.Fatal(err)
		}
		if verification.Valid || !sliceContainsSubstring(verification.Issues, "path no longer identifies the pinned") {
			t.Fatalf("root swap was accepted: %+v", verification)
		}
	})

	t.Run("root becomes symlink to original inode", func(t *testing.T) {
		root := extractBundleForTest(t, archive, t.TempDir())
		moved := root + "-moved"
		verification, err := verifyBundle(root, bundleVerifyHooks{beforeFinalConfirm: func() error {
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			return os.Symlink(moved, root)
		}})
		if err != nil {
			t.Fatal(err)
		}
		if verification.Valid || !sliceContainsSubstring(verification.Issues, "real non-symlink directory") {
			t.Fatalf("symlinked root alias was accepted: %+v", verification)
		}
	})
}

func createBundleTestChain(t *testing.T, directory, gateStatus string) string {
	t.Helper()
	candidate := Candidate{
		Version:          "0.2.6",
		Tag:              "v0.2.6",
		GitCommit:        strings.Repeat("a", 40),
		AssetFingerprint: strings.Repeat("b", 64),
		ScenarioPack: ScenarioPack{
			ID:      "builtin",
			Version: "0.2.6",
			Digest:  "sha256:" + strings.Repeat("c", 64),
		},
	}
	index, err := NewIndex(candidate, "2026-08-18T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	r0 := filepath.Join(directory, "index-r0.json")
	written, err := WriteNew(r0, index)
	if err != nil {
		t.Fatal(err)
	}
	r1Index := index
	r1Index.Lineage = &Lineage{Revision: 1, PreviousIndexDigest: &written.Digest}
	if gateStatus == GateStatusFailed {
		r1Index.Gates.CriticalFindingReview = Gate{Status: gateStatus, Evidence: typedCriticalReviewBundleEvidence()}
	} else {
		evidence := typedExternalDriverEvidence("urn:pgworkbench:evidence:bundle-test")
		evidence.CapturedAt = "2026-08-18T12:00:01Z"
		runID := "987654321"
		runAttempt := int64(1)
		evidence.RunID = &runID
		evidence.RunAttempt = &runAttempt
		evidence.Record.Adapter = ExternalDriverVerificationAdapter
		r1Index.Gates.DraftExternalDrivers = Gate{Status: gateStatus, Evidence: evidence}
	}
	if err := finalizeDerivedDecision(&r1Index, "2026-08-18T12:00:01Z"); err != nil {
		t.Fatal(err)
	}
	r1 := filepath.Join(directory, "index-r1.json")
	if _, err := WriteNew(r1, r1Index); err != nil {
		t.Fatal(err)
	}
	return r1
}

func typedCompatibilityBundleEvidence() *Evidence {
	return &Evidence{
		Ref:        "urn:pgworkbench:evidence:bundle-compatibility-test",
		Digest:     "sha256:" + strings.Repeat("d", 64),
		CapturedAt: "2026-08-18T12:00:02Z",
		Record: &EvidenceRecord{
			SchemaVersion: CompatibilityVerificationSchema,
			ArtifactType:  CompatibilityVerificationType,
			Adapter:       CompatibilitySourceAdapter,
		},
		Assurance: &EvidenceAssurance{
			Durability:   EvidenceDurabilityAsserted,
			Authenticity: EvidenceAuthenticityUnverified,
		},
	}
}

func typedCriticalReviewBundleEvidence() *Evidence {
	return &Evidence{
		Ref:        "urn:pgworkbench:evidence:bundle-critical-review-test",
		Digest:     "sha256:" + strings.Repeat("e", 64),
		CapturedAt: "2026-08-18T12:00:01Z",
		Record: &EvidenceRecord{
			SchemaVersion: CriticalFindingReviewSchema,
			ArtifactType:  CriticalFindingReviewType,
			Adapter:       CriticalFindingReviewAdapter,
		},
		Assurance: &EvidenceAssurance{Durability: EvidenceDurabilityAsserted, Authenticity: EvidenceAuthenticityUnverified},
	}
}

func typedAggregateBundleEvidence(attempt int) *Evidence {
	adapter := AggregateAttempt1Adapter
	if attempt == 2 {
		adapter = AggregateAttempt2Adapter
	}
	runID := "987654321"
	runAttempt := int64(2)
	return &Evidence{
		Ref:        "urn:pgworkbench:evidence:bundle-aggregate-test",
		Digest:     "sha256:" + strings.Repeat("f", 64),
		CapturedAt: "2026-08-18T12:00:01Z",
		RunID:      &runID,
		RunAttempt: &runAttempt,
		Record: &EvidenceRecord{
			SchemaVersion: AggregateVerificationSchema,
			ArtifactType:  AggregateVerificationType,
			Adapter:       adapter,
		},
		Assurance: &EvidenceAssurance{Durability: EvidenceDurabilityAsserted, Authenticity: EvidenceAuthenticityUnverified},
	}
}

func extractBundleForTest(t *testing.T, archivePath, destination string) string {
	t.Helper()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	root := ""
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("unsafe archive entry: %s", header.Name)
		}
		parts := strings.Split(filepath.ToSlash(clean), "/")
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root {
			t.Fatalf("archive contains multiple roots: %s", header.Name)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(file, tarReader)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				t.Fatal(errors.Join(copyErr, closeErr))
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unsupported archive entry type %d", header.Typeflag)
		}
	}
	if root == "" {
		t.Fatal("archive is empty")
	}
	return filepath.Join(destination, root)
}

func readBundleTestInventory(t *testing.T, root string) BundleInventory {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, BundleInventoryName))
	if err != nil {
		t.Fatal(err)
	}
	var inventory BundleInventory
	if err := strictjson.Parse(content, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func writeBundleTestInventory(t *testing.T, root string, inventory BundleInventory) {
	t.Helper()
	content, err := encodeBundleInventory(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, BundleInventoryName), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func refreshBundleTestInventory(t *testing.T, root string) {
	t.Helper()
	inventory := readBundleTestInventory(t, root)
	var total int64
	for index := range inventory.Files {
		path := filepath.Join(root, inventory.Files[index].Path)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		inventory.Files[index].Size = int64(len(content))
		inventory.Files[index].Digest = digestExactBytes(content)
		inventory.Files[index].Mode = uint32(info.Mode().Perm())
		total += int64(len(content))
	}
	inventory.FileCount = int64(len(inventory.Files))
	inventory.TotalSizeBytes = total
	inventory.TreeDigest, _ = bundleTreeDigest(inventory.Files)
	head := readBundleTestIndex(t, filepath.Join(root, inventory.HeadIndex))
	verification := Verify(head)
	inventory.Candidate = head.Candidate
	inventory.HeadDigest = inventory.Files[len(inventory.Files)-1].Digest
	inventory.Outcome = outcomeFromVerification(verification)
	writeBundleTestInventory(t, root, inventory)
}

func rebindBundleTestSuccessor(t *testing.T, root string) {
	t.Helper()
	r0, err := os.ReadFile(filepath.Join(root, "index-r0.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index-r1.json")
	index := readBundleTestIndex(t, path)
	digest := digestExactBytes(r0)
	index.Lineage.PreviousIndexDigest = &digest
	writeCanonicalBundleTestIndex(t, path, index)
}

func readBundleTestIndex(t *testing.T, path string) Index {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	index, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func writeCanonicalBundleTestIndex(t *testing.T, path string, index Index) {
	t.Helper()
	content, err := encodeIndex(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireBundleSpecialModeBits(t *testing.T, directory string) {
	t.Helper()
	probe := filepath.Join(directory, ".special-mode-probe")
	if err := os.WriteFile(probe, []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(probe) })
	if err := os.Chmod(probe, 0o644|os.ModeSetuid); err != nil {
		t.Skipf("filesystem does not permit setuid mode probes: %v", err)
	}
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetuid == 0 {
		t.Skip("filesystem does not retain setuid mode bits")
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove special-mode probe: %v", err)
	}
}
