package benchmarkexternal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/r314tive/postgres-experiment-workbench/internal/evidence"
)

func decodeClosedJSON(content []byte, destination any, label string) error {
	if err := rejectDuplicateKeys(content, label); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s contains multiple JSON values", label)
		}
		return fmt.Errorf("decode %s trailing content: %w", label, err)
	}
	return nil
}

func rejectDuplicateKeys(content []byte, label string) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, label); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s contains trailing JSON token %v", label, token)
		}
		return fmt.Errorf("decode %s: %w", label, err)
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, label string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
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
				return fmt.Errorf("decode %s object key: %w", label, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s object key is not text", label)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate %s object key %q", label, key)
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder, label); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("invalid %s object closing token", label)
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, label); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("invalid %s array closing token", label)
		}
	default:
		return fmt.Errorf("unexpected %s JSON delimiter %q", label, delimiter)
	}
	return nil
}

func artifactDigest(artifact Artifact) (string, error) {
	artifact.Digest = ""
	artifact.ArtifactDir = ""
	content, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	return evidence.DigestBytes(content), nil
}

func renderJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONExclusive(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytesExclusive(path, append(content, '\n'), 0o644)
}

func writeBytesExclusive(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	return errors.Join(writeErr, file.Close())
}

func readRegular(path string, limit int64, allowEmpty bool, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular non-symlink file", label)
	}
	if info.Size() > limit || !allowEmpty && info.Size() == 0 {
		minimum := "between 1"
		if allowEmpty {
			minimum = "between 0"
		}
		return nil, fmt.Errorf("%s size must be %s and %d bytes", label, minimum, limit)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return content, nil
}

func fileRef(path string, content []byte) FileRef {
	return FileRef{Path: filepath.ToSlash(path), Digest: evidence.DigestBytes(content), SizeBytes: int64(len(content))}
}

func parseCanonicalUTC(value string) (timeValue time.Time, err error) {
	timeValue, err = time.Parse(time.RFC3339Nano, value)
	if err != nil || !strings.HasSuffix(value, "Z") || timeValue.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("must be canonical UTC RFC3339Nano")
	}
	return timeValue, nil
}
