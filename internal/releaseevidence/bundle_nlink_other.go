//go:build !darwin && !linux

package releaseevidence

import "os"

func bundleFileHasMultipleLinks(_ os.FileInfo) bool {
	return false
}
