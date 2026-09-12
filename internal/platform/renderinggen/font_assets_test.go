package renderinggen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestResolveFontAssetIsAbsoluteAndCertified pins items 11/12: a registered
// font resolves to an ABSOLUTE path under the configured asset root (never a
// CWD-relative one) and carries the real content digest of the file.
func TestResolveFontAssetIsAbsoluteAndCertified(t *testing.T) {
	ref, err := ResolveFontAsset(FontMontserratBold)
	if err != nil {
		t.Fatalf("ResolveFontAsset(%s): %v", FontMontserratBold, err)
	}
	if !filepath.IsAbs(ref.LocalPath) {
		t.Fatalf("font LocalPath %q is not absolute (must not depend on the process CWD)", ref.LocalPath)
	}
	if len(ref.Hash) != 64 {
		t.Fatalf("font digest = %q, want a 64-char SHA-256", ref.Hash)
	}
	if ref.LogicalPath == "" {
		t.Fatal("font LogicalPath must be set")
	}
	info, err := os.Stat(ref.LocalPath)
	if err != nil {
		t.Fatalf("resolved font path is unreadable: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("resolved font is empty")
	}
}

// TestResolveFontAssetUsesConfiguredRootAndCachesDescriptor pins the
// process-invariant contract: the descriptor is resolved against the
// configured asset root exactly once, and later calls never re-read the file.
func TestResolveFontAssetUsesConfiguredRootAndCachesDescriptor(t *testing.T) {
	root := t.TempDir()
	fontDir := filepath.Join(root, "assets", "fonts")
	if err := os.MkdirAll(fontDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake-montserrat-font-bytes")
	localPath := filepath.Join(fontDir, "Montserrat-Bold.ttf")
	if err := os.WriteFile(localPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(AssetRootEnv, root)
	resetFontAssetCache()
	t.Cleanup(resetFontAssetCache)

	first, err := ResolveFontAsset(FontMontserratBold)
	if err != nil {
		t.Fatalf("resolve from configured root: %v", err)
	}
	if first.LocalPath != localPath {
		t.Fatalf("LocalPath = %q, want %q resolved from %s", first.LocalPath, localPath, AssetRootEnv)
	}
	if want := digest.SHA256Bytes(payload); first.Hash != want {
		t.Fatalf("digest = %q, want %q", first.Hash, want)
	}

	// The descriptor is immutable for the process lifetime: removing the
	// source file must not change an already-resolved ref, proving the render
	// hot path performs no second read/hash.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveFontAsset(FontMontserratBold)
	if err != nil {
		t.Fatalf("cached resolve after file removal: %v", err)
	}
	if second != first {
		t.Fatalf("cached descriptor drifted: %+v != %+v", second, first)
	}
}

// TestResolveFontAssetRejectsUnknownID pins fail-closed resolution: an
// unregistered font identity is a typed error, never an empty ref.
func TestResolveFontAssetRejectsUnknownID(t *testing.T) {
	if _, err := ResolveFontAsset("font-not-registered"); err == nil {
		t.Fatal("unknown font id must fail closed")
	}
}
