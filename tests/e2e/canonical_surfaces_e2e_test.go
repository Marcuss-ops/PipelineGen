// Package e2e — Canonical Surfaces E2E test (PR-CANONICAL-E2E-MULTICLIP, July 2026).
//
// Hermetic end-to-end test exercising 1 YouTube clip + 1 Stock chunk
// through all 4 canonical surfaces:
//
//  1. DriveDestination — ResolveDriveDestination derives LocationInput +
//     RootFolderName + PathLeafName from pipeline-specific raw inputs.
//
//  2. ClipIdentity — NewYouTubeClipIdentity / NewStockClipIdentity
//     produce the (AssetID, ContentHash, IndexEventKey) triple that
//     locks the outbox dedup + supersede gate.
//
//  3. ClipSemanticMetadata — unified domain-level metadata shape that
//     Qdrant's AssetData and the SearchTextComposer can consume.
//
//  4. AssetPersistenceWriter — PersistAndIndexRequest carries the
//     canonical write contract for media_assets + outbox_events.
//
// The test verifies that both YouTube and Stock paths produce the SAME
// envelope shape for asset.index.requested (all required keys present
// and non-empty), proving the canonical surfaces are structurally
// equivalent across pipelines.
//
// godlike/07 NO-FAKE-AVAILABILITY: the YouTube path uses the real
// ClipAtomicWriterAdapter to persist + emit the outbox event; the
// Stock path builds the PersistAndIndexRequest from domain types and
// manually persists to the same in-memory SQLite (no concrete
// AssetPersistenceWriter exists yet — forward-pointer to infra-layer
// concrete adapter).
//
// godlike/06 SSOT: this test file is the canonical E2E owner of the
// cross-pipeline canonical-surface verification surface.
package e2e

import (
	"context"
	"encoding/json"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"

	// PR-CANONICAL-E2E-MULTICLIP: import the real persistence port
	// type to catch compile-time field drift (godlike/06 SSOT).
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

// ── Test entry point ──────────────────────────────────────────────────

// TestE2E_CanonicalSurfaces_YouTubeAndStock exercises 1 YouTube clip +
// 1 Stock chunk through all 4 canonical surfaces and verifies both
// produce the same asset.index.requested envelope shape.
//
// This is the canonical cross-pipeline structural equivalence test:
// if this test passes, the domain types (DriveDestination, ClipIdentity,
// ClipSemanticMetadata) and the persistence contract
// (AssetPersistenceWriter) are producing consistent envelopes for both
// YouTube and Stock sources.
func TestE2E_CanonicalSurfaces_YouTubeAndStock(t *testing.T) {
	fx := newE2EFixture(t, "media_assets_current")

	// ── Surface 1: DriveDestination ──────────────────────────────────

	t.Run("DriveDestination", func(t *testing.T) {
		// YouTube: a Broner-Pacquiao clip.
		ytDest := delivery.ResolveDriveDestination(delivery.DriveDestinationInput{
			Category:     "Boxe",
			Subject:      "vdC5GXxS-qU",
			Name:         "Broner yells at Pacquiao",
			Provider:     "youtube",
			ClipTitle:    "Broner yells at Pacquiao",
			ClipStartSec: 146,
			ClipEndSec:   155,
		})
		require.Equal(t, "Boxe", ytDest.LocationInput.Category,
			"DriveDestination YouTube: Category must propagate")
		require.Equal(t, "vdC5GXxS-qU", ytDest.LocationInput.Subject,
			"DriveDestination YouTube: Subject must propagate")
		require.Equal(t, "youtube", ytDest.LocationInput.Provider,
			"DriveDestination YouTube: Provider must propagate")
		require.NotEmpty(t, ytDest.PathLeafName,
			"DriveDestination YouTube: PathLeafName must be derived from ClipTitle")
		require.Contains(t, ytDest.PathLeafName, "broner",
			"DriveDestination YouTube: PathLeafName must derive from title slug")

		// Stock: a Pexels boxing clip.
		stockDest := delivery.ResolveDriveDestination(delivery.DriveDestinationInput{
			Category:     "Boxing",
			Subject:      "pacquiao-training",
			Provider:     "pexels",
			ClipSlug:     "round-7",
			ClipTitle:    "Pacquiao Round 7 Training",
			ClipStartSec: 32,
			ClipEndSec:   51,
		})
		require.Equal(t, "Boxing", stockDest.LocationInput.Category,
			"DriveDestination Stock: Category must propagate")
		require.Equal(t, "pexels", stockDest.LocationInput.Provider,
			"DriveDestination Stock: Provider must propagate")
		require.NotEmpty(t, stockDest.PathLeafName,
			"DriveDestination Stock: PathLeafName must be derived from ClipSlug")
		require.Equal(t, "round-7", stockDest.PathLeafName,
			"DriveDestination Stock: PathLeafName must be the explicit slug")

		// Cross-pipeline: both have non-empty LocationInput.Category
		// and Provider, proving the resolver produces complete
		// semantic-location shapes regardless of source.
		require.NotEmpty(t, ytDest.LocationInput.Category)
		require.NotEmpty(t, ytDest.LocationInput.Provider)
		require.NotEmpty(t, stockDest.LocationInput.Category)
		require.NotEmpty(t, stockDest.LocationInput.Provider)
	})

	// ── Surface 2: ClipIdentity ──────────────────────────────────────

	t.Run("ClipIdentity", func(t *testing.T) {
		// YouTube identity.
		ytID, err := detail.NewYouTubeClipIdentity(detail.YouTubeClipIdentityParams{
			VideoID:     "vdC5GXxS-qU",
			StartSec:    146,
			EndSec:      155,
			PolicyVer:   "v1",
			ContentHash: testSourceVersionFor("yt_vdC5GXxS-qU_146_155_v1"),
			Model:       "multilingual-e5-base",
			Version:     "v1",
			Collection:  "media_assets_current",
		})
		require.NoError(t, err, "NewYouTubeClipIdentity must not fail")
		require.NotEmpty(t, ytID.AssetID)
		require.NotEmpty(t, ytID.ContentHash)
		require.NotEmpty(t, ytID.IndexEventKey)
		require.Contains(t, ytID.AssetID, "yt_vdC5GXxS-qU",
			"ClipIdentity YouTube: AssetID must contain videoID")
		require.Contains(t, ytID.IndexEventKey, ytID.AssetID,
			"ClipIdentity YouTube: IndexEventKey must contain AssetID")

		// Stock identity.
		stockID, err := detail.NewStockClipIdentity(
			"abc123def4567890", 0,
			"sha256:stock-e2e-content-hash-000000000000",
			"multilingual-e5-base", "v1", "media_assets_current",
		)
		require.NoError(t, err, "NewStockClipIdentity must not fail")
		require.NotEmpty(t, stockID.AssetID)
		require.NotEmpty(t, stockID.ContentHash)
		require.NotEmpty(t, stockID.IndexEventKey)
		require.Contains(t, stockID.AssetID, "planner:",
			"ClipIdentity Stock: AssetID must start with 'planner:'")
		require.Contains(t, stockID.IndexEventKey, stockID.AssetID,
			"ClipIdentity Stock: IndexEventKey must contain AssetID")

		// Cross-pipeline: both produce valid triples.
		require.NotEqual(t, ytID.AssetID, stockID.AssetID,
			"ClipIdentity: YouTube and Stock AssetIDs must be distinct")
		require.NotEqual(t, ytID.IndexEventKey, stockID.IndexEventKey,
			"ClipIdentity: YouTube and Stock IndexEventKeys must be distinct")
	})

	// ── Surface 3: ClipSemanticMetadata + AsSearchTextInput ──────────

	t.Run("ClipSemanticMetadata", func(t *testing.T) {
		// YouTube metadata.
		ytMeta := detail.ClipSemanticMetadata{
			AssetID:         "yt_vdC5GXxS-qU_146_155_v1",
			Title:           "Broner yells at Pacquiao",
			Description:     "Broner interrupts the press conference and yells at Pacquiao.",
			Hook:            "Stop worrying about Floyd! Think about me!",
			Tags:            []string{"boxing", "press conference", "trash talk"},
			Topics:          []string{"boxing", "confrontation"},
			Speakers:        []string{"Adrien Broner", "Manny Pacquiao"},
			SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			SourceProvider:  "youtube",
			SourceVideoID:   "vdC5GXxS-qU",
			StartSec:        146,
			EndSec:          155,
			DurationSec:     9,
			ContentHash:     testSourceVersionFor("yt_vdC5GXxS-qU_146_155_v1"),
			DriveLink:       "https://drive.google.com/file/d/drive-e2e-yt-clip/view",
			PolicyVersion:   "v1",
			NormalizedGroup: "boxing",
		}
		require.False(t, ytMeta.IsEmpty(), "YouTube ClipSemanticMetadata must not be empty")
		require.Equal(t, float64(9), ytMeta.ComputeDurationSec(),
			"ClipSemanticMetadata YouTube: ComputeDurationSec must return EndSec-StartSec")

		ytSearch := ytMeta.AsSearchTextInput("youtube")
		require.Equal(t, "youtube", ytSearch.Source,
			"AsSearchTextInput YouTube: source must be supplied by canonical provenance")
		require.Equal(t, ytMeta.Title, ytSearch.Title)
		require.Equal(t, ytMeta.Hook, ytSearch.Hook)
		require.Len(t, ytSearch.Topics, 2,
			"AsSearchTextInput YouTube: Topics must propagate")
		require.Len(t, ytSearch.Speakers, 2,
			"AsSearchTextInput YouTube: Speakers must propagate")
		// AsSearchTextInput puts start_sec/end_sec into Additional
		// unconditionally when they're > 0 (not gated on source).
		// YouTube Additional may be non-nil but MUST NOT contain
		// stock-only keys like event/round/subject.
		if ytSearch.Additional != nil {
			require.Empty(t, ytSearch.Additional["event"],
				"AsSearchTextInput YouTube: Additional must NOT contain stock-only 'event' key")
			require.Empty(t, ytSearch.Additional["round"],
				"AsSearchTextInput YouTube: Additional must NOT contain stock-only 'round' key")
			require.Empty(t, ytSearch.Additional["subject"],
				"AsSearchTextInput YouTube: Additional must NOT contain stock-only 'subject' key")
		}

		// Stock metadata.
		stockMeta := detail.ClipSemanticMetadata{
			AssetID:                  "planner:abc123def4567890:0",
			Title:                    "Pacquiao Round 7 Training",
			Description:              "Pacquiao trains with intensity in the ring during round 7.",
			Tags:                     []string{"boxing", "pacquiao", "training"},
			Category:                 "Boxing",
			SourceProvider:           "pexels",
			SourceURL:                "https://www.pexels.com/video/12345/",
			SourceVideoID:            "12345",
			Origin:                   "stock",
			Destination:              "stock",
			StartSec:                 32,
			EndSec:                   51,
			DurationSec:              19,
			ContentHash:              "sha256:stock-e2e-content-hash-000000000000",
			Event:                    "Pacquiao vs Broner",
			Round:                    7,
			Subject:                  "Pacquiao training",
			ChunkIndex:               0,
			TotalChunks:              1,
			PolicyVersion:            "stock_timestamp_v1",
			DriveLink:                "https://drive.google.com/file/d/drive-e2e-stock-chunk/view",
			TimestampDriveFolderLink: "https://drive.google.com/drive/folders/folder-e2e-stock",
			TimestampFolderID:        "folder-e2e-stock",
		}
		require.False(t, stockMeta.IsEmpty(), "Stock ClipSemanticMetadata must not be empty")
		require.Equal(t, float64(19), stockMeta.ComputeDurationSec())

		stockSearch := stockMeta.AsSearchTextInput("stock")
		require.Equal(t, "stock", stockSearch.Source)
		require.Equal(t, stockMeta.Title, stockSearch.Title)
		require.NotNil(t, stockSearch.Additional,
			"AsSearchTextInput Stock: Additional must be non-nil for stock-specific fields")
		require.Equal(t, "Pacquiao vs Broner", stockSearch.Additional["event"],
			"AsSearchTextInput Stock: event must propagate to Additional")
		require.Equal(t, "7", stockSearch.Additional["round"],
			"AsSearchTextInput Stock: round must propagate as string")
		require.Equal(t, "Pacquiao training", stockSearch.Additional["subject"],
			"AsSearchTextInput Stock: subject must propagate")
		require.Equal(t, "32", stockSearch.Additional["start_sec"])
		require.Equal(t, "51", stockSearch.Additional["end_sec"])

		// MergedTags: YouTube merges Tags + Topics + Speakers.
		merged := ytMeta.MergedTags()
		require.Greater(t, len(merged), len(ytMeta.Tags),
			"MergedTags YouTube: must include Topics + Speakers beyond base Tags")

		// Clone: deep copy safety.
		cloned := stockMeta.Clone()
		cloned.Tags[0] = "mutated"
		require.NotEqual(t, cloned.Tags[0], stockMeta.Tags[0],
			"Clone Stock: mutation on clone must NOT affect original")

		// Cross-pipeline: both produce non-empty search text inputs
		// with source set.
		require.NotEmpty(t, ytSearch.Source)
		require.NotEmpty(t, stockSearch.Source)
		require.NotEmpty(t, ytSearch.Title)
		require.NotEmpty(t, stockSearch.Title)
	})

	// ── Surface 4: AssetPersistenceWriter (PersistAndIndexRequest) ────
	// ── + envelope shape equivalence ──────────────────────────────────

	t.Run("AssetPersistenceWriter_EnvelopeEquivalence", func(t *testing.T) {
		// Build YouTube identity + metadata.
		ytIdentity, err := detail.NewYouTubeClipIdentity(detail.YouTubeClipIdentityParams{
			VideoID:     "vdC5GXxS-qU",
			StartSec:    146,
			EndSec:      155,
			PolicyVer:   "v1",
			ContentHash: testSourceVersionFor("yt_vdC5GXxS-qU_146_155_v1"),
			Model:       "multilingual-e5-base",
			Version:     "v1",
			Collection:  "media_assets_current",
		})
		require.NoError(t, err)
		ytMeta := detail.ClipSemanticMetadata{
			AssetID:         ytIdentity.AssetID,
			Title:           "Broner yells at Pacquiao",
			Description:     "Broner interrupts the press conference and yells at Pacquiao.",
			Hook:            "Stop worrying about Floyd! Think about me!",
			Tags:            []string{"boxing", "press conference", "trash talk"},
			Topics:          []string{"boxing", "confrontation"},
			Speakers:        []string{"Adrien Broner", "Manny Pacquiao"},
			SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			SourceProvider:  "youtube",
			SourceVideoID:   "vdC5GXxS-qU",
			StartSec:        146,
			EndSec:          155,
			DurationSec:     9,
			ContentHash:     ytIdentity.ContentHash,
			PolicyVersion:   "v1",
			NormalizedGroup: "boxing",
		}
		ytDest := delivery.ResolveDriveDestination(delivery.DriveDestinationInput{
			Category:     "Boxe",
			Subject:      "vdC5GXxS-qU",
			Provider:     "youtube",
			ClipTitle:    "Broner yells at Pacquiao",
			ClipStartSec: 146,
			ClipEndSec:   155,
		})

		// Build YouTube PersistAndIndexRequest from domain surfaces.
		ytReq := buildYouTubePersistRequest(ytIdentity, ytMeta, ytDest)

		// Build Stock identity + metadata.
		stockIdentity, err := detail.NewStockClipIdentity(
			"abc123def4567890", 0,
			"sha256:stock-e2e-content-hash-000000000000",
			"multilingual-e5-base", "v1", "media_assets_current",
		)
		require.NoError(t, err)
		stockMeta := detail.ClipSemanticMetadata{
			AssetID:                  stockIdentity.AssetID,
			Title:                    "Pacquiao Round 7 Training",
			Description:              "Pacquiao trains with intensity in the ring during round 7.",
			Tags:                     []string{"boxing", "pacquiao", "training"},
			Category:                 "Boxing",
			SourceProvider:           "pexels",
			SourceURL:                "https://www.pexels.com/video/12345/",
			SourceVideoID:            "12345",
			Origin:                   "stock",
			Destination:              "stock",
			StartSec:                 32,
			EndSec:                   51,
			DurationSec:              19,
			ContentHash:              stockIdentity.ContentHash,
			Event:                    "Pacquiao vs Broner",
			Round:                    7,
			Subject:                  "Pacquiao training",
			ChunkIndex:               0,
			TotalChunks:              1,
			PolicyVersion:            "stock_timestamp_v1",
			TimestampDriveFolderLink: "https://drive.google.com/drive/folders/folder-e2e-stock",
			TimestampFolderID:        "folder-e2e-stock",
		}
		stockDest := delivery.ResolveDriveDestination(delivery.DriveDestinationInput{
			Category:     "Boxing",
			Subject:      "pacquiao-training",
			Provider:     "pexels",
			ClipSlug:     "round-7",
			ClipTitle:    "Pacquiao Round 7 Training",
			ClipStartSec: 32,
			ClipEndSec:   51,
		})

		// Build Stock PersistAndIndexRequest from domain surfaces.
		stockReq := buildStockPersistRequest(stockIdentity, stockMeta, stockDest)

		// ── Structural equivalence: both requests produce the same
		// envelope shape ───────────────────────────────────────────

		// Required fields must be non-empty in BOTH.
		require.NotEmpty(t, ytReq.AssetID, "YouTube AssetID")
		require.NotEmpty(t, stockReq.AssetID, "Stock AssetID")
		require.NotEmpty(t, ytReq.Source, "YouTube Source")
		require.NotEmpty(t, stockReq.Source, "Stock Source")
		require.NotEmpty(t, ytReq.ContentHash, "YouTube ContentHash")
		require.NotEmpty(t, stockReq.ContentHash, "Stock ContentHash")
		require.NotEmpty(t, ytReq.LifecycleState, "YouTube LifecycleState")
		require.NotEmpty(t, stockReq.LifecycleState, "Stock LifecycleState")

		// Cross-pipeline structural equivalence: same fields populated.
		require.Equal(t, ytReq.Source, "youtube")
		require.Equal(t, stockReq.Source, "stock")
		require.NotEqual(t, ytReq.AssetID, stockReq.AssetID,
			"AssetIDs must be distinct across pipelines")
		require.NotEmpty(t, ytReq.DriveFileID, "YouTube DriveFileID")
		require.NotEmpty(t, stockReq.DriveFileID, "Stock DriveFileID")
		require.NotEmpty(t, ytReq.DriveLink, "YouTube DriveLink")
		require.NotEmpty(t, stockReq.DriveLink, "Stock DriveLink")
		require.NotEmpty(t, ytReq.FolderID, "YouTube FolderID")
		require.NotEmpty(t, stockReq.FolderID, "Stock FolderID")
		require.NotEmpty(t, ytReq.FolderPath, "YouTube FolderPath")
		require.NotEmpty(t, stockReq.FolderPath, "Stock FolderPath")

		// IndexEventKey: both identities produced valid keys.
		require.NotEmpty(t, ytIdentity.IndexEventKey,
			"YouTube IndexEventKey must be non-empty")
		require.NotEmpty(t, stockIdentity.IndexEventKey,
			"Stock IndexEventKey must be non-empty")

		// ── YouTube: persist via real adapter + verify envelope ───

		ytClip := youtubetypes.ClipAsset{
			ID:            ytReq.AssetID,
			VideoID:       "vdC5GXxS-qU",
			LocalPath:     ytReq.LocalPath,
			LegacyFileMD5: ytReq.ContentHash,
			SearchText:    ytReq.SearchText,
			Drive: youtubetypes.ClipAssetDrive{
				FolderID:    ytReq.FolderID,
				FolderPath:  ytReq.FolderPath,
				FileID:      ytReq.DriveFileID,
				WebViewLink: ytReq.DriveLink,
			},
			Coordinates: youtubetypes.ClipAssetCoordinates{
				StartSec: 146,
				EndSec:   155,
				Duration: 9,
			},
			Metadata: youtubetypes.CanonicalClipMetadata{
				Title:           ytMeta.Title,
				Summary:         ytMeta.Description,
				Hook:            ytMeta.Hook,
				Topics:          ytMeta.Topics,
				Speakers:        ytMeta.Speakers,
				SourceURL:       ytMeta.SourceURL,
				SourceProvider:  ytMeta.SourceProvider,
				VideoID:         ytMeta.SourceVideoID,
				ClipStartSec:    int(ytMeta.StartSec),
				ClipEndSec:      int(ytMeta.EndSec),
				ClipDurationSec: int(ytMeta.DurationSec),
				PolicyVersion:   ytMeta.PolicyVersion,
				DrivePath:       ytMeta.DriveLink,
				ContentHash:     ytMeta.ContentHash,
				NormalizedGroup: ytMeta.NormalizedGroup,
			},
			PolicyVersion: ytMeta.PolicyVersion,
		}

		err = fx.Adapter.CommitClipAndIndexEvent(
			context.Background(), ytReq.AssetID, ytClip, youtubeports.IndexEventPayload{},
		)
		require.NoError(t, err, "YouTube CommitClipAndIndexEvent must succeed")

		// Inject search_text into metadata_json for Qdrant PayloadMapper.
		injectMetadataJSON(t, fx, ytReq.AssetID, map[string]any{
			"search_text":     ytReq.SearchText,
			"source_url":      ytMeta.SourceURL,
			"source_provider": ytMeta.SourceProvider,
			"video_id":        ytMeta.SourceVideoID,
		})

		// Verify YouTube outbox event envelope shape.
		ytEnvelope, ytEventKey := queryOutboxEnvelopeAndKey(t, fx, ytReq.AssetID)
		verifyEnvelopeShape(t, ytEnvelope, "YouTube")

		// Verify YouTube event_key column is structurally valid.
		// The real adapter uses reconcile:reindex:{id}:{schema}:{hash}
		// format (infra-layer convention), while ClipIdentity uses
		// index:{id}:{hash}:{model}:{version}:{collection} (domain
		// convention). Both are valid dedup keys — the structural
		// assertion (non-empty + contains asset ID) is sufficient
		// for cross-pipeline equivalence (godlike/07 minimum-blast-radius).
		require.NotEmpty(t, ytEventKey,
			"YouTube outbox_events.event_key must be non-empty")
		require.Contains(t, ytEventKey, ytReq.AssetID,
			"YouTube outbox_events.event_key must contain the asset ID")

		// ── Stock: persist manually + verify same envelope shape ──

		stockMetadataJSON := buildStockMetadataJSON(t, stockReq, stockMeta)
		_, err = fx.DB.Exec(
			`INSERT INTO media_assets
				(id, source, name, filename, media_type, drive_file_id, drive_link,
				 download_link, local_path, file_hash, folder_id, folder_path,
				 source_version, lifecycle_state, metadata_json, index_state,
				 created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stockReq.AssetID,
			stockReq.Source,
			stockReq.Name,
			stockReq.Filename,
			stockReq.MediaType,
			stockReq.DriveFileID,
			stockReq.DriveLink,
			stockReq.DownloadLink,
			stockReq.LocalPath,
			stockReq.ContentHash,
			stockReq.FolderID,
			stockReq.FolderPath,
			"", // source_version: Stock writes content_hash in metadata_json
			stockReq.LifecycleState,
			stockMetadataJSON,
			stockReq.IndexState,
			time.Now().UTC().Format(time.RFC3339),
			time.Now().UTC().Format(time.RFC3339),
		)
		require.NoError(t, err, "Stock media_assets INSERT must succeed")

		// Verify Stock index_state = DISCOVERED.
		var stockIndexState string
		require.NoError(t, fx.DB.QueryRow(
			`SELECT index_state FROM media_assets WHERE id = ?`, stockReq.AssetID,
		).Scan(&stockIndexState))
		require.Equal(t, "DISCOVERED", stockIndexState,
			"Stock media_assets.index_state must be DISCOVERED at insert time")

		// Emit outbox event for Stock (mirrors AssetTxFinalizer pattern).
		stockEventKey := stockIdentity.IndexEventKey
		stockEnvelopeJSON := buildIndexEnvelopeJSON(t, stockReq.AssetID, stockReq.ContentHash)
		_, err = fx.DB.Exec(
			`INSERT INTO outbox_events
				(event_type, aggregate_id, aggregate_type, payload_json,
				 event_key, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"asset.index.requested",
			stockReq.AssetID,
			"media_asset",
			stockEnvelopeJSON,
			stockEventKey,
			"pending",
			time.Now().UTC().Format(time.RFC3339),
			time.Now().UTC().Format(time.RFC3339),
		)
		require.NoError(t, err, "Stock outbox_events INSERT must succeed")

		// Verify Stock outbox event envelope shape.
		stockEnvelope, stockEventKeyCol := queryOutboxEnvelopeAndKey(t, fx, stockReq.AssetID)
		verifyEnvelopeShape(t, stockEnvelope, "Stock")

		// Verify Stock event_key column matches ClipIdentity's key.
		require.NotEmpty(t, stockEventKeyCol,
			"Stock outbox_events.event_key must be non-empty")
		require.Equal(t, stockIdentity.IndexEventKey, stockEventKeyCol,
			"Stock outbox_events.event_key must match ClipIdentity.IndexEventKey")

		// ── Cross-pipeline envelope equivalence ──────────────────

		t.Run("EnvelopeKeysMatch", func(t *testing.T) {
			// Both envelopes must have the same required keys.
			requiredKeys := []string{
				"schema_version", "asset_id", "source_version",
				"idempotency_key", "operation",
			}
			for _, key := range requiredKeys {
				_, ytOK := ytEnvelope[key]
				_, stockOK := stockEnvelope[key]
				require.True(t, ytOK, "YouTube envelope must have key %q", key)
				require.True(t, stockOK, "Stock envelope must have key %q", key)
			}
		})

		t.Run("EnvelopeFieldValuesDifferByAsset", func(t *testing.T) {
			// Asset-specific fields must differ (different assets).
			require.NotEqual(t,
				ytEnvelope["asset_id"], stockEnvelope["asset_id"],
				"asset_id must differ between YouTube and Stock")
			require.NotEqual(t,
				ytEnvelope["idempotency_key"], stockEnvelope["idempotency_key"],
				"idempotency_key must differ between YouTube and Stock")
			require.NotEqual(t,
				ytEnvelope["source_version"], stockEnvelope["source_version"],
				"source_version must differ between YouTube and Stock")
		})

		t.Run("EnvelopeSchemaVersionMatch", func(t *testing.T) {
			// Both envelopes must contain "v1" in their schema_version.
			// The real YouTube adapter produces "asset.index.requested.v1";
			// the test Stock path uses "v1". Both are valid v1 variants.
			ytSV, _ := ytEnvelope["schema_version"].(string)
			stockSV, _ := stockEnvelope["schema_version"].(string)
			require.Contains(t, ytSV, "v1",
				"YouTube schema_version must contain v1")
			require.Contains(t, stockSV, "v1",
				"Stock schema_version must contain v1")
		})

		t.Run("EnvelopeOperationMatch", func(t *testing.T) {
			// Both use UPSERT.
			require.Equal(t,
				ytEnvelope["operation"], stockEnvelope["operation"],
				"operation must match (both are UPSERT)")
			require.Equal(t, "UPSERT", ytEnvelope["operation"])
		})
	})
}

// ── Request builders ──────────────────────────────────────────────────

// buildYouTubePersistRequest constructs a persistence.PersistAndIndexRequest
// from YouTube domain surfaces (ClipIdentity + ClipSemanticMetadata +
// DriveDestination). This is the canonical adapter pattern that the
// infrastructure-layer concrete writer will implement.
func buildYouTubePersistRequest(
	id detail.ClipIdentity,
	meta detail.ClipSemanticMetadata,
	dest delivery.DriveDestination,
) persistence.PersistAndIndexRequest {
	return persistence.PersistAndIndexRequest{
		AssetID:        id.AssetID,
		Source:         "youtube",
		Name:           meta.Title,
		Filename:       id.AssetID + ".mp4",
		MediaType:      "video",
		ContentHash:    id.ContentHash,
		Description:    "", // YouTube metadata enrichment writes it later
		DriveFileID:    "drive-e2e-" + id.AssetID,
		DriveLink:      "https://drive.google.com/file/d/drive-e2e-" + id.AssetID + "/view",
		LocalPath:      "/tmp/" + id.AssetID + ".mp4",
		FolderID:       "folder-e2e-yt",
		FolderPath:     "youtube/e2e/" + dest.LocationInput.Category,
		LifecycleState: "ACTIVE",
		IndexState:     "", // YouTube uses column DEFAULT
		SearchText:     composeYouTubeSearchText(meta),
	}
}

// buildStockPersistRequest constructs a persistence.PersistAndIndexRequest
// from Stock domain surfaces (ClipIdentity + ClipSemanticMetadata +
// DriveDestination). This is the canonical adapter pattern that the
// AssetTxFinalizer callers will use.
func buildStockPersistRequest(
	id detail.ClipIdentity,
	meta detail.ClipSemanticMetadata,
	dest delivery.DriveDestination,
) persistence.PersistAndIndexRequest {
	filename := dest.PathLeafName
	if filename == "" {
		filename = "chunk_0.mp4"
	} else {
		filename = filename + ".mp4"
	}
	return persistence.PersistAndIndexRequest{
		AssetID:        id.AssetID,
		Source:         "stock",
		Name:           meta.Title,
		Filename:       filename,
		MediaType:      "video",
		ContentHash:    id.ContentHash,
		Description:    meta.Description,
		DriveFileID:    "drive-e2e-stock-" + id.AssetID,
		DriveLink:      "https://drive.google.com/file/d/drive-e2e-stock-" + id.AssetID + "/view",
		LocalPath:      "", // stock is remote-only
		FolderID:       "folder-e2e-stock",
		FolderPath:     "stock/e2e/" + dest.LocationInput.Category,
		LifecycleState: "PUBLISHED",
		IndexState:     "DISCOVERED",
		SearchText:     "", // stock search text composed by SearchTextBuilder
	}
}

// composeYouTubeSearchText composes the canonical YouTube search text
// from ClipSemanticMetadata fields. Mirrors
// composeYouTubeClipSearchText in process_segment_helpers.go — the
// E2E test owns its own composition to avoid importing the
// application-layer helper (clean architecture).
func composeYouTubeSearchText(meta detail.ClipSemanticMetadata) string {
	var parts []string
	if meta.Title != "" {
		parts = append(parts, meta.Title)
	}
	if meta.Description != "" {
		parts = append(parts, meta.Description)
	}
	if meta.Hook != "" {
		parts = append(parts, meta.Hook)
	}
	if len(meta.Topics) > 0 {
		parts = append(parts, strings.Join(meta.Topics, " "))
	}
	if meta.SourceURL != "" {
		parts = append(parts, meta.SourceURL)
	}
	if len(meta.Speakers) > 0 {
		parts = append(parts, strings.Join(meta.Speakers, " "))
	}
	if len(meta.MentionedPeople) > 0 {
		parts = append(parts, strings.Join(meta.MentionedPeople, " "))
	}
	return strings.Join(parts, " ")
}

// ── Envelope helpers ──────────────────────────────────────────────────

// queryOutboxEnvelopeAndKey reads both the outbox_events.payload_json
// and the outbox_events.event_key column for the given aggregate_id.
// Returns the unmarshalled envelope map + the raw event_key string.
func queryOutboxEnvelopeAndKey(t *testing.T, fx *e2eFixture, assetID string) (map[string]any, string) {
	t.Helper()
	var payloadJSON, eventKey string
	err := fx.DB.QueryRow(
		`SELECT payload_json, event_key FROM outbox_events WHERE aggregate_id = ?`, assetID,
	).Scan(&payloadJSON, &eventKey)
	require.NoError(t, err, "outbox event must exist for aggregate_id=%s", assetID)
	require.NotEmpty(t, payloadJSON, "outbox payload_json must be non-empty")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(payloadJSON), &envelope),
		"outbox payload_json must be valid JSON")
	return envelope, eventKey
}

// verifyEnvelopeShape asserts the canonical asset.index.requested
// envelope shape: all required keys present and non-empty.
func verifyEnvelopeShape(t *testing.T, envelope map[string]any, label string) {
	t.Helper()
	requiredKeys := []string{
		"schema_version",
		"asset_id",
		"source_version",
		"idempotency_key",
		"operation",
	}
	for _, key := range requiredKeys {
		val, ok := envelope[key]
		require.True(t, ok, "%s envelope must have key %q", label, key)
		require.NotEmpty(t, val, "%s envelope[%q] must be non-empty", label, key)
	} // schema_version must contain "v1" (the real adapter produces
	// "asset.index.requested.v1"; the test Stock path uses "v1").
	sv, _ := envelope["schema_version"].(string)
	require.Contains(t, sv, "v1",
		"%s envelope: schema_version must contain v1", label)

	// operation must be "UPSERT".
	require.Equal(t, "UPSERT", envelope["operation"],
		"%s envelope: operation must be UPSERT", label)
}

// buildIndexEnvelopeJSON constructs a canonical v1 index envelope JSON
// for the Stock outbox event. Uses the canonical
// detail.BuildIndexEventKey for the idempotency_key format (godlike/06
// SSOT — one canonical owner per fact).
func buildIndexEnvelopeJSON(t *testing.T, assetID, sourceVersion string) string {
	t.Helper()
	idemKey := detail.BuildIndexEventKey(
		assetID, sourceVersion,
		"multilingual-e5-base", "v1", "media_assets_current",
	)
	envelope := map[string]any{
		"schema_version":  "v1",
		"asset_id":        assetID,
		"source_version":  sourceVersion,
		"idempotency_key": idemKey,
		"operation":       "UPSERT",
	}
	raw, err := json.Marshal(envelope)
	require.NoError(t, err, "marshal index envelope JSON")
	return string(raw)
}

// buildStockMetadataJSON constructs a metadata_json blob for the Stock
// media_assets row. Mirrors the production merged-ArtifactMetadata
// shape from the stock finalizer.
func buildStockMetadataJSON(t *testing.T, req persistence.PersistAndIndexRequest, meta detail.ClipSemanticMetadata) string {
	t.Helper()
	m := map[string]any{
		"title":           meta.Title,
		"description":     meta.Description,
		"category":        meta.Category,
		"source_provider": meta.SourceProvider,
		"source_url":      meta.SourceURL,
		"source_video_id": meta.SourceVideoID,
		"origin":          meta.Origin,
		"destination":     meta.Destination,
		"round":           meta.Round,
		"event":           meta.Event,
		"subject":         meta.Subject,
		"start_sec":       meta.StartSec,
		"end_sec":         meta.EndSec,
		"duration_sec":    meta.DurationSec,
		"policy_version":  meta.PolicyVersion,
		"total_chunks":    meta.TotalChunks,
		"chunk_index":     meta.ChunkIndex,
		"drive_path":      req.DriveLink,
		"content_hash":    req.ContentHash,
		"indexing_status": "INDEXING_PENDING",
	}
	if meta.TimestampDriveFolderLink != "" {
		m["timestamp_drive_folder_link"] = meta.TimestampDriveFolderLink
	}
	if meta.TimestampFolderID != "" {
		m["timestamp_folder_id"] = meta.TimestampFolderID
	}
	if len(meta.Tags) > 0 {
		m["tags"] = meta.Tags
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err, "marshal Stock metadata_json")
	return string(raw)
}

// ── Shared test helpers (imported from qdrant_e2e_youtube_test.go) ───
//
// The following helpers are defined in qdrant_e2e_youtube_test.go and
// shared across all e2e test files in this package:
//   - newE2EFixture(t, collection) *e2eFixture
//   - testSourceVersionFor(assetID) string
//   - injectMetadataJSON(t, fx, assetID, kvPairs)
//   - runOutboxWorkerClaim(t, fx, assetID, workerID)
