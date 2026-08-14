// Package strictjson provides fail-closed JSON loading for evidence and
// release-control artifacts. It rejects ambiguous JSON before decoding into a
// caller-provided type and reads filesystem inputs through a bounded regular
// non-symlink file contract.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// LoadFile reads at most maxBytes from path and strictly parses the result into
// target. The path and the opened file must identify the same regular,
// non-symlink file. Files that change size while being read are rejected.
func LoadFile(path string, maxBytes int64, target any) error {
	if maxBytes <= 0 {
		return fmt.Errorf("maximum JSON file size must be positive")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect JSON file: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("JSON file must be a regular non-symlink file")
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open JSON file: %w", err)
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened JSON file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return fmt.Errorf("JSON file changed while it was being opened")
	}
	if openedInfo.Size() > maxBytes {
		return fmt.Errorf("JSON file exceeds %d bytes", maxBytes)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read JSON file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return fmt.Errorf("JSON file exceeds %d bytes", maxBytes)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect read JSON file: %w", err)
	}
	if !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != openedInfo.Size() || finalInfo.Size() != int64(len(content)) {
		return fmt.Errorf("JSON file changed while it was being read")
	}

	return Parse(content, target)
}

// Parse decodes exactly one JSON value into target. Duplicate object
// properties, explicit null values, unknown struct fields, and trailing JSON
// are rejected.
func Parse(content []byte, target any) error {
	if !utf8.Valid(content) {
		return fmt.Errorf("JSON input is not valid UTF-8")
	}
	if err := rejectAmbiguousJSON(content); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func rejectAmbiguousJSON(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := scanValue(decoder, "$"); err != nil {
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

func scanValue(decoder *json.Decoder, path string) error {
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
			if err := scanValue(decoder, path+"."+key); err != nil {
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
			if err := scanValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
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
