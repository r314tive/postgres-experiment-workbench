package evidence

import (
	"strings"
	"testing"
)

func TestDigestReaderUsesCanonicalSHA256Format(t *testing.T) {
	digest, size, err := DigestReader(strings.NewReader("artifact\n"))
	if err != nil {
		t.Fatal(err)
	}
	if size != 9 {
		t.Fatalf("size = %d, want 9", size)
	}
	if !IsDigest(digest) {
		t.Fatalf("non-canonical digest: %q", digest)
	}
	if digest != DigestBytes([]byte("artifact\n")) {
		t.Fatalf("reader and byte digests differ: %q", digest)
	}
}

func TestIsDigestRejectsNonCanonicalValues(t *testing.T) {
	valid := DigestBytes([]byte("artifact"))
	for _, value := range []string{"", strings.TrimPrefix(valid, DigestPrefix), strings.ToUpper(valid), DigestPrefix + "xyz"} {
		if IsDigest(value) {
			t.Fatalf("accepted invalid digest: %q", value)
		}
	}
}

func TestBundleInventoryRoundTripIsDeterministic(t *testing.T) {
	inventory := NewBundleInventory("run-a", []BundleFile{{
		Path:   "manifest.env",
		Size:   10,
		Digest: DigestBytes([]byte("manifest\n")),
	}})
	first, err := MarshalBundleInventory(inventory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalBundleInventory(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("inventory encoding is not deterministic")
	}
	parsed, err := ParseBundleInventory(first)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.RunID != "run-a" || len(parsed.Files) != 1 {
		t.Fatalf("unexpected parsed inventory: %#v", parsed)
	}
}

func TestPortablePath(t *testing.T) {
	for _, value := range []string{"manifest.env", "nested/artifact.txt"} {
		if !IsPortablePath(value) {
			t.Fatalf("expected portable path: %q", value)
		}
	}
	for _, value := range []string{"", ".", "../secret", "nested/../secret", "/absolute", `windows\\path`, "C:/windows", "line\nbreak"} {
		if IsPortablePath(value) {
			t.Fatalf("accepted non-portable path: %q", value)
		}
	}
}
