package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

type fakePGMediaReader struct {
	rec *pgmedia.MediaAssetRecord
	err error
	got string
}

func (f *fakePGMediaReader) GetAsset(_ context.Context, assetID string) (*pgmedia.MediaAssetRecord, error) {
	f.got = assetID
	return f.rec, f.err
}

func TestClipRenderPGAssetResolver_ResolveAsset(t *testing.T) {
	reader := &fakePGMediaReader{rec: &pgmedia.MediaAssetRecord{
		ID:           "asset-1",
		Name:         "fallback name",
		Title:        "", // exercises the Title → Name fallback
		MediaType:    "video",
		LocalPath:    "/media/asset-1.mp4",
		DriveFileID:  "drive-1",
		SHA256:       "sha256:abc",
		DurationMS:   4321,
		DriveLink:    "https://drive.example/1",
		DownloadLink: "https://drive.example/1?export=download",
	}}
	resolver, err := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPGAssetResolver: %v", err)
	}

	ref, err := resolver.ResolveAsset(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("ResolveAsset: %v", err)
	}
	if reader.got != "asset-1" {
		t.Fatalf("reader.GetAsset assetID = %q, want asset-1", reader.got)
	}
	if ref.AssetID != "asset-1" || ref.Title != "fallback name" ||
		ref.MediaType != "video" || ref.LocalPath != "/media/asset-1.mp4" ||
		ref.DriveFileID != "drive-1" || ref.LegacyFileMD5 != "sha256:abc" || ref.DurationMS != 4321 {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestClipRenderPGAssetResolver_NotFound(t *testing.T) {
	reader := &fakePGMediaReader{err: pgmedia.ErrMediaAssetNotFound}
	resolver, err := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPGAssetResolver: %v", err)
	}
	if _, err := resolver.ResolveAsset(context.Background(), "missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestClipRenderPGAssetResolver_RequiresReader(t *testing.T) {
	if _, err := NewClipRenderPGAssetResolver(nil, zap.NewNop()); err == nil {
		t.Fatal("nil media reader must fail closed at construction")
	}
}

func TestClipRenderPGAssetResolver_TitleWins(t *testing.T) {
	reader := &fakePGMediaReader{rec: &pgmedia.MediaAssetRecord{ID: "x", Name: "n", Title: "t"}}
	resolver, _ := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	ref, err := resolver.ResolveAsset(context.Background(), "x")
	if err != nil {
		t.Fatalf("ResolveAsset: %v", err)
	}
	if ref.Title != "t" {
		t.Fatalf("Title = %q, want t", ref.Title)
	}
}

// TestClipRenderPGAssetResolver_RejectsUncertifiedPlateBytes pins the plate
// registration contract at the read boundary: a curated background plate must
// be registered with the certified NORMALIZED bytes, so resolving it with any
// other content hash fails closed instead of silently rendering the original
// (audio-bearing) Drive file.
func TestClipRenderPGAssetResolver_RejectsUncertifiedPlateBytes(t *testing.T) {
	plate, ok := mediaregistry.LookupEditorialBackground("drive-background-03")
	if !ok {
		t.Fatal("drive-background-03 is missing from the registry")
	}

	// Certified bytes resolve.
	okReader := &fakePGMediaReader{rec: &pgmedia.MediaAssetRecord{
		ID: "drive-background-03", MediaType: "video", SHA256: plate.SHA256,
	}}
	resolver, err := NewClipRenderPGAssetResolver(okReader, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPGAssetResolver: %v", err)
	}
	if _, err := resolver.ResolveAsset(context.Background(), "drive-background-03"); err != nil {
		t.Fatalf("certified plate bytes rejected: %v", err)
	}

	// Uncertified bytes (e.g. the original Drive file) fail closed.
	badReader := &fakePGMediaReader{rec: &pgmedia.MediaAssetRecord{
		ID: "drive-background-03", MediaType: "video", SHA256: "0badc0ffee",
	}}
	badResolver, err := NewClipRenderPGAssetResolver(badReader, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPGAssetResolver: %v", err)
	}
	if _, err := badResolver.ResolveAsset(context.Background(), "drive-background-03"); err == nil {
		t.Fatal("uncertified plate bytes must fail closed")
	}
}

// TestClipRenderPGAssetResolver_LeavesNonPlateAssetsAlone guards the blast
// radius of the plate check: an ordinary asset (or the legacy classic1 plate,
// which is deliberately outside the curated set) is not subject to it.
func TestClipRenderPGAssetResolver_LeavesNonPlateAssetsAlone(t *testing.T) {
	reader := &fakePGMediaReader{rec: &pgmedia.MediaAssetRecord{
		ID: "classic1", MediaType: "video", SHA256: "legacy-hash",
	}}
	resolver, err := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPGAssetResolver: %v", err)
	}
	if _, err := resolver.ResolveAsset(context.Background(), "classic1"); err != nil {
		t.Fatalf("non-plate asset rejected: %v", err)
	}
}

func TestClipRenderPGAssetResolver_ReaderErrorPropagates(t *testing.T) {
	reader := &fakePGMediaReader{err: errors.New("dial tcp: refused")}
	resolver, _ := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	if _, err := resolver.ResolveAsset(context.Background(), "x"); err == nil {
		t.Fatal("expected transport error to propagate")
	}
}
