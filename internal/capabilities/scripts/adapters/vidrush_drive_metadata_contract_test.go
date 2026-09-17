package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushDriveMetadataHashResolverIndexAndDeduplicationContract(t *testing.T) {
	resetEntityImageCatalogCaches()
	repo := newIntegrationEntityImageCatalog()
	seedCatalogPerson(t, repo, "Michael Jordan", "https://images.example/michael-jordan-contract.jpg")
	identity, err := entitycatalog.CanonicalizePersonName("Michael Jordan")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("catalog rows=%d err=%v", len(rows), err)
	}

	const assetID = "asset-michael-jordan-contract"
	const fileHash = "sha256:7f83b1657ff1fc53b92dc18148a1d65dfa135e2f"
	const driveFileID = "drive-file-michael-jordan-contract"
	const driveLink = "https://drive.google.com/file/d/drive-file-michael-jordan-contract/view"
	if err := repo.UpsertMaterialization(context.Background(), entitycatalog.Materialization{
		CandidateID: rows[0].ID, AssetID: assetID,
		LegacyFileMD5: fileHash, DriveFileID: driveFileID, DriveLink: driveLink,
		Status:         entitycatalog.MaterializationStatusMaterialized,
		MaterializedAt: time.Now().UTC(), LastVerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	provider := &catalogReuseImageProvider{}
	finalizer := &catalogReuseFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 1, MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
	}}
	result, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "drive-contract-segment",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "discovery-id", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Michael Jordan", Query: "Michael Jordan",
			SourceURL:    "https://images.example/michael-jordan-contract.jpg",
			RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	images := result.VidRushSegments[0].Assets.SecondaryImages
	if len(images) != 1 {
		t.Fatalf("images=%d want 1", len(images))
	}
	asset := images[0]
	if asset.AssetID != assetID || asset.DriveLink != driveLink {
		t.Fatalf("Drive metadata not hydrated canonically: %+v", asset)
	}
	// Identity and location metadata hydrate; a filesystem path does NOT. The
	// catalog is a durable metadata store, and a path recorded in a database is
	// stale by construction after a restart or a tmpfs sweep. Bytes are located
	// from the content address by the canonical materializer at the moment they
	// are needed, so no path may arrive from this row.
	if asset.LocalPath != "" {
		t.Fatalf("catalog hydration must not inject a local path: got %q", asset.LocalPath)
	}
	if asset.LegacyFileMD5 != fileHash {
		t.Fatalf("hash=%q want %q", asset.LegacyFileMD5, fileHash)
	}
	if asset.PersistenceStatus != scriptpkg.VidRushStatusPersisted {
		t.Fatalf("persistence status=%q", asset.PersistenceStatus)
	}
	if asset.IndexStatus == scriptpkg.VidRushStatusFailed || asset.IndexStatus == "" {
		t.Fatalf("index status=%q must be usable", asset.IndexStatus)
	}
	if provider.acquireCalls.Load() != 0 || finalizer.finalizeCalls.Load() != 0 {
		t.Fatalf("canonical Drive reuse performed acquisition/finalization: acquire=%d finalize=%d", provider.acquireCalls.Load(), finalizer.finalizeCalls.Load())
	}

	// Replaying the same discovery candidate must not create another output
	// row or another Drive object for the same canonical URL/hash.
	replay, err := processor.Process(context.Background(), plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "drive-contract-replay",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "different-discovery-id", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "MICHAEL   JORDAN", Query: "Michael Jordan",
			SourceURL:    "https://images.example/michael-jordan-contract.jpg",
			RightsStatus: "unknown",
		}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	replayed := replay.VidRushSegments[0].Assets.SecondaryImages
	if len(replayed) != 1 || replayed[0].AssetID != assetID || replayed[0].DriveLink != driveLink {
		t.Fatalf("replay did not reuse canonical asset: %+v", replayed)
	}
	if provider.acquireCalls.Load() != 0 || finalizer.finalizeCalls.Load() != 0 {
		t.Fatalf("replay performed new materialization: acquire=%d finalize=%d", provider.acquireCalls.Load(), finalizer.finalizeCalls.Load())
	}
	rowsAfter, err := repo.ListCandidates(context.Background(), identity.CanonicalEntityID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsAfter) != 1 {
		t.Fatalf("duplicate catalog rows=%d want 1", len(rowsAfter))
	}
}

var _ scriptports.VidRushArtifactFinalizer = (*catalogReuseFinalizer)(nil)
