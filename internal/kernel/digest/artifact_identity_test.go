package digest

import (
	"errors"
	"testing"
)

func TestArtifactKeyDigest_Deterministic(t *testing.T) {
	a, err := ArtifactKeyDigest(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"thumbnail",
		`{"timestamp_seconds":1.5}`,
		"thumbnail/v1",
	)
	if err != nil {
		t.Fatalf("ArtifactKeyDigest error: %v", err)
	}
	b, err := ArtifactKeyDigest(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"thumbnail",
		`{"timestamp_seconds":1.5}`,
		"thumbnail/v1",
	)
	if err != nil {
		t.Fatalf("ArtifactKeyDigest error: %v", err)
	}
	if a != b {
		t.Fatalf("ArtifactKeyDigest not deterministic: %q vs %q", a, b)
	}
	if len(a) != SHA256HexLength {
		t.Fatalf("ArtifactKeyDigest length = %d, want %d", len(a), SHA256HexLength)
	}
}

// Parameter field ORDER must not drift the digest (canonicalization). Changing
// the semantic VALUE must.
func TestArtifactKeyDigest_CanonicalizesParameters(t *testing.T) {
	v1, err := ArtifactKeyDigest("s", "op", `{"a":1,"b":2}`, "p")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	v2, err := ArtifactKeyDigest("s", "op", `{"b":2,"a":1}`, "p")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if v1 != v2 {
		t.Fatalf("field order drifted digest: %q vs %q", v1, v2)
	}

	changed, err := ArtifactKeyDigest("s", "op", `{"a":1,"b":3}`, "p")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if changed == v1 {
		t.Fatalf("semantic parameter change must change digest")
	}
}

func TestArtifactKeyDigest_EmptyParametersBecomeObject(t *testing.T) {
	empty, err := ArtifactKeyDigest("s", "op", ``, "p")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	obj, err := ArtifactKeyDigest("s", "op", `{}`, "p")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if empty != obj {
		t.Fatalf("empty params (%q) must equal explicit object (%q)", empty, obj)
	}
}

func TestArtifactKeyDigest_Validation(t *testing.T) {
	if _, err := ArtifactKeyDigest("", "op", `{}`, "p"); !errors.Is(err, ErrInvalidArtifactIdentity) {
		t.Fatalf("empty source: err = %v, want ErrInvalidArtifactIdentity", err)
	}
	if _, err := ArtifactKeyDigest("s", "", `{}`, "p"); !errors.Is(err, ErrInvalidArtifactIdentity) {
		t.Fatalf("empty operation: err = %v, want ErrInvalidArtifactIdentity", err)
	}
	if _, err := ArtifactKeyDigest("s", "op", `{}`, ""); !errors.Is(err, ErrInvalidArtifactIdentity) {
		t.Fatalf("empty processor version: err = %v, want ErrInvalidArtifactIdentity", err)
	}
	if _, err := ArtifactKeyDigest("s", "op", `not json`, "p"); err == nil {
		t.Fatal("malformed parameters must error")
	}
}