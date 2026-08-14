package strictjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	Name   string   `json:"name"`
	Nested nested   `json:"nested"`
	Values []string `json:"values"`
}

type nested struct {
	Enabled bool `json:"enabled"`
}

func TestParseStrictJSON(t *testing.T) {
	valid := []byte(`{"name":"record","nested":{"enabled":true},"values":["one","two"]}`)
	var decoded fixture
	if err := Parse(valid, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "record" || !decoded.Nested.Enabled || len(decoded.Values) != 2 {
		t.Fatalf("unexpected decoded fixture: %#v", decoded)
	}

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "unknown field", content: `{"name":"record","nested":{"enabled":true},"values":[],"unknown":true}`, want: "unknown field"},
		{name: "duplicate top level", content: `{"name":"first","name":"second","nested":{"enabled":true},"values":[]}`, want: "duplicate property"},
		{name: "duplicate nested", content: `{"name":"record","nested":{"enabled":true,"enabled":false},"values":[]}`, want: "duplicate property"},
		{name: "null property", content: `{"name":null,"nested":{"enabled":true},"values":[]}`, want: "null is not allowed"},
		{name: "null array value", content: `{"name":"record","nested":{"enabled":true},"values":[null]}`, want: "null is not allowed"},
		{name: "trailing value", content: string(valid) + ` {}`, want: "trailing JSON"},
		{name: "wrong type", content: `{"name":1,"nested":{"enabled":true},"values":[]}`, want: "cannot unmarshal number"},
		{name: "invalid UTF-8", content: string(append([]byte(`{"name":"`), append([]byte{0xff}, []byte(`","nested":{"enabled":true},"values":[]}`)...)...)), want: "valid UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target fixture
			err := Parse([]byte(test.content), &target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoadFileIsBoundedAndRejectsUnsafeInputs(t *testing.T) {
	directory := t.TempDir()
	content := []byte(`{"name":"record","nested":{"enabled":true},"values":[]}`)
	path := filepath.Join(directory, "record.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	var decoded fixture
	if err := LoadFile(path, int64(len(content)), &decoded); err != nil {
		t.Fatal(err)
	}

	t.Run("oversize", func(t *testing.T) {
		var target fixture
		err := LoadFile(path, int64(len(content)-1), &target)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("LoadFile() error = %v, want size error", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(directory, "record-link.json")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		var target fixture
		err := LoadFile(link, 1024, &target)
		if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("LoadFile() error = %v, want non-symlink error", err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		var target fixture
		err := LoadFile(directory, 1024, &target)
		if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("LoadFile() error = %v, want regular-file error", err)
		}
	})
	t.Run("invalid bound", func(t *testing.T) {
		var target fixture
		err := LoadFile(path, 0, &target)
		if err == nil || !strings.Contains(err.Error(), "must be positive") {
			t.Fatalf("LoadFile() error = %v, want bound error", err)
		}
	})
}
