package envfile

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// Parse reads simple KEY=value env files used by workbench specs.
func Parse(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parse(path, file)
}

// ParseBytes parses one already-selected env-file snapshot. The label is used
// only in diagnostics; callers can therefore bind validation and later
// execution to the same byte sequence without reopening a mutable path.
func ParseBytes(label string, content []byte) (map[string]string, error) {
	return parse(label, bytes.NewReader(content))
}

func parse(path string, reader io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(reader)
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
				value = unquoteDouble(value)
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

func unquoteDouble(value string) string {
	inner := value[1 : len(value)-1]
	var out strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] != '\\' || i+1 >= len(inner) {
			out.WriteByte(inner[i])
			continue
		}
		next := inner[i+1]
		switch next {
		case '"', '\\', '$', '`':
			out.WriteByte(next)
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		default:
			out.WriteByte('\\')
			out.WriteByte(next)
		}
		i++
	}
	return out.String()
}
