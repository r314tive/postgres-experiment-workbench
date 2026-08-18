//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package benchmarkexternal

import "os/exec"

func configureProcessGroup(*exec.Cmd) error { return nil }

func killProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func closeProcessGroup(*exec.Cmd) (bool, error) { return false, nil }
