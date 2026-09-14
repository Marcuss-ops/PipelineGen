package finalizer

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// TestSkipCatalogCommit_UnpublishedMetadataArtifact pins the
// PR-STOCK-METADATA-LOCAL-ONLY gate: the run-level metadata.json that Stock
// deliberately keeps off Drive (RuntimeConfig.SkipMetadataUpload) must not
// produce a media_assets row — it has no location, so a catalog entry would be
// an asset pointing nowhere (observed as one DISCOVERED "metadata.json" row per
// run before this fix).
func TestSkipCatalogCommit_UnpublishedMetadataArtifact(t *testing.T) {
	unpublished := finalization.PublishedArtifact{
		ArtifactID:       "job-1:metadata",
		Kind:             finalization.KindMetadata,
		Filename:         "metadata.json",
		ArtifactMetadata: map[string]any{"drive_upload_skipped": true, "job_id": "job-1"},
	}
	if !skipCatalogCommit(unpublished) {
		t.Fatal("skipCatalogCommit(unpublished metadata.json) = false, want true")
	}

	cases := []struct {
		name     string
		artifact finalization.PublishedArtifact
	}{
		{
			name:     "published metadata (has Drive location)",
			artifact: publishedArtifact("job-1:metadata", "sha-meta", "drive-meta"),
		},
		{
			name: "metadata without the explicit skip decision",
			artifact: finalization.PublishedArtifact{
				ArtifactID: "job-1:metadata",
				Kind:       finalization.KindMetadata,
				Filename:   "metadata.json",
			},
		},
		{
			name: "video artifact without location (publish failure must stay loud)",
			artifact: finalization.PublishedArtifact{
				ArtifactID:       "job-1:video",
				Kind:             finalization.KindVideo,
				Filename:         "clip_001.mp4",
				ArtifactMetadata: map[string]any{"drive_upload_skipped": true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if skipCatalogCommit(tc.artifact) {
				t.Fatalf("skipCatalogCommit(%s) = true, want false", tc.name)
			}
		})
	}
}

// TestFinalizeAsset_UnpublishedMetadataWritesNothing pins the durable side of
// the same contract: FinalizeAsset returns a ZERO ref (the "nothing was
// written" signal artifact_writer drops) and no outbox event, and leaves
// media_assets untouched — while a normally published artifact still commits.
func TestFinalizeAsset_UnpublishedMetadataWritesNothing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fx := newTestFinalizer(t, db)
	ctx := context.Background()

	unpublished := finalization.PublishedArtifact{
		ArtifactID:       "job-2:metadata",
		Kind:             finalization.KindMetadata,
		Filename:         "metadata.json",
		MIMEType:         "application/json",
		SizeBytes:        9089,
		SHA256:           "84b22e8da190d264650e0918a10e2df03d1ab0045ef8c26e5b8fbf481f33d4e7",
		SourceVersion:    1,
		Requirement:      finalization.ArtifactRequirementRequired,
		IdempotencyKey:   "stock:84b22e8da190d264:metadata",
		ArtifactMetadata: map[string]any{"drive_upload_skipped": true, "job_id": "job-2"},
		Source:           "stock",
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	ref, events, err := fx.FinalizeAsset(ctx, WrapTx(tx), unpublished)
	if err != nil {
		t.Fatalf("FinalizeAsset(unpublished metadata) = %v, want nil", err)
	}
	if ref.ArtifactID != "" {
		t.Fatalf("ref.ArtifactID = %q, want empty (no durable row was written)", ref.ArtifactID)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (no index request for a location-less artifact)", len(events))
	}

	var assets int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_assets`).Scan(&assets); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if assets != 0 {
		t.Fatalf("media_assets rows = %d, want 0", assets)
	}

	// Control: a published video artifact still commits normally.
	const videoSHA = "2036e99215c101f82679172d685b46cbe42d8f0abe153b46806bce11c12371ff"
	published := publishedArtifact("asset-video-1", videoSHA, "drive-file-abc")
	ref2, err := func() (finalization.ArtifactRef, error) {
		tx2, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx2: %v", err)
		}
		defer tx2.Rollback()
		r, _, err := fx.FinalizeAsset(ctx, WrapTx(tx2), published)
		return r, err
	}()
	if err != nil {
		t.Fatalf("FinalizeAsset(published video) = %v, want nil", err)
	}
	if ref2.ArtifactID != "asset-video-1" {
		t.Fatalf("control ref.ArtifactID = %q, want asset-video-1", ref2.ArtifactID)
	}
}
