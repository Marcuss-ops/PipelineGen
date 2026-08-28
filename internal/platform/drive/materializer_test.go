package drive

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type materializerReader struct {
	content []byte
	calls   int
}

func (r *materializerReader) DownloadFile(context.Context, string) (io.ReadCloser, string, error) {
	r.calls++
	return io.NopCloser(bytesReader(r.content)), "video/mp4", nil
}
func (*materializerReader) GetFileMD5(context.Context, string) (string, error)     { return "", nil }
func (*materializerReader) GetFileMeta(context.Context, string) (*FileMeta, error) { return nil, nil }
func (*materializerReader) ListFiles(context.Context, string) ([]DriveFileInfo, error) {
	return nil, nil
}
func (*materializerReader) FindFileByName(context.Context, string, string) (ExistingFileLookup, error) {
	return ExistingFileLookup{}, nil
}
func (*materializerReader) FileIsNotTrashed(context.Context, string) (bool, error) { return true, nil }
func (*materializerReader) FileExists(context.Context, string) (bool, error)       { return true, nil }
func (*materializerReader) SearchFiles(context.Context, string) ([]DriveFileInfo, error) {
	return nil, nil
}

type byteReader struct {
	data []byte
	off  int
}

func bytesReader(data []byte) *byteReader { return &byteReader{data: data} }
func (r *byteReader) Read(p []byte) (int, error) {
	if r.off == len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

func materializerHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}

func TestCanonicalAssetMaterializer_CreatesContentAddressedDirectoryAndWritesArtifact(t *testing.T) {
	scratch := t.TempDir()
	content := []byte("deterministic artifact bytes")
	expected := materializerHash(content)
	reader := &materializerReader{content: content}
	m, err := NewCanonicalAssetMaterializer(reader, scratch, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := m.Materialize(context.Background(), MaterializeRequest{
		AssetID:        "asset-1",
		DriveFileID:    "drive-1",
		ExpectedSHA256: expected,
		Extension:      ".mp4",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(scratch, "assets", expected, "source.mp4")
	if result.LocalPath != want {
		t.Fatalf("path = %q, want %q", result.LocalPath, want)
	}
	if result.SHA256 != expected {
		t.Fatalf("hash = %q, want %q", result.SHA256, expected)
	}
	if result.SizeBytes != int64(len(content)) {
		t.Fatalf("size = %d, want %d", result.SizeBytes, len(content))
	}
	if reader.calls != 1 {
		t.Fatalf("downloads = %d, want 1", reader.calls)
	}
	if _, err := os.Stat(filepath.Dir(want)); err != nil {
		t.Fatalf("CAS directory missing: %v", err)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("artifact content = %q, want %q", got, content)
	}
	if _, err := os.Stat(want + ".part"); !os.IsNotExist(err) {
		t.Fatalf("part file remains: %v", err)
	}
}

func TestCanonicalAssetMaterializer_CASHitAvoidsSecondWrite(t *testing.T) {
	scratch := t.TempDir()
	content := []byte("cached artifact")
	expected := materializerHash(content)
	reader := &materializerReader{content: content}
	m, err := NewCanonicalAssetMaterializer(reader, scratch, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := MaterializeRequest{AssetID: "asset-2", DriveFileID: "drive-2", ExpectedSHA256: expected, Extension: ".wav"}
	first, err := m.Materialize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Materialize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.FromCache {
		t.Fatal("second materialization should be a cache hit")
	}
	if first.LocalPath != second.LocalPath {
		t.Fatalf("paths differ: %q vs %q", first.LocalPath, second.LocalPath)
	}
	if reader.calls != 1 {
		t.Fatalf("downloads = %d, want 1", reader.calls)
	}
}

func TestCanonicalAssetMaterializer_HashMismatchDoesNotPublishArtifact(t *testing.T) {
	scratch := t.TempDir()
	content := []byte("wrong artifact")
	wrongExpected := materializerHash([]byte("different bytes"))
	reader := &materializerReader{content: content}
	m, err := NewCanonicalAssetMaterializer(reader, scratch, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.Materialize(context.Background(), MaterializeRequest{
		AssetID: "asset-3", DriveFileID: "drive-3", ExpectedSHA256: wrongExpected, Extension: ".png",
	})
	if err == nil {
		t.Fatal("hash mismatch should fail")
	}
	final := filepath.Join(scratch, "assets", wrongExpected, "source.png")
	if _, statErr := os.Stat(final); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched artifact published: %v", statErr)
	}
	if _, statErr := os.Stat(final + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("part file remains: %v", statErr)
	}
}
