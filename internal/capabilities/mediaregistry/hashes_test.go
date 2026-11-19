package mediaregistry

import (
	"errors"
	"strings"
	"testing"
)

func TestIsSHA256Hex(t *testing.T) {
	sha := strings.Repeat("a", 64)
	if !IsSHA256Hex(sha) {
		t.Fatalf("IsSHA256Hex(%q) = false, want true", sha)
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
