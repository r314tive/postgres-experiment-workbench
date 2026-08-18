package releaseevidence

import (
	"fmt"
	"path/filepath"
	"strings"
)

func validateDirectoryEntryName(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || filepath.Clean(name) != name || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("release evidence directory entry name must be a single non-empty basename")
	}
	return nil
}
