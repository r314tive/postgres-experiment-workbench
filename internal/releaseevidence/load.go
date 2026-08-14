package releaseevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const maxIndexBytes = 2 << 20

// LoadFile reads a bounded regular non-symlink file and strictly decodes one
// release evidence index. Syntax errors, duplicate properties, unknown fields,
// and trailing JSON are load errors. Semantic invalidity is reported by Verify.
func LoadFile(path string) (Index, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return Index{}, fmt.Errorf("inspect release evidence index: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return Index{}, fmt.Errorf("release evidence index must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Index{}, fmt.Errorf("open release evidence index: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return Index{}, fmt.Errorf("inspect opened release evidence index: %w", err)
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(pathInfo, fileInfo) {
		return Index{}, fmt.Errorf("release evidence index changed while it was being opened")
	}
	if fileInfo.Size() > maxIndexBytes {
		return Index{}, fmt.Errorf("release evidence index exceeds %d bytes", maxIndexBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxIndexBytes+1))
	if err != nil {
		return Index{}, fmt.Errorf("read release evidence index: %w", err)
	}
	if len(content) > maxIndexBytes {
		return Index{}, fmt.Errorf("release evidence index exceeds %d bytes", maxIndexBytes)
	}
	index, err := Parse(content)
	if err != nil {
		return Index{}, fmt.Errorf("parse release evidence index: %w", err)
	}
	return index, nil
}

// Parse strictly decodes exactly one JSON index without performing semantic
// verification. This split lets callers distinguish malformed input from a
// well-formed index that records an invalid or no-go state.
func Parse(content []byte) (Index, error) {
	if err := rejectDuplicateProperties(content); err != nil {
		return Index{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var index Index
	if err := decoder.Decode(&index); err != nil {
		return Index{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Index{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return Index{}, err
	}
	return index, nil
}

func rejectDuplicateProperties(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		if token == nil {
			return fmt.Errorf("%s: null is not allowed", path)
		}
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: object property must be a string", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%s.%s: duplicate property", path, key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("%s: unterminated object", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("%s: unterminated array", path)
		}
	default:
		return fmt.Errorf("%s: unexpected delimiter %q", path, delimiter)
	}
	return nil
}
