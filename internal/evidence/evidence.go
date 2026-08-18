package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	DigestPrefix                = "sha256:"
	BundleInventoryName         = ".pgworkbench-bundle.json"
	BundleInventorySchema       = "pgworkbench.run-bundle-inventory/v1"
	BundleInventoryArtifactType = "pgworkbench.run-bundle-inventory"
)

type BundleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type BundleInventory struct {
	SchemaVersion string       `json:"schema_version"`
	ArtifactType  string       `json:"artifact_type"`
	RunID         string       `json:"run_id"`
	Files         []BundleFile `json:"files"`
}

func DigestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return DigestPrefix + hex.EncodeToString(sum[:])
}

func DigestReader(reader io.Reader) (string, int64, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, reader)
	if err != nil {
		return "", written, err
	}
	return DigestPrefix + hex.EncodeToString(hash.Sum(nil)), written, nil
}

func DigestFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	digest, _, err := DigestReader(file)
	return digest, err
}

func IsDigest(value string) bool {
	if len(value) != len(DigestPrefix)+sha256.Size*2 || !strings.HasPrefix(value, DigestPrefix) {
		return false
	}
	hexValue := strings.TrimPrefix(value, DigestPrefix)
	if strings.ToLower(hexValue) != hexValue {
		return false
	}
	decoded, err := hex.DecodeString(hexValue)
	return err == nil && len(decoded) == sha256.Size
}

func NewBundleInventory(runID string, files []BundleFile) BundleInventory {
	return BundleInventory{
		SchemaVersion: BundleInventorySchema,
		ArtifactType:  BundleInventoryArtifactType,
		RunID:         runID,
		Files:         files,
	}
}

func MarshalBundleInventory(inventory BundleInventory) ([]byte, error) {
	content, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func ParseBundleInventory(content []byte) (BundleInventory, error) {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()

	var inventory BundleInventory
	if err := decoder.Decode(&inventory); err != nil {
		return BundleInventory{}, err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return BundleInventory{}, fmt.Errorf("unexpected trailing JSON value")
		}
		return BundleInventory{}, err
	}
	return inventory, nil
}

func IsPortablePath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\:") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
