package benchmarkimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// validateJSONDocument rejects duplicate object keys and trailing values. The
// upstream result schema is deliberately not guessed, but the retained source
// must still be one well-formed structured JSON document.
func validateJSONDocument(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, "$", true); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return fmt.Errorf("read trailing JSON token: %w", err)
	}
	return nil
}

func decodeStructuredJSON(content []byte) (any, error) {
	if err := validateJSONDocument(content); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func jsonPointer(root any, pointer string) (any, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON Pointer must be non-empty and begin with /")
	}
	current := root
	for _, encoded := range strings.Split(pointer[1:], "/") {
		token, err := decodePointerToken(encoded)
		if err != nil {
			return nil, err
		}
		switch typed := current.(type) {
		case map[string]any:
			value, exists := typed[token]
			if !exists {
				return nil, fmt.Errorf("JSON Pointer %q does not exist", pointer)
			}
			current = value
		case []any:
			if token == "" || len(token) > 1 && token[0] == '0' {
				return nil, fmt.Errorf("JSON Pointer %q has a non-canonical array index", pointer)
			}
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("JSON Pointer %q has an invalid array index", pointer)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("JSON Pointer %q traverses a scalar", pointer)
		}
	}
	return current, nil
}

func decodePointerToken(value string) (string, error) {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			builder.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) || value[index+1] != '0' && value[index+1] != '1' {
			return "", fmt.Errorf("JSON Pointer contains an invalid ~ escape")
		}
		index++
		if value[index] == '0' {
			builder.WriteByte('~')
		} else {
			builder.WriteByte('/')
		}
	}
	return builder.String(), nil
}

func validateJSONValue(decoder *json.Decoder, location string, root bool) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", location, err)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		if root {
			return fmt.Errorf("%s: structured result root must be an object or array", location)
		}
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s: read object key: %w", location, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s: object key is not a string", location)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%s: duplicate object key %q", location, key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, location+"."+key, false); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%s: close object: %w", location, err)
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("%s: object has invalid closing token", location)
		}
		return nil
	case '[':
		index := 0
		for decoder.More() {
			if err := validateJSONValue(decoder, fmt.Sprintf("%s[%d]", location, index), false); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%s: close array: %w", location, err)
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("%s: array has invalid closing token", location)
		}
		return nil
	default:
		return fmt.Errorf("%s: unexpected delimiter %q", location, delimiter)
	}
}

func decodeMapping(content []byte) (Mapping, error) {
	if err := validateJSONDocument(content); err != nil {
		return Mapping{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var mapping Mapping
	if err := decoder.Decode(&mapping); err != nil {
		return Mapping{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Mapping{}, fmt.Errorf("multiple JSON values")
		}
		return Mapping{}, err
	}
	return mapping, nil
}
