//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package experimentrun

import (
	"fmt"
	"os/exec"
	"runtime"
)

type processSignal int

const (
	processSignalTerminate processSignal = iota
	processSignalKill
)

func configureProcessGroup(_ *exec.Cmd) error {
	return fmt.Errorf("fail-closed descendant process-group execution is unsupported on %s", runtime.GOOS)
}

func signalProcessGroup(cmd *exec.Cmd, signal processSignal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = signal
	return fmt.Errorf("fail-closed descendant process-group termination is unsupported on %s", runtime.GOOS)
}

func processGroupStatus(_ *exec.Cmd) (bool, error) {
	return false, fmt.Errorf("process-group status is unsupported on %s", runtime.GOOS)
}
