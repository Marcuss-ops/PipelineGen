package files

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestRandomString(t *testing.T) {
	s1 := RandomString(16)
	s2 := RandomString(16)
	if len(s1) != 16 {
		t.Errorf("expected length 16, got %d", len(s1))
	}
	if s1 == s2 {
		t.Error("expected different random strings")
	}
}

func TestRandomString_Short(t *testing.T) {
	s := RandomString(1)
	if len(s) != 1 {
		t.Errorf("expected length 1, got %d", len(s))
	}
}

// TestSHA256_Golden locks the byte-identical migration contract: the digest
// of "hello world" must equal the canonical SHA-256 literal produced by the
// pre-migration implementation (crypto/sha256 directly). If the kernel digest
// SSOT ever drifts, this pins old == new for string, bytes, and file forms.
func TestSHA256_Golden(t *testing.T) {
	const goldenHelloWorld = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if got := SHA256String("hello world"); got != goldenHelloWorld {
		t.Fatalf("SHA256String(hello world) = %q, want %q", got, goldenHelloWorld)
	}
	if got := SHA256Bytes([]byte("hello world")); got != goldenHelloWorld {
		t.Fatalf("SHA256Bytes(hello world) = %q, want %q", got, goldenHelloWorld)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "golden.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != goldenHelloWorld {
		t.Fatalf("SHA256File = %q, want %q", got, goldenHelloWorld)
	}
}

func TestSHA256Bytes(t *testing.T) {
	hash := SHA256Bytes([]byte("hello world"))
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d: %s", len(hash), hash)
	}
	hash2 := SHA256Bytes([]byte("hello world"))
	if hash != hash2 {
		t.Error("expected deterministic hashing")
	}
}

func TestSHA256String(t *testing.T) {
	hash := SHA256String("hello world")
	hash2 := SHA256String("hello world")
	if hash != hash2 {
		t.Error("expected deterministic hashing")
	}
	hash3 := SHA256String("hello world!")
	if hash == hash3 {
		t.Error("expected different hash for different input")
	}
}

// TestLegacyMD5String_Golden pins the absolute MD5 digest produced by the
// pre-isolation implementation (md5.New + hex.EncodeToString) so a future
// algorithm/encoding drift fails loudly (golden old==new).
func TestLegacyMD5String_Golden(t *testing.T) {
	const goldenHello = "5d41402abc4b2a76b9719d911017c592"
	if got := LegacyMD5String("hello"); got != goldenHello {
		t.Fatalf("LegacyMD5String(hello) = %q, want %q", got, goldenHello)
	}
}

func TestLegacyMD5String(t *testing.T) {
	hash := LegacyMD5String("hello")
	if len(hash) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(hash), hash)
	}
	hash2 := LegacyMD5String("hello")
	if hash != hash2 {
		t.Error("expected deterministic hashing")
	}
}

// TestLegacyMD5File_Golden pins file MD5 == LegacyMD5String for the same
// content (golden old==new: the streaming path is byte-identical to the
// legacy whole-file computation).
func TestLegacyMD5File_Golden(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	hash, err := LegacyMD5File(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(hash), hash)
	}
	expected := LegacyMD5String("hello")
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestLegacyMD5File_NotFound(t *testing.T) {
	_, err := LegacyMD5File("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}
	hash, err := HashFile(path, sha256.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(hash))
	}
	hash2, _ := HashFile(path, sha256.New())
	if hash != hash2 {
		t.Error("expected deterministic hashing")
	}
}

func TestSHA256Bytes_Empty(t *testing.T) {
	hash := SHA256Bytes([]byte{})
	if hash == "" {
		t.Error("expected non-empty hash for empty input")
	}
}

func TestLegacyMD5String_Empty(t *testing.T) {
	hash := LegacyMD5String("")
	if hash == "" {
		t.Error("expected non-empty hash for empty string")
	}
}

func TestRandomString_Fallback(t *testing.T) {
	for n := 1; n <= 32; n++ {
		s := RandomString(n)
		if len(s) != n {
			t.Errorf("RandomString(%d) returned length %d", n, len(s))
		}
	}
}
