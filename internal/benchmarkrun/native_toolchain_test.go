package benchmarkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/r314tive/postgres-experiment-workbench/internal/benchmarkplan"
	"github.com/r314tive/postgres-experiment-workbench/internal/nativetoolchain"
	"github.com/r314tive/postgres-experiment-workbench/internal/speccatalog"
)

func TestNativePGConfigSeriesSnapshotsAndRevalidatesToolchain(t *testing.T) {
	root := writeRunCatalog(t, 1, true)
	plan, err := benchmarkplan.Build(speccatalog.New(root), "test")
	if err != nil {
		t.Fatal(err)
	}
	bindir := fakeRunnerToolchain(t, "ordinary-pg-config")
	execution, err := Start(root, speccatalog.New(root), plan, Options{
		Runtime: "native", RunID: "native-pg-config-identity", NativeBindir: bindir,
		Getenv: func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	options := execution.options
	if options.SubjectDimension != "" || !strings.HasSuffix(options.NativeToolchainManifestRef, "/"+NativeToolchainSeriesRef) {
		t.Fatalf("ordinary native series did not receive a series-local toolchain identity: %#v", options)
	}
	manifestPath, err := nativeToolchainManifestPath(root, options.NativeToolchainManifestRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nativetoolchain.VerifySnapshot(filepath.Dir(manifestPath), options.NativeToolchainDigest); err != nil {
		t.Fatalf("ordinary native series snapshot does not verify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bindir, "pgbench"), []byte("#!/bin/sh\necho mutated\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeToolchainOptions("native", options); err == nil || !strings.Contains(err.Error(), "differs from the bound protocol") {
		t.Fatalf("ordinary pg_config native toolchain mutation passed revalidation: %v", err)
	}
}

func TestNativeToolchainOptionsRevalidateBytesAndBindExecutionDigest(t *testing.T) {
	bindir := fakeRunnerToolchain(t, "bound")
	installation, err := nativetoolchain.Inspect(bindir)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{
		SubjectDimension:           "native_toolchain",
		NativeBindir:               bindir,
		NativeToolchainDigest:      installation.Manifest.Digest,
		NativeToolchainManifestRef: "runs/benchmark-ab/example/toolchains/baseline/manifest.json",
	}
	if err := validateNativeToolchainOptions("native", options); err != nil {
		t.Fatal(err)
	}
	plan := benchmarkplan.Plan{PGConfig: "default"}
	plain := ExpectedExecutionParametersDigest(plan, "native", 1)
	bound := ExpectedExecutionParametersDigestWithToolchain(plan, "native", 1, installation.Manifest.Digest)
	if plain == bound {
		t.Fatal("native toolchain byte identity did not enter execution-parameters digest")
	}
	if err := os.WriteFile(filepath.Join(bindir, "pgbench"), []byte("#!/bin/sh\necho mutated\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateNativeToolchainOptions("native", options); err == nil || !strings.Contains(err.Error(), "differs from the bound protocol") {
		t.Fatalf("mutated toolchain passed pre-trial revalidation: %v", err)
	}
}

func TestNativeToolchainOptionsRejectRuntimeAndCrossArmDigestSwap(t *testing.T) {
	left, err := nativetoolchain.Inspect(fakeRunnerToolchain(t, "left"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := nativetoolchain.Inspect(fakeRunnerToolchain(t, "right"))
	if err != nil {
		t.Fatal(err)
	}
	options := Options{
		SubjectDimension:           "native_toolchain",
		NativeBindir:               left.Bindir,
		NativeToolchainDigest:      right.Manifest.Digest,
		NativeToolchainManifestRef: "runs/benchmark-ab/example/toolchains/baseline/manifest.json",
	}
	if err := validateNativeToolchainOptions("native", options); err == nil || !strings.Contains(err.Error(), "differs from the bound protocol") {
		t.Fatalf("cross-arm digest swap passed: %v", err)
	}
	options.NativeToolchainDigest = left.Manifest.Digest
	if err := validateNativeToolchainOptions("docker", options); err == nil || !strings.Contains(err.Error(), "requires native runtime") {
		t.Fatalf("Docker native_toolchain subject passed: %v", err)
	}
}

func TestDockerPGConfigRejectsNativeToolchainIdentity(t *testing.T) {
	installation, err := nativetoolchain.Inspect(fakeRunnerToolchain(t, "docker-rejected"))
	if err != nil {
		t.Fatal(err)
	}
	options := Options{
		SubjectDimension: "pg_config", NativeBindir: installation.Bindir,
		NativeToolchainDigest:      installation.Manifest.Digest,
		NativeToolchainManifestRef: "runs/benchmarks/example/protocol/native-toolchain/manifest.json",
	}
	if err := validateNativeToolchainOptions("docker", options); err == nil || !strings.Contains(err.Error(), "rejects native toolchain identity") {
		t.Fatalf("Docker pg_config accepted native byte identity: %v", err)
	}
}

func fakeRunnerToolchain(t *testing.T, identity string) string {
	t.Helper()
	bindir := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bindir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range nativetoolchain.RequiredExecutableNames() {
		content := "#!/bin/sh\necho '" + name + " (PostgreSQL) " + identity + "'\n"
		if err := os.WriteFile(filepath.Join(bindir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bindir
}
