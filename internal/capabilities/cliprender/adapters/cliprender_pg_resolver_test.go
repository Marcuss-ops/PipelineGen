package adapters

import (
	"context"
	"errors"
	"testing"

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

func TestClipRenderPGAssetResolver_ReaderErrorPropagates(t *testing.T) {
	reader := &fakePGMediaReader{err: errors.New("dial tcp: refused")}
	resolver, _ := NewClipRenderPGAssetResolver(reader, zap.NewNop())
	if _, err := resolver.ResolveAsset(context.Background(), "x"); err == nil {
		t.Fatal("expected transport error to propagate")
	}
}
