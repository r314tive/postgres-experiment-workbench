package benchmarkrun

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

const EngineBinarySeriesRef = "protocol/engine/pgworkbench"

type engineBinaryIdentity struct {
	Path   string
	Size   int64
	Digest string
}

func inspectEngineBinary(path string) (engineBinaryIdentity, error) {
	if path == "" {
		resolved, err := os.Executable()
		if err != nil {
			return engineBinaryIdentity{}, fmt.Errorf("resolve benchmark engine executable: %w", err)
		}
		path = resolved
	}
	if !filepath.IsAbs(path) {
		return engineBinaryIdentity{}, fmt.Errorf("benchmark engine binary must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	if filepath.Clean(resolved) != resolved || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return engineBinaryIdentity{}, fmt.Errorf("benchmark engine must resolve to a clean executable regular non-symlink file")
	}
	digest, err := evidence.DigestFile(resolved)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	identity := engineBinaryIdentity{Path: resolved, Size: info.Size(), Digest: digest}
	if err := revalidateEngineBinary(identity); err != nil {
		return engineBinaryIdentity{}, err
	}
	return identity, nil
}

func revalidateEngineBinary(identity engineBinaryIdentity) error {
	info, err := os.Lstat(identity.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 || info.Size() != identity.Size {
		return fmt.Errorf("benchmark engine binary changed type, mode, or size")
	}
	digest, err := evidence.DigestFile(identity.Path)
	if err != nil || digest != identity.Digest {
		return fmt.Errorf("benchmark engine binary digest changed")
	}
	return nil
}

func snapshotEngineBinary(identity engineBinaryIdentity, destination string) error {
	content, err := os.ReadFile(identity.Path)
	if err != nil {
		return err
	}
	if int64(len(content)) != identity.Size || evidence.DigestBytes(content) != identity.Digest {
		return fmt.Errorf("benchmark engine binary changed while being read")
	}
	if err := writeFileAtomic(destination, content, 0o644); err != nil {
		return err
	}
	return revalidateEngineBinary(identity)
}
