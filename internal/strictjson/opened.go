package strictjson

import (
	"fmt"
	"io"
	"math"
	"os"
)

// ReadOpenedFile reads one exact bounded byte snapshot from an already-open
// regular file descriptor. It never reopens a path. Callers that resolve a
// directory entry must open it with no-follow semantics before calling this
// helper; an open descriptor itself no longer retains whether its path was a
// symlink.
func ReadOpenedFile(file *os.File, maxBytes int64) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("opened JSON file is required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("maximum JSON file size must be positive")
	}

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened JSON file: %w", err)
	}
	if openedInfo.Mode()&os.ModeSymlink != 0 || !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("opened JSON file must be a regular non-symlink file")
	}
	if openedInfo.Size() > maxBytes {
		return nil, fmt.Errorf("JSON file exceeds %d bytes", maxBytes)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind opened JSON file: %w", err)
	}

	readLimit := maxBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	content, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return nil, fmt.Errorf("read opened JSON file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("JSON file exceeds %d bytes", maxBytes)
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect read JSON file: %w", err)
	}
	if !os.SameFile(openedInfo, finalInfo) || finalInfo.Size() != openedInfo.Size() || finalInfo.Size() != int64(len(content)) {
		return nil, fmt.Errorf("JSON file changed while it was being read")
	}
	return content, nil
}
