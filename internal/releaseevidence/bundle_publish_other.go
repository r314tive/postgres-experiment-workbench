//go:build !darwin && !linux

package releaseevidence

import (
	"fmt"
	"os"
)

func publishBundleArchiveAt(_ *os.File, _, _ string, _ []byte, _ BundleCreateResult, _ func() error) (BundleCreateResult, error) {
	return BundleCreateResult{}, fmt.Errorf("descriptor-relative evidence bundle publication is unsupported on this platform")
}
