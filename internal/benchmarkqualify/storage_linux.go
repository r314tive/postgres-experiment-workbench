//go:build linux

package benchmarkqualify

import (
	"fmt"
	"syscall"
)

func collectStorage(path string) (storageResult, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return storageResult{}, err
	}
	blockSize := uint64(stat.Bsize)
	if blockSize == 0 || stat.Blocks > ^uint64(0)/blockSize || stat.Bavail > ^uint64(0)/blockSize {
		return storageResult{}, fmt.Errorf("invalid filesystem capacity")
	}
	return storageResult{
		filesystem:     linuxFilesystem(uint64(stat.Type)),
		totalBytes:     stat.Blocks * blockSize,
		availableBytes: stat.Bavail * blockSize,
	}, nil
}

func linuxFilesystem(kind uint64) string {
	switch kind {
	case 0xEF53:
		return "ext"
	case 0x58465342:
		return "xfs"
	case 0x9123683E:
		return "btrfs"
	case 0x01021994:
		return "tmpfs"
	case 0x794C7630:
		return "overlayfs"
	case 0x6969:
		return "nfs"
	default:
		return fmt.Sprintf("linux-0x%x", kind)
	}
}
