//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package benchmarksampler

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// The sampler command deliberately stays in the experiment runner's process
// group. scripts/psql.sh execs psql, so signalling the owned Process handle is
// sufficient here while the outer runner remains the final containment layer.
func terminateProcess(cmd *exec.Cmd, wait <-chan error, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	termErr := cmd.Process.Signal(syscall.SIGTERM)
	if errors.Is(termErr, syscall.ESRCH) || errors.Is(termErr, os.ErrProcessDone) {
		termErr = nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case waitErr := <-wait:
		return errors.Join(termErr, waitErr)
	case <-timer.C:
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, syscall.ESRCH) || errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		waitErr := <-wait
		return errors.Join(termErr, killErr, waitErr)
	}
}
