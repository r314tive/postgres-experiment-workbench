//go:build !darwin && !linux

package releaseevidence

import "os"

func openDirectoryPath(path string) (*os.File, error) {
	return os.Open(path)
}
