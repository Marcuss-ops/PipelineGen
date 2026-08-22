package mediaregistry

import (
	"errors"
	"strings"
	"testing"
)

// TestFingerprint_GoldenOldNew pins the byte-identical migration contract:
// Fingerprint("a","b") must equal the SHA-256 of the NUL-joined string
// "a\x00b" (literal computed by the pre-migration implementation, which was
// sha256.Sum256 over the same NUL join).
func TestFingerprint_GoldenOldNew(t *testing.T) {
	const goldenAB = "59b271ae1bbcb1d31d41929817f4b16fb439eb4f31520b5ad1d5ce98920a7138"
	if got := Fingerprint("a", "b"); got != goldenAB {
		t.Fatalf("Fingerprint(a,b) = %q, want %q", got, goldenAB)
	}
	// A single NUL-containing part must hash identically to the split form.
	if got, want := Fingerprint("a\x00b"), Fingerprint("a", "b"); got != want {
		t.Fatalf("Fingerprint(a\\x00b) = %q, want Fingerprint(a,b) = %q", got, want)
	}
}

func TestFingerprint_DeterministicAndCanonical(t *testing.T) {
	if got, want := Fingerprint("a", "b", "c"), Fingerprint("a", "b", "c"); got != want {
		t.Fatalf("Fingerprint is not deterministic: %q vs %q", got, want)
	}
	if got := Fingerprint("a", "b", "c"); len(got) != 64 {
		t.Fatalf("Fingerprint length = %d, want 64", len(got))
	}
}

func TestFingerprint_DistinguishesDifferentParts(t *testing.T) {
	if Fingerprint("a", "b") == Fingerprint("a", "c") {
		t.Fatal("Fingerprint must change when a part changes")
	}
	if Fingerprint() == Fingerprint("a") {
		t.Fatal("Fingerprint of no parts must differ from Fingerprint of a single part")
	}
}

func TestFingerprint_NULSeparationPreventsCollision(t *testing.T) {
	// "a|b"+"c" vs "a"+"b|c" must NOT collide: NUL joining makes the
	// boundary unambiguous, unlike naive string concatenation.
	joined := Fingerprint("a|b", "c")
	split := Fingerprint("a", "b|c")
	if joined == split {
		t.Fatalf("NUL-separated fingerprint collided across a boundary: %q", joined)
	}
}

func TestIsSHA256Hex(t *testing.T) {
	sha := strings.Repeat("a", 64)
	if !IsSHA256Hex(sha) {
		t.Fatalf("IsSHA256Hex(%q) = false, want true", sha)
	}
	// A valid digest produced by Fingerprint must classify as SHA-256.
	if !IsSHA256Hex(Fingerprint("x")) {
		t.Fatal("Fingerprint output must satisfy IsSHA256Hex")
	}
	// MD5 length (32) and non-hex must be rejected.
	if IsSHA256Hex(strings.Repeat("a", 32)) {
		t.Fatal("32-char value must not classify as SHA-256 (it is MD5-shaped)")
	}
	if IsSHA256Hex(strings.Repeat("z", 64)) {
		t.Fatal("non-hex 64-char value must not classify as SHA-256")
	}
	if IsSHA256Hex("") {
		t.Fatal("empty value must not classify as SHA-256")
	}
}

func TestIsMD5Hex(t *testing.T) {
	md5 := strings.Repeat("b", 32)
	if !IsMD5Hex(md5) {
		t.Fatalf("IsMD5Hex(%q) = false, want true", md5)
	}
	if IsMD5Hex(strings.Repeat("b", 64)) {
		t.Fatal("64-char value must not classify as MD5")
	}
	if IsMD5Hex(strings.Repeat("g", 32)) {
		t.Fatal("non-hex 32-char value must not classify as MD5")
	}
}

func TestNormalizeContentSHA256(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty becomes unknown", "", ContentSHA256Unknown},
		{"whitespace becomes unknown", "   ", ContentSHA256Unknown},
		{"explicit unknown preserved", ContentSHA256Unknown, ContentSHA256Unknown},
		{"known digest trimmed", "  " + strings.Repeat("a", 64) + "  ", strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		if got := NormalizeContentSHA256(tc.in); got != tc.want {
			t.Errorf("%s: NormalizeContentSHA256(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestValidateContentSHA256(t *testing.T) {
	sha := strings.Repeat("a", 64)

	if err := ValidateContentSHA256(""); err != nil {
		t.Fatalf("empty (unknown) digest must validate: %v", err)
	}
	if err := ValidateContentSHA256(ContentSHA256Unknown); err != nil {
		t.Fatalf("UNKNOWN sentinel must validate: %v", err)
	}
	if err := ValidateContentSHA256(sha); err != nil {
		t.Fatalf("64-hex SHA-256 must validate: %v", err)
	}

	// MD5 is forbidden as content identity.
	if err := ValidateContentSHA256(strings.Repeat("a", 32)); !errors.Is(err, ErrInvalidContentSHA256) {
		t.Fatalf("MD5-shaped digest must fail with ErrInvalidContentSHA256, got %v", err)
	}
	// A fabricated value (e.g. "drive-meta-sha256:..." from a Drive ID) must
	// be rejected.
	if err := ValidateContentSHA256("drive-meta-sha256:" + strings.Repeat("a", 64)); !errors.Is(err, ErrInvalidContentSHA256) {
		t.Fatalf("fabricated digest must fail with ErrInvalidContentSHA256, got %v", err)
	}
}
