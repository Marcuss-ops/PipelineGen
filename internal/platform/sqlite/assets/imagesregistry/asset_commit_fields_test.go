package imagesregistry

import (
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

func TestNormalizeAssetCommitFieldsDerivesStableDefaults(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	fields := normalizeAssetCommitFields(persistence.CommitRequest{
		AssetID: "asset", Filename: "clip.mp4", ContentHash: "content",
		Metadata: persistence.TypedMetadata{StartSec: 1.5, EndSec: 3.25},
	}, now)
	if fields.name != "clip.mp4" || fields.indexState != "DISCOVERED" || fields.sourceVersion != "content" {
		t.Fatalf("normalized defaults = %+v", fields)
	}
	if fields.startMS != 1500 || fields.endMS != 3250 {
		t.Fatalf("time conversion = start:%d end:%d", fields.startMS, fields.endMS)
	}
	if !fields.now.Equal(now) || !fields.requestedAt.Equal(now) {
		t.Fatalf("timestamps = now:%v requested:%v", fields.now, fields.requestedAt)
	}
}

func TestNormalizeAssetCommitFieldsPrefersExplicitValues(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	requested := now.Add(-time.Hour)
	fields := normalizeAssetCommitFields(persistence.CommitRequest{
		AssetID: "asset", Name: "explicit", IndexState: "READY", ContentHash: "content",
		RequestedAt: requested, StartMs: 42, EndMs: 84,
		Metadata: persistence.TypedMetadata{Title: "metadata title", SourceVersion: "revision"},
	}, now)
	if fields.name != "explicit" || fields.indexState != "READY" || fields.sourceVersion != "revision" || fields.startMS != 42 || fields.endMS != 84 {
		t.Fatalf("explicit values lost = %+v", fields)
	}
	if !fields.requestedAt.Equal(requested) {
		t.Fatalf("requested timestamp = %v, want %v", fields.requestedAt, requested)
	}
}
