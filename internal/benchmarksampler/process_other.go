//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package benchmarksampler

import (
	"os/exec"
	"time"
)

func terminateProcess(cmd *exec.Cmd, wait <-chan error, _ time.Duration) error {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return <-wait
}
