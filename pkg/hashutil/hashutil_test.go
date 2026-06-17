package hashutil

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

func TestMD5String(t *testing.T) {
	hash := MD5String("hello")
	if len(hash) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(hash), hash)
	}
	hash2 := MD5String("hello")
	if hash != hash2 {
		t.Error("expected deterministic hashing")
	}
}

func TestMD5File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	hash, err := MD5File(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 {
		t.Errorf("expected 32 hex chars, got %d: %s", len(hash), hash)
	}
	expected := MD5String("hello")
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestMD5File_NotFound(t *testing.T) {
	_, err := MD5File("/nonexistent/file.txt")
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

func TestMD5String_Empty(t *testing.T) {
	hash := MD5String("")
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
