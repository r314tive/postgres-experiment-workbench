//go:build darwin || linux

package releaseevidence

import (
	"fmt"
	"os"
	"syscall"
)

func openDirectoryPath(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(fd), path)
	if directory == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("open directory returned an invalid descriptor")
	}
	return directory, nil
}
