package topologyinspect

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type RuntimeOptions struct {
	Env        map[string]string
	RunCommand func([]string) (string, error)
}

type RuntimeStatus struct {
	Inspection Inspection
	Command    []string
	Services   []RuntimeService
	Result     string
}

type RuntimeService struct {
	Service  string
	Name     string
	State    string
	Health   string
	ExitCode int
	Missing  bool
}

type composePSRecord struct {
	Name     string `json:"Name"`
	Service  string `json:"Service"`
	State    string `json:"State"`
	Health   string `json:"Health"`
	ExitCode int    `json:"ExitCode"`
}

func Runtime(root string, topology string, options RuntimeOptions) (RuntimeStatus, error) {
	inspection, err := Inspect(root, topology, Options{Env: options.Env})
	if err != nil {
		return RuntimeStatus{}, err
	}

	command := buildPSJSONCommand(inspection)
	runCommand := options.RunCommand
	if runCommand == nil {
		runCommand = defaultRunCommand
	}
	output, err := runCommand(command)
	if err != nil {
		return RuntimeStatus{}, err
	}

	records, err := parseComposePS(output)
	if err != nil {
		return RuntimeStatus{}, err
	}
	services := summarizeServices(inspection.Services, records)
	return RuntimeStatus{
		Inspection: inspection,
		Command:    command,
		Services:   services,
		Result:     classifyRuntime(services),
	}, nil
}

func RenderRuntime(w io.Writer, status RuntimeStatus) error {
	lines := []string{
		fmt.Sprintf("TOPOLOGY_NAME=%s", status.Inspection.Name),
		fmt.Sprintf("TOPOLOGY_SERVICES=%s", strings.Join(status.Inspection.Services, " ")),
		fmt.Sprintf("TOPOLOGY_PS_COMMAND=%s", strings.Join(status.Command, " ")),
	}
	if _, err := fmt.Fprintln(w, strings.Join(lines, "\n")); err != nil {
		return err
	}
	for _, service := range status.Services {
		missing := "0"
		if service.Missing {
			missing = "1"
		}
		if _, err := fmt.Fprintf(
			w,
			"SERVICE %s name=%s state=%s health=%s exit_code=%d missing=%s\n",
			service.Service,
			service.Name,
			service.State,
			service.Health,
			service.ExitCode,
			missing,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "result=%s\n", status.Result)
	return err
}

func buildPSJSONCommand(inspection Inspection) []string {
	command := append([]string(nil), inspection.ComposeCommand...)
	if inspection.EnvFile != "" {
		command = append(command, "--env-file", inspection.EnvFile)
	}
	command = append(command, profileFlags(inspection.Profiles)...)
	command = append(command, "ps", "--format", "json")
	command = append(command, inspection.Services...)
	return command
}

func defaultRunCommand(command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("%s timed out", command[0])
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		} else {
			detail = firstLine(detail) + "; " + err.Error()
		}
		return string(output), fmt.Errorf("%s failed: %s", strings.Join(command, " "), detail)
	}
	return string(output), nil
}

func parseComposePS(output string) ([]composePSRecord, error) {
	trimmed := bytes.TrimSpace([]byte(output))
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		var records []composePSRecord
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	var records []composePSRecord
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var record composePSRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func summarizeServices(expected []string, records []composePSRecord) []RuntimeService {
	byService := make(map[string]composePSRecord)
	for _, record := range records {
		if record.Service == "" {
			continue
		}
		byService[record.Service] = record
	}

	services := make([]RuntimeService, 0, len(expected))
	for _, service := range expected {
		record, ok := byService[service]
		if !ok {
			services = append(services, RuntimeService{
				Service: service,
				State:   "missing",
				Missing: true,
			})
			continue
		}
		services = append(services, RuntimeService{
			Service:  service,
			Name:     record.Name,
			State:    valueOr(record.State, "unknown"),
			Health:   record.Health,
			ExitCode: record.ExitCode,
		})
	}
	sort.SliceStable(services, func(i, j int) bool {
		return indexOf(expected, services[i].Service) < indexOf(expected, services[j].Service)
	})
	return services
}

func classifyRuntime(services []RuntimeService) string {
	if len(services) == 0 {
		return "unknown"
	}
	allRunning := true
	anyRunning := false
	for _, service := range services {
		running := service.State == "running" && (service.Health == "" || service.Health == "healthy")
		if running {
			anyRunning = true
		}
		if !running {
			allRunning = false
		}
	}
	if allRunning {
		return "running"
	}
	if anyRunning {
		return "partial"
	}
	return "stopped"
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return len(values)
}

func firstLine(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	return strings.TrimSpace(line)
}
