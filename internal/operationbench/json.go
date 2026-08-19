package operationbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

func decodeStrictBytes(content []byte, value any) error {
	if err := rejectDuplicateKeys(content); err != nil {
		return err
	}
	if _, ok := value.(*Series); ok {
		if err := rejectUnexpectedSeriesNulls(content, "$", "$"); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	if series, ok := value.(*Series); ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(content, &fields); err != nil {
			return err
		}
		_, series.RuntimePortsPresent = fields["runtime_ports"]
		_, series.RuntimePortsDigestPresent = fields["runtime_ports_digest"]
	}
	return nil
}

var nullableOperationSeriesPaths = map[string]struct{}{
	"$.stats.cv_pct":        {},
	"$.stats.robust_cv_pct": {},
}

// rejectUnexpectedSeriesNulls keeps result.json aligned with its JSON Schema.
// The duplicate-key pass has already validated the same byte snapshot.
func rejectUnexpectedSeriesNulls(content json.RawMessage, canonicalPath string, displayPath string) error {
	trimmed := bytes.TrimSpace(content)
	if bytes.Equal(trimmed, []byte("null")) {
		if _, allowed := nullableOperationSeriesPaths[canonicalPath]; allowed {
			return nil
		}
		return fmt.Errorf("%s: null is not allowed", displayPath)
	}
	if len(trimmed) == 0 {
		return fmt.Errorf("%s: empty JSON value", displayPath)
	}

	switch trimmed[0] {
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			return err
		}
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := rejectUnexpectedSeriesNulls(fields[name], canonicalPath+"."+name, displayPath+"."+name); err != nil {
				return err
			}
		}
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return err
		}
		for index, item := range values {
			if err := rejectUnexpectedSeriesNulls(item, canonicalPath+"[]", fmt.Sprintf("%s[%d]", displayPath, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeStrictFile(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() == 0 {
		return fmt.Errorf("must be a non-empty regular non-symlink file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return decodeStrictBytes(content, value)
}

func rejectDuplicateKeys(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object closing token")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array closing token")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
