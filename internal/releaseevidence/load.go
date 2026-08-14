package releaseevidence

import (
	"fmt"

	"github.com/r314tive/postgres-experiment-workbench/internal/strictjson"
)

const maxIndexBytes = 2 << 20

// LoadFile reads a bounded regular non-symlink file and strictly decodes one
// release evidence index. Syntax errors, duplicate properties, unknown fields,
// and trailing JSON are load errors. Semantic invalidity is reported by Verify.
func LoadFile(path string) (Index, error) {
	var index Index
	if err := strictjson.LoadFile(path, maxIndexBytes, &index); err != nil {
		return Index{}, fmt.Errorf("load release evidence index: %w", err)
	}
	return index, nil
}

// Parse strictly decodes exactly one JSON index without performing semantic
// verification. This split lets callers distinguish malformed input from a
// well-formed index that records an invalid or no-go state.
func Parse(content []byte) (Index, error) {
	var index Index
	if err := strictjson.Parse(content, &index); err != nil {
		return Index{}, err
	}
	return index, nil
}
