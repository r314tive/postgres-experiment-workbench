package envfile

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Parse reads simple KEY=value env files used by workbench specs.
func Parse(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=value", path, lineNumber)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}

		if len(value) >= 2 {
			if value[0] == '"' && value[len(value)-1] == '"' {
				unquoted, err := strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: %w", path, lineNumber, err)
				}
				value = unquoted
			} else if value[0] == '\'' && value[len(value)-1] == '\'' {
				value = value[1 : len(value)-1]
			}
		}

		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}
