// Package releaseassets verifies a closed inventory of one GitHub release's
// downloaded assets. Verification is an integrity and candidate-binding check;
// it does not authenticate GitHub, the inventory producer, or the provider
// asset identifiers recorded in the inventory.
package releaseassets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"
)

const (
	SchemaVersion        = "pgworkbench.release-asset-inventory/v1"
	ArtifactType         = "pgworkbench.release-asset-inventory"
	FingerprintAlgorithm = "github-release-assets-jq-cS/v1"

	ReleaseStateDraft     = "draft"
	ReleaseStatePublished = "published"
)

var canonicalPositiveInteger = regexp.MustCompile(`^[1-9][0-9]*$`)

const maxJSONSafeInteger = uint64(9007199254740991)

// AssetID retains whether a provider asset identifier was encoded as a JSON
// string or as a positive JSON integer. The distinction is part of the
// fingerprint input: the number 42 and the string "42" are different IDs.
type AssetID struct {
	kind  assetIDKind
	value string
}

type assetIDKind uint8

const (
	assetIDInvalid assetIDKind = iota
	assetIDString
	assetIDInteger
)

func NewStringAssetID(value string) (AssetID, error) {
	if !validAssetIDString(value) {
		return AssetID{}, fmt.Errorf("asset id string must contain 1 to 256 valid non-control Unicode characters")
	}
	return AssetID{kind: assetIDString, value: value}, nil
}

func NewIntegerAssetID(value uint64) (AssetID, error) {
	if value == 0 || value > maxJSONSafeInteger {
		return AssetID{}, fmt.Errorf("asset id integer must be between 1 and %d", maxJSONSafeInteger)
	}
	return AssetID{kind: assetIDInteger, value: strconv.FormatUint(value, 10)}, nil
}

// NewIntegerAssetIDString constructs an integer ID without narrowing a JSON
// integer to the platform's integer width.
func NewIntegerAssetIDString(value string) (AssetID, error) {
	if !canonicalPositiveInteger.MatchString(value) {
		return AssetID{}, fmt.Errorf("asset id integer must be a canonical positive JSON integer")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed > maxJSONSafeInteger {
		return AssetID{}, fmt.Errorf("asset id integer must be no greater than %d", maxJSONSafeInteger)
	}
	return AssetID{kind: assetIDInteger, value: value}, nil
}

func (id AssetID) IsString() bool {
	return id.kind == assetIDString
}

func (id AssetID) IsInteger() bool {
	return id.kind == assetIDInteger
}

// Value returns the decoded string value or the canonical base-10 integer
// representation. Call IsString or IsInteger when the JSON type matters.
func (id AssetID) Value() string {
	return id.value
}

func (id AssetID) MarshalJSON() ([]byte, error) {
	switch id.kind {
	case assetIDString:
		return json.Marshal(id.value)
	case assetIDInteger:
		if !canonicalPositiveInteger.MatchString(id.value) {
			return nil, fmt.Errorf("asset id integer is not canonical")
		}
		return []byte(id.value), nil
	default:
		return nil, fmt.Errorf("asset id is not initialized")
	}
}

func (id *AssetID) UnmarshalJSON(content []byte) error {
	if id == nil {
		return fmt.Errorf("asset id destination is nil")
	}
	if len(content) == 0 {
		return fmt.Errorf("asset id is empty")
	}
	if content[0] == '"' {
		if !utf8.Valid(content) {
			return fmt.Errorf("asset id string is not valid UTF-8")
		}
		var value string
		decoder := json.NewDecoder(bytes.NewReader(content))
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode asset id string: %w", err)
		}
		parsed, err := NewStringAssetID(value)
		if err != nil {
			return err
		}
		*id = parsed
		return nil
	}
	parsed, err := NewIntegerAssetIDString(string(content))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func (id AssetID) key() string {
	switch id.kind {
	case assetIDString:
		return "string:" + id.value
	case assetIDInteger:
		return "integer:" + id.value
	default:
		return "invalid:"
	}
}

func validAssetIDString(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

type Asset struct {
	ID     AssetID `json:"id"`
	Name   string  `json:"name"`
	Size   int64   `json:"size"`
	Digest string  `json:"digest"`
}

type Inventory struct {
	SchemaVersion        string  `json:"schema_version"`
	ArtifactType         string  `json:"artifact_type"`
	ReleaseState         string  `json:"release_state"`
	Tag                  string  `json:"tag"`
	GitCommit            string  `json:"git_commit"`
	CapturedAt           string  `json:"captured_at"`
	FingerprintAlgorithm string  `json:"fingerprint_algorithm"`
	AssetFingerprint     string  `json:"asset_fingerprint"`
	Assets               []Asset `json:"assets"`
}

type Verification struct {
	Valid               bool     `json:"valid"`
	ComputedFingerprint string   `json:"computed_fingerprint"`
	Issues              []string `json:"issues"`
}

func (verification Verification) IsValid() bool {
	return verification.Valid
}
