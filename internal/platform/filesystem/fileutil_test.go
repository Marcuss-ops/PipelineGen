package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("roundtrip", func(t *testing.T) {
		src := filepath.Join(tmpDir, "src.txt")
		content := []byte("hello copy")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		dst := filepath.Join(tmpDir, "dst.txt")
		if err := CopyFile(src, dst); err != nil {
			t.Fatalf("CopyFile failed: %v", err)
		}

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("copied content = %q, want %q", got, content)
		}
	})

	t.Run("missing source", func(t *testing.T) {
		dst := filepath.Join(tmpDir, "missing_dst.txt")
		if err := CopyFile(filepath.Join(tmpDir, "nonexistent.txt"), dst); err == nil {
			t.Error("expected error for missing source")
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Error("expected destination to not exist after failed copy")
		}
	})
}

func TestUsableCachedClip(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("missing file", func(t *testing.T) {
		ok, err := UsableCachedClip(filepath.Join(tmpDir, "missing.mp4"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected false for missing file")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "empty.mp4")
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		ok, err := UsableCachedClip(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected false for empty file")
		}
		// File should have been removed
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("expected empty file to be removed")
		}
	})

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(tmpDir, "valid.mp4")
		if err := os.WriteFile(path, []byte{0x00, 0x01}, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		ok, err := UsableCachedClip(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Error("expected true for valid file")
		}
	})
}
