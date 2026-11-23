package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type stubAssetLookup struct {
	details *asset.Details
	err     error
}

func (s stubAssetLookup) Get(context.Context, string) (*asset.Details, error) {
	return s.details, s.err
}

func detailsWithLocalURI(uri string) *asset.Details {
	return &asset.Details{
		Asset: &asset.Asset{ID: "clip_1"},
		Locations: []*asset.Location{{
			AssetID:      "clip_1",
			LocationKind: asset.LocationKindLocal,
			URI:          uri,
			IsPrimary:    true,
		}},
	}
}

// TestProbeClip_RejectsMissingCreatorLocalBytes pins the measured case: the
// registry knows the clip but the local file is gone. This must fail at
// preflight, before the render, not in the remote final job.
func TestProbeClip_RejectsMissingCreatorLocalBytes(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "intro.mp4")
	prober := &assetServiceClipProber{assets: stubAssetLookup{details: detailsWithLocalURI(missing)}}

	err := prober.ProbeClip(context.Background(), "clip_1")
	if err == nil {
		t.Fatal("a missing creator-local source must fail the preflight")
	}
	if !strings.Contains(err.Error(), "creator-local source") || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error must name the missing local source, got %v", err)
	}
}

// TestProbeClip_AcceptsExistingCreatorLocalBytes keeps the healthy path free of
// false negatives.
func TestProbeClip_AcceptsExistingCreatorLocalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intro.mp4")
	if err := os.WriteFile(path, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	prober := &assetServiceClipProber{assets: stubAssetLookup{details: detailsWithLocalURI("file://" + path)}}

	if err := prober.ProbeClip(context.Background(), "clip_1"); err != nil {
		t.Fatalf("an existing local file must pass, got %v", err)
	}
}

// TestProbeClip_LeavesRemoteLocationsAlone is the false-negative guard: a
// remote-mode asset has no local bytes and must not be rejected.
func TestProbeClip_LeavesRemoteLocationsAlone(t *testing.T) {
	for _, uri := range []string{"s3://bucket/intro.mp4", "https://cdn.example.com/intro.mp4", ""} {
		prober := &assetServiceClipProber{assets: stubAssetLookup{details: detailsWithLocalURI(uri)}}
		if err := prober.ProbeClip(context.Background(), "clip_1"); err != nil {
			t.Fatalf("remote/empty location %q must pass, got %v", uri, err)
		}
	}
}

// TestProbeClip_RejectsDirectory keeps the probe honest about what a local
// source must be.
func TestProbeClip_RejectsDirectory(t *testing.T) {
	prober := &assetServiceClipProber{assets: stubAssetLookup{details: detailsWithLocalURI(t.TempDir())}}
	if err := prober.ProbeClip(context.Background(), "clip_1"); err == nil {
		t.Fatal("a directory is not a creator-local media file")
	}
}
