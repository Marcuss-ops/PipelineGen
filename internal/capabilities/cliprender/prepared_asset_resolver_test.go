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
