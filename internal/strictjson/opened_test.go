package strictjson

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOpenedFileUsesOneDescriptorAndRewindsIt(t *testing.T) {
	content := []byte("{\"name\":\"record\"}\n")
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}

	read, err := ReadOpenedFile(file, int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != string(content) {
		t.Fatalf("ReadOpenedFile() = %q, want %q", read, content)
	}
}

func TestReadOpenedFileEnforcesBoundAndRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := ReadOpenedFile(file, 2); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadOpenedFile() bound error = %v", err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err := ReadOpenedFile(reader, 16); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("ReadOpenedFile() non-regular error = %v", err)
	}
}
