package drive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// countingDriveHasher counts real full-file reads through the materializer's
// verifier. The materializer verifies content-addressed bytes on every cache
// hit; the contract is that the read happens when the bytes enter the system,
// not once per consumer.
type countingDriveHasher struct{ calls int }

func (c *countingDriveHasher) hash(path string) (string, int64, error) {
	c.calls++
	return digest.SHA256File(path)
}

// TestCanonicalAssetMaterializer_RegisteredLocalHitHashesOnce pins the
// registered-local branch: the same registered path served twice must be read
// once.
func TestCanonicalAssetMaterializer_RegisteredLocalHitHashesOnce(t *testing.T) {
	root := t.TempDir()
	content := []byte("registered local bytes")
	registered := filepath.Join(root, "source.mp4")
	if err := os.WriteFile(registered, content, 0o644); err != nil {
		t.Fatal(err)
	}
	reader := &materializerReader{content: content}
	m, err := NewCanonicalAssetMaterializer(reader, filepath.Join(root, "materialized"), nil)
	if err != nil {
		t.Fatalf("materializer: %v", err)
	}
	counted := &countingDriveHasher{}
	m.verifier = digest.NewVerifier(counted.hash)

	req := MaterializeRequest{
		AssetID:        "asset-1",
		RegisteredPath: registered,
		ExpectedSHA256: materializerHash(content),
		Extension:      ".mp4",
	}
	first, err := m.Materialize(context.Background(), req)
	if err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	second, err := m.Materialize(context.Background(), req)
	if err != nil {
		t.Fatalf("second materialize: %v", err)
	}
	if counted.calls != 1 {
		t.Fatalf("full-file reads = %d, want 1 (a certified cache hit must not re-hash)", counted.calls)
	}
	if reader.calls != 0 {
		t.Fatalf("Drive downloads = %d, want 0", reader.calls)
	}
	if first.SHA256 != second.SHA256 || first.LocalPath != registered || !second.FromCache {
		t.Fatalf("materialize results drifted: first=%+v second=%+v", first, second)
	}
}

// TestCanonicalAssetMaterializer_CASHitHashesOnce pins the content-addressed
// branch (scratch/assets/<sha>/source.<ext>), which is the hot path for a batch
// of jobs cut from one source.
func TestCanonicalAssetMaterializer_CASHitHashesOnce(t *testing.T) {
	root := t.TempDir()
	content := []byte("content addressed bytes")
	expected := materializerHash(content)
	scratch := filepath.Join(root, "materialized")
	casDir := filepath.Join(scratch, "assets", expected)
	if err := os.MkdirAll(casDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casDir, "source.mp4"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	reader := &materializerReader{content: content}
	m, err := NewCanonicalAssetMaterializer(reader, scratch, nil)
	if err != nil {
		t.Fatalf("materializer: %v", err)
	}
	counted := &countingDriveHasher{}
	m.verifier = digest.NewVerifier(counted.hash)

	req := MaterializeRequest{AssetID: "asset-1", ExpectedSHA256: expected, Extension: ".mp4"}
	for i := 0; i < 3; i++ {
		res, err := m.Materialize(context.Background(), req)
		if err != nil {
			t.Fatalf("materialize %d: %v", i, err)
		}
		if !res.FromCache || res.SHA256 != expected {
			t.Fatalf("materialize %d: got %+v, want a cache hit on %s", i, res, expected)
		}
	}
	if counted.calls != 1 {
		t.Fatalf("full-file reads = %d, want 1 across three cache hits", counted.calls)
	}
	if reader.calls != 0 {
		t.Fatalf("Drive downloads = %d, want 0", reader.calls)
	}
}

// TestCanonicalAssetMaterializer_HashMismatchRejectsCachedBytes pins that the
// memo does not turn verification into trust: bytes that do not match the
// expected digest are still rejected, and the verifier reports what it read.
func TestCanonicalAssetMaterializer_HashMismatchRejectsCachedBytes(t *testing.T) {
	root := t.TempDir()
	content := []byte("stale bytes")
	registered := filepath.Join(root, "source.mp4")
	if err := os.WriteFile(registered, content, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewCanonicalAssetMaterializer(&materializerReader{content: content}, filepath.Join(root, "materialized"), nil)
	if err != nil {
		t.Fatalf("materializer: %v", err)
	}
	_, err = m.Materialize(context.Background(), MaterializeRequest{
		AssetID:        "asset-1",
		RegisteredPath: registered,
		ExpectedSHA256: materializerHash([]byte("a different artifact")),
		Extension:      ".mp4",
	})
	if err == nil {
		t.Fatal("a registered local file whose bytes do not match the expected digest must be rejected")
	}
}
