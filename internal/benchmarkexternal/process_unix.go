//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package benchmarkexternal

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureProcessGroup(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func killProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func closeProcessGroup(command *exec.Cmd) (bool, error) {
	if command == nil || command.Process == nil {
		return false, nil
	}
	err := syscall.Kill(-command.Process.Pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false, err
	}
	return true, killProcessGroup(command)
}
