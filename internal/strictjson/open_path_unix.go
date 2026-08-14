//go:build darwin || linux

package strictjson

import (
	"os"
	"syscall"
)

func openReadOnlyPath(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
