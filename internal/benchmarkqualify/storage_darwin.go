//go:build darwin

package benchmarkqualify

import (
	"fmt"
	"strings"
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
	filesystem := strings.TrimRight(int8ArrayString(stat.Fstypename[:]), "\x00")
	if !portableToken(filesystem) {
		filesystem = "darwin-unknown"
	}
	return storageResult{
		filesystem:     filesystem,
		totalBytes:     stat.Blocks * blockSize,
		availableBytes: stat.Bavail * blockSize,
	}, nil
}

func int8ArrayString(value []int8) string {
	bytes := make([]byte, 0, len(value))
	for _, item := range value {
		if item == 0 {
			break
		}
		bytes = append(bytes, byte(item))
	}
	return string(bytes)
}
