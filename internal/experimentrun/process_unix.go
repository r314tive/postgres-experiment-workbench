//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package experimentrun

import (
	"errors"
	"os/exec"
	"syscall"
)

type processSignal syscall.Signal

const (
	processSignalTerminate = processSignal(syscall.SIGTERM)
	processSignalKill      = processSignal(syscall.SIGKILL)
)

func configureProcessGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func signalProcessGroup(cmd *exec.Cmd, signal processSignal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.Signal(signal))
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func processGroupStatus(cmd *exec.Cmd) (bool, error) {
	if cmd == nil || cmd.Process == nil {
		return false, nil
	}
	err := syscall.Kill(-cmd.Process.Pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
