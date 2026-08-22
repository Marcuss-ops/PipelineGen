package checksum

import (
	"os"
	"strings"
	"testing"
)

// TestLegacyMD5String_Golden pins the absolute MD5 digests produced by the
// pre-isolation implementation (md5.Sum + hex.EncodeToString) so a future
// algorithm/encoding drift fails loudly.
func TestLegacyMD5String_Golden(t *testing.T) {
	cases := map[string]string{
		"":      "d41d8cd98f00b204e9800998ecf8427e",
		"hello": "5d41402abc4b2a76b9719d911017c592",
	}
	for in, want := range cases {
		if got := LegacyMD5String(in); got != want {
			t.Fatalf("LegacyMD5String(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLegacyMD5File_Golden pins the file MD5 against the same "hello"
// vector (golden old==new: the streaming io.Copy path must produce the
// identical digest as md5.Sum on the full buffer).
func TestLegacyMD5File_Golden(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/hello.txt"
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := LegacyMD5File(path)
	if err != nil {
		t.Fatalf("LegacyMD5File: %v", err)
	}
	const want = "5d41402abc4b2a76b9719d911017c592"
	if got != want {
		t.Fatalf("LegacyMD5File = %q, want %q", got, want)
	}
}

// TestLegacyMD5Reader_MatchesString pins reader-vs-string equivalence for a
// multi-block payload (golden old==new: no buffering artifact).
func TestLegacyMD5Reader_MatchesString(t *testing.T) {
	payload := strings.Repeat("x", 1<<20) + "tail"
	got, err := LegacyMD5Reader(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("LegacyMD5Reader: %v", err)
	}
	if got != LegacyMD5String(payload) {
		t.Fatalf("reader digest %q != string digest %q", got, LegacyMD5String(payload))
	}
}

func TestProviderMD5Checksum(t *testing.T) {
	// Lowercase hex is accepted and returned canonical.
	got, err := ProviderMD5Checksum("5d41402abc4b2a76b9719d911017c592")
	if err != nil {
		t.Fatalf("lowercase provider checksum must validate: %v", err)
	}
	if got != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("canonical form = %q", got)
	}
	// Uppercase hex normalizes to lowercase.
	got, err = ProviderMD5Checksum("5D41402ABC4B2A76B9719D911017C592")
	if err != nil {
		t.Fatalf("uppercase provider checksum must validate: %v", err)
	}
	if got != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("uppercase must normalize to lowercase, got %q", got)
	}
	// Malformed provider data fails closed (godlike/07 NO-FAKE-AVAILABILITY).
	for _, bad := range []string{"", "abc", strings.Repeat("0", 31), strings.Repeat("0", 33), "zz41402abc4b2a76b9719d911017c592"} {
		if _, err := ProviderMD5Checksum(bad); err == nil {
			t.Fatalf("ProviderMD5Checksum(%q) must fail closed", bad)
		}
	}
}
