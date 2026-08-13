//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package benchmarksampler

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func terminateProcessGroup(cmd *exec.Cmd, wait <-chan error, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	termErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(termErr, syscall.ESRCH) {
		termErr = nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case waitErr := <-wait:
		return errors.Join(termErr, waitErr)
	case <-timer.C:
		killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(killErr, syscall.ESRCH) {
			killErr = nil
		}
		waitErr := <-wait
		return errors.Join(termErr, killErr, waitErr)
	}
}
