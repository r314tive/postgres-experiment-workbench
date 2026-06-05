package workloadbg

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Status struct {
	State       string   `json:"state"`
	PID         int      `json:"pid,omitempty"`
	Command     string   `json:"command,omitempty"`
	Log         string   `json:"log,omitempty"`
	LogExists   bool     `json:"log_exists"`
	StateDir    string   `json:"state_dir"`
	PIDFile     string   `json:"pid_file"`
	LogFile     string   `json:"log_file"`
	CommandFile string   `json:"command_file"`
	Issues      []string `json:"issues"`
}

func Inspect(root string) Status {
	stateDir := filepath.Join(root, ".tmp", "workloads")
	status := Status{
		State:       "not_running",
		StateDir:    stateDir,
		PIDFile:     filepath.Join(stateDir, "current.pid"),
		LogFile:     filepath.Join(stateDir, "current.log"),
		CommandFile: filepath.Join(stateDir, "current.cmd"),
		Issues:      []string{},
	}

	if pidText, ok := readTrimmed(&status, status.PIDFile, "pid file"); ok {
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 {
			status.State = "unknown"
			addIssue(&status, "invalid pid: %s", pidText)
		} else {
			status.PID = pid
			if processRunning(pid) {
				status.State = "running"
			} else {
				status.State = "stopped"
			}
		}
	}

	if command, ok := readTrimmed(&status, status.CommandFile, "command file"); ok {
		status.Command = command
	}
	if logPath, ok := readTrimmed(&status, status.LogFile, "log file"); ok {
		status.Log = logPath
		if info, err := os.Stat(logPath); err == nil && info.Mode().IsRegular() {
			status.LogExists = true
		} else if err != nil && !os.IsNotExist(err) {
			addIssue(&status, "log stat failed: %v", err)
		}
	}

	return status
}

func Render(w io.Writer, status Status) error {
	if status.PID > 0 {
		if _, err := fmt.Fprintf(w, "%s pid=%d\n", status.State, status.PID); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, status.State); err != nil {
		return err
	}

	if status.Command != "" {
		if _, err := fmt.Fprintf(w, "command=%s\n", status.Command); err != nil {
			return err
		}
	}
	if status.Log != "" {
		if _, err := fmt.Fprintf(w, "log=%s\n", status.Log); err != nil {
			return err
		}
	}
	for _, issue := range status.Issues {
		if _, err := fmt.Fprintf(w, "issue=%s\n", issue); err != nil {
			return err
		}
	}
	return nil
}

func RenderJSON(w io.Writer, status Status) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

func readTrimmed(status *Status, path string, label string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		addIssue(status, "%s read failed: %v", label, err)
		return "", false
	}
	value := strings.TrimSpace(string(content))
	if value == "" {
		addIssue(status, "%s is empty", label)
		return "", false
	}
	return value, true
}

func processRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func addIssue(status *Status, format string, args ...any) {
	status.Issues = append(status.Issues, fmt.Sprintf(format, args...))
}
