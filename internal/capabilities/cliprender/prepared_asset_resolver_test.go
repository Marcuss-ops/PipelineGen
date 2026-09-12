package cliprender

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

type fallbackMaterializer struct {
	calls int
	asset *MaterializedAsset
}

func (f *fallbackMaterializer) Materialize(context.Context, AssetRef) (*MaterializedAsset, error) {
	f.calls++
	return f.asset, nil
}

func TestPreparedAssetResolver_HitAvoidsFallback(t *testing.T) {
	root := t.TempDir()
	content := []byte("prepared bytes")
	hash := sha256.Sum256(content)
	sha := fmtHash(hash[:])
	path := filepath.Join(root, sha, "source.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fallback := &fallbackMaterializer{asset: &MaterializedAsset{LocalPath: "fallback"}}
	resolver, err := NewPreparedAssetResolver(root, fallback)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Materialize(context.Background(), AssetRef{AssetID: "asset-1", MediaType: "video", LegacyFileMD5: sha})
	if err != nil || got.LocalPath != path || !got.FromCache || fallback.calls != 0 {
		t.Fatalf("cache hit: got=%#v err=%v fallback_calls=%d", got, err, fallback.calls)
	}
}

// TestPreparedAssetResolver_MemoizesVerificationAcrossRenders pins the batch
// win: N renders of the same content-addressed source must verify it ONCE, not
// once per clip. The counting hasher proves no second full-file read happened.
func TestPreparedAssetResolver_MemoizesVerificationAcrossRenders(t *testing.T) {
	root := t.TempDir()
	content := []byte("shared source bytes")
	sum := sha256.Sum256(content)
	sha := fmtHash(sum[:])
	path := filepath.Join(root, sha, "source.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	counted := &countingHasher{}
	fallback := &fallbackMaterializer{asset: &MaterializedAsset{LocalPath: "fallback"}}
	resolver, err := NewPreparedAssetResolver(root, fallback)
	if err != nil {
		t.Fatal(err)
	}
	resolver.verifier = NewContentVerifier(counted.hash)
	for i := 0; i < 3; i++ {
		got, err := resolver.Materialize(context.Background(), AssetRef{AssetID: "asset-4", MediaType: "video", LegacyFileMD5: sha})
		if err != nil || got.LocalPath != path || !got.FromCache || got.SHA256 != sha || got.SizeBytes != int64(len(content)) {
			t.Fatalf("render %d: got=%#v err=%v", i, got, err)
		}
	}
	if counted.calls != 1 {
		t.Fatalf("full-file hashes across 3 renders = %d, want 1", counted.calls)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", fallback.calls)
	}
}

func TestPreparedAssetResolver_MissFallsBack(t *testing.T) {
	fallback := &fallbackMaterializer{asset: &MaterializedAsset{LocalPath: "downloaded", SHA256: "sha"}}
	resolver, err := NewPreparedAssetResolver(t.TempDir(), fallback)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Materialize(context.Background(), AssetRef{AssetID: "asset-2", MediaType: "video", LegacyFileMD5: "unknown"})
	if err != nil || got.LocalPath != "downloaded" || fallback.calls != 1 {
		t.Fatalf("fallback: got=%#v err=%v fallback_calls=%d", got, err, fallback.calls)
	}
}

func TestPreparedAssetResolver_CorruptCacheFallsBack(t *testing.T) {
	root := t.TempDir()
	expected := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := filepath.Join(root, expected, "source.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	fallback := &fallbackMaterializer{asset: &MaterializedAsset{LocalPath: "fresh"}}
	resolver, err := NewPreparedAssetResolver(root, fallback)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Materialize(context.Background(), AssetRef{AssetID: "asset-3", MediaType: "video", LegacyFileMD5: expected})
	if err != nil || got.LocalPath != "fresh" || fallback.calls != 1 {
		t.Fatalf("corrupt fallback: got=%#v err=%v fallback_calls=%d", got, err, fallback.calls)
	}
}

func fmtHash(bytes []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, len(bytes)*2)
	for i, b := range bytes {
		out[i*2] = hexChars[b>>4]
		out[i*2+1] = hexChars[b&15]
	}
	return string(out)
}
