package operationbench

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

type engineBinaryIdentity struct {
	Path   string
	Size   int64
	Digest string
}

func inspectEngineBinary(path string) (engineBinaryIdentity, error) {
	if path == "" || !filepath.IsAbs(path) {
		return engineBinaryIdentity{}, fmt.Errorf("engine binary must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	if filepath.Clean(resolved) != resolved {
		return engineBinaryIdentity{}, fmt.Errorf("engine binary path is not clean")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return engineBinaryIdentity{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return engineBinaryIdentity{}, fmt.Errorf("engine binary must resolve to an executable regular non-symlink file")
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
		return fmt.Errorf("engine binary changed type, mode, or size")
	}
	digest, err := evidence.DigestFile(identity.Path)
	if err != nil || digest != identity.Digest {
		return fmt.Errorf("engine binary digest changed")
	}
	return nil
}

func snapshotEngineBinary(identity engineBinaryIdentity, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := snapshotFile(identity.Path, destination); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != identity.Size {
		return fmt.Errorf("engine binary snapshot is missing or unsafe")
	}
	digest, err := evidence.DigestFile(destination)
	if err != nil || digest != identity.Digest {
		return fmt.Errorf("engine binary snapshot digest mismatch")
	}
	return revalidateEngineBinary(identity)
}
