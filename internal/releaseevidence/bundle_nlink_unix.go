//go:build darwin || linux

package releaseevidence

import (
	"os"
	"syscall"
)

func bundleFileHasMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || uint64(stat.Nlink) != 1
}
