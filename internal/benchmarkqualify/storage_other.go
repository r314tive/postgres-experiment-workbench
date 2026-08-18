//go:build !linux && !darwin

package benchmarkqualify

import "fmt"

func collectStorage(string) (storageResult, error) {
	return storageResult{}, fmt.Errorf("storage inspection is unsupported on this platform")
}
