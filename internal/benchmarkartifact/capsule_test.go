package benchmarkartifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRejectsImmutableProtocolCapsuleTampering(t *testing.T) {
	tests := []struct {
		name string
		edit func(*testing.T, string)
		want string
	}{
		{
			name: "digest mismatch",
			edit: func(t *testing.T, seriesDir string) {
				writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "capsule", "workloads", "pgbench", "tiny.env"), "WORKLOAD_KIND=pgbench\n# mutated\n")
			},
			want: "immutable protocol capsule digest mismatch",
		},
		{
			name: "unexpected executable input",
			edit: func(t *testing.T, seriesDir string) {
				writeArtifactFile(t, filepath.Join(seriesDir, "protocol", "capsule", "workloads", "extra.env"), "WORKLOAD_KIND=pgbench\n")
			},
			want: "immutable protocol capsule contains unexpected file",
		},
		{
			name: "symlinked input",
			edit: func(t *testing.T, seriesDir string) {
				path := filepath.Join(seriesDir, "protocol", "capsule", "workloads", "pgbench", "tiny.env")
				target := filepath.Join(seriesDir, "protocol", "workload-spec.env")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
			want: "immutable protocol capsule contains symlink",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, seriesDir, _, _, _ := writeArtifactFixture(t)
			test.edit(t, seriesDir)
			verification, err := Verify(root, seriesDir)
			if err != nil {
				t.Fatal(err)
			}
			if verification.IsValid() || !containsCapsuleIssue(verification.Issues, test.want) {
				t.Fatalf("capsule tamper verified; want %q in %v", test.want, verification.Issues)
			}
		})
	}
}

func containsCapsuleIssue(issues []string, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, fragment) {
			return true
		}
	}
	return false
}
