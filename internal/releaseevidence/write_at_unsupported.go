//go:build !darwin && !linux

package releaseevidence

import (
	"fmt"
	"os"
	"runtime"
)

// WriteNewAt is unavailable where this package has no audited openat/linkat/
// unlinkat implementation.
func WriteNewAt(_ *os.File, _, _ string, _ Index) (WriteResult, error) {
	return WriteResult{}, fmt.Errorf("pinned release evidence publication is unsupported on %s", runtime.GOOS)
}
