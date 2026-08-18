//go:build !darwin && !linux

package strictjson

import "os"

func openReadOnlyPath(path string) (*os.File, error) {
	return os.Open(path)
}
