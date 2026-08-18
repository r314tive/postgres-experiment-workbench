//go:build !darwin && !linux

package releaseevidence

import (
	"fmt"
	"os"
	"runtime"
)

func openReadOnlyEntryAt(_ *os.File, _, _ string) (*os.File, error) {
	return nil, fmt.Errorf("pinned release evidence reads are unsupported on %s", runtime.GOOS)
}
