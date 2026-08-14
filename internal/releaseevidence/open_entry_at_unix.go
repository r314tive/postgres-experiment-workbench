//go:build darwin || linux

package releaseevidence

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openReadOnlyEntryAt(directory *os.File, name, description string) (*os.File, error) {
	if directory == nil {
		return nil, fmt.Errorf("pinned release evidence directory is required")
	}
	if err := validateDirectoryEntryName(name); err != nil {
		return nil, err
	}
	fd, err := openAt(int(directory.Fd()), name, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s must be a regular non-symlink file", description)
		}
		return nil, fmt.Errorf("open %s relative to pinned directory: %w", description, err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open %s returned an invalid descriptor", description)
	}
	return file, nil
}
