package digest

import (
	"errors"
	"strings"
	"testing"
)

// Known SHA-256 golden vectors (RFC 6234 / FIPS 180-4 test vectors).
const (
	goldenHello = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	goldenEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func TestSHA256String_Golden(t *testing.T) {
	if got := SHA256String("hello"); got != goldenHello {
		t.Fatalf("SHA256String(hello) = %q, want %q", got, goldenHello)
	}
	if got := SHA256String(""); got != goldenEmpty {
		t.Fatalf("SHA256String(empty) = %q, want %q", got, goldenEmpty)
	}
	if got := SHA256String("hello"); len(got) != SHA256HexLength {
		t.Fatalf("digest length = %d, want %d", len(got), SHA256HexLength)
	}
}

func TestSHA256Bytes_MatchesString(t *testing.T) {
	want := SHA256String("hello")
	if got := SHA256Bytes([]byte("hello")); got != want {
		t.Fatalf("SHA256Bytes = %q, want %q", got, want)
	}
}

func TestSHA256Reader_MatchesBytes(t *testing.T) {
	want := SHA256Bytes([]byte("hello world"))
	got, err := SHA256Reader(strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("SHA256Reader error: %v", err)
	}
	if got != want {
		t.Fatalf("SHA256Reader = %q, want %q", got, want)
	}
}

// errReader fails on the first read to prove SHA256Reader surfaces errors.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestSHA256Reader_Error(t *testing.T) {
	if _, err := SHA256Reader(errReader{}); err == nil {
		t.Fatal("SHA256Reader must surface reader errors")
	}
}

func TestFingerprint_NULSeparator_PreventsCollision(t *testing.T) {
	// "a|b" + "c" must not collide with "a" + "b|c".
	if a, b := Fingerprint("a|b", "c"), Fingerprint("a", "b|c"); a == b {
		t.Fatalf("NUL separator broken: %q collides with %q", a, b)
	}
	// Two parts "a","b" must equal the single canonical string "a\x00b".
	if got, want := Fingerprint("a", "b"), Fingerprint("a\x00b"); got != want {
		t.Fatalf("Fingerprint(a,b) = %q, want Fingerprint(a\\x00b) = %q", got, want)
	}
}

func TestFingerprint_Deterministic(t *testing.T) {
	in := []string{"preset", "font", "color", "timing", "text"}
	if got, want := Fingerprint(in...), Fingerprint(in...); got != want {
		t.Fatalf("Fingerprint not deterministic: %q vs %q", got, want)
	}
	if len(Fingerprint(in...)) != SHA256HexLength {
		t.Fatalf("Fingerprint length = %d, want %d", len(Fingerprint(in...)), SHA256HexLength)
	}
}

func TestIsSHA256(t *testing.T) {
	if !IsSHA256(goldenHello) {
		t.Fatalf("IsSHA256(%q) = false, want true", goldenHello)
	}
	if IsSHA256("d41d8cd98f00b204e9800998ecf8427e") { // classic 32-char MD5
		t.Fatal("32-char MD5 must not be classified as SHA-256")
	}
	if IsSHA256("zz" + goldenHello[2:]) {
		t.Fatal("non-hex characters must not be classified as SHA-256")
	}
	if IsSHA256(goldenHello[:63]) {
		t.Fatal("truncated digest must not be classified as SHA-256")
	}
	if IsSHA256("") {
		t.Fatal("empty string must not be classified as SHA-256")
	}
}

func TestValidateSHA256(t *testing.T) {
	if err := ValidateSHA256(""); err != nil {
		t.Fatalf("ValidateSHA256(empty) = %v, want nil", err)
	}
	if err := ValidateSHA256(goldenHello); err != nil {
		t.Fatalf("ValidateSHA256(valid) = %v, want nil", err)
	}
	if err := ValidateSHA256("d41d8cd98f00b204e9800998ecf8427e"); !errors.Is(err, ErrInvalidSHA256) {
		t.Fatalf("ValidateSHA256(md5) = %v, want ErrInvalidSHA256", err)
	}
	if err := ValidateSHA256("not-a-digest"); !errors.Is(err, ErrInvalidSHA256) {
		t.Fatalf("ValidateSHA256(garbage) = %v, want ErrInvalidSHA256", err)
	}
}
