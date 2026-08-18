//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package experimentrun

import (
	"errors"
	"fmt"
	"os"
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

func processInterruptSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM}
}

func processInterruptSignalName(received os.Signal) string {
	switch received {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return fmt.Sprintf("signal(%v)", received)
	}
}

func processInterruptExitCode(received os.Signal) int {
	switch received {
	case syscall.SIGHUP:
		return 129
	case syscall.SIGINT:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 128
	}
}
