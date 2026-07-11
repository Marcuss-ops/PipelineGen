// Package e2e — Text Track pipeline E2E tests (Passo 10, July 2026).
//
// Hermetic end-to-end tests verifying the full text track pipeline:
//  1. TextTrackResolver resolves transcript from payload Texts[] (Whisper NOT called)
//  2. asset_text_tracks rows created in SQLite
//  3. Qdrant receives multilingual search text via PayloadMapper
//  4. source_version hash changes when text tracks are added
//  5. Backfill: existing clips can get text tracks added retroactively
//
// Uses the canonical e2e fixture stack: in-memory SQLite + mock Qdrant
// REST surrogate + production adapters. TextTrackRepository is wired
// into the PayloadMapper via SetTextTrackQuerier + SetIndexLanguages.
package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	clipwriter "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// textTrackFixture extends e2eFixture with text track components:
// TextTrackRepository (SQLite), TextTrackResolver (usecase), and
// the PayloadMapper wired with TextTrackQuerier + index_languages.
type textTrackFixture struct {
	*e2eFixture
	TTRepo   *clipwriter.TextTrackRepositorySQLite
	Resolver *youtubeusecase.TextTrackResolver
}

// newTextTrackFixture creates a hermetic fixture with text track support.
// The base e2eFixture provides in-memory SQLite + mock Qdrant + production
// adapters. This wrapper adds:
//   - asset_text_tracks table (migration 137 DDL)
//   - TextTrackRepositorySQLite wired to the same in-memory DB
//   - TextTrackResolver for the priority-chain lookup
//   - PayloadMapper wired with TextTrackQuerier + index_languages
func newTextTrackFixture(t *testing.T, collection string) *textTrackFixture {
	t.Helper()
	fx := newE2EFixture(t, collection)

	// Add search_text column (migration 059/062) — the base youTubeE2EDB
	// schema predates this column. Mirror the DoD12 E2E pattern.
	_, err := fx.DB.Exec(`ALTER TABLE media_assets ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`)
	require.NoError(t, err, "search_text column must exist per migration 059/062")

	// Add asset_text_tracks table (migration 137 DDL).
	_, err = fx.DB.Exec(`
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,
    text_content        TEXT NOT NULL DEFAULT '',
    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,
    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',
    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',
    confidence          REAL,
    status              TEXT NOT NULL DEFAULT 'READY',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(asset_id, language_code, text_kind)
)`)
	require.NoError(t, err, "CREATE TABLE asset_text_tracks must succeed")

	// Construct TextTrackRepository from the same in-memory DB.
	ttRepo, err := clipwriter.NewTextTrackRepository(fx.DB, fx.Log)
	require.NoError(t, err, "NewTextTrackRepository must succeed")

	// Wire PayloadMapper with TextTrackQuerier + index_languages so
	// resolveSearchText populates SearchTextInput.TextTracks at
	// indexing time. Mirrors production buildQdrantDeps wiring.
	fx.Mapper.SetTextTrackQuerier(ttRepo)
	fx.Mapper.SetIndexLanguages("en,it")

	// Construct TextTrackResolver for the priority-chain lookup.
	resolver := &youtubeusecase.TextTrackResolver{
		Repo: ttRepo,
		Log:  fx.Log,
	}

	return &textTrackFixture{
		e2eFixture: fx,
		TTRepo:     ttRepo,
		Resolver:   resolver,
	}
}

// ── Test 1: Resolver payload hit + persistence ────────────────────────
//
// Verifies:
//   - TextTrackResolver resolves transcript from payload Texts[] (Priority 1)
//   - Save persists to asset_text_tracks (row created)
//   - TextTrackResolver resolves from DB on second call (Priority 2)
//   - Whisper transcriber is NOT needed (resolver short-circuits)
func TestE2E_TextTrack_ResolverPayloadHitAndPersist(t *testing.T) {
	fx := newTextTrackFixture(t, "media_assets_current")
	ctx := context.Background()
	clipID := "yt_tt_resolve_001"

	// ── Priority 1: resolve from API payload ──────────────────────
	// Use "en" language so the resolver's Priority 2 DB lookup
	// (which defaults to "en") can find it after Save.
	payloadTexts := []youtubetypes.LocalizedClipText{
		{
			LanguageCode: "en",
			Transcript:   "Broner yells at Pacquiao — stop worrying about Floyd",
			SourceType:   "provided",
			IsOriginal:   true,
		},
	}

	result, err := fx.Resolver.Resolve(ctx, clipID, payloadTexts)
	require.NoError(t, err, "Resolve must not error")
	require.True(t, result.Found,
		"Priority 1: resolver must find transcript in payload Texts[]")
	require.Equal(t, "Broner yells at Pacquiao — stop worrying about Floyd", result.Transcript,
		"Priority 1: transcript must match payload text")
	require.Equal(t, "en", result.LanguageCode,
		"Priority 1: language must match payload language_code")
	require.Equal(t, asset.TextSourceProvided, result.Source,
		"Priority 1: source must be 'provided' (from payload)")

	// ── Save persists to asset_text_tracks ────────────────────────
	err = fx.Resolver.Save(ctx, clipID, result.Transcript, result.Source, result.LanguageCode)
	require.NoError(t, err, "Save must succeed")

	// Verify DB row.
	track, err := fx.TTRepo.Find(ctx, clipID, "en", asset.TextTrackTranscript)
	require.NoError(t, err, "Find must not error")
	require.NotNil(t, track, "asset_text_tracks row must exist after Save")
	require.Equal(t, "Broner yells at Pacquiao — stop worrying about Floyd", track.TextContent,
		"text_content must match saved transcript")
	require.Equal(t, asset.TextSourceProvided, track.SourceType,
		"source_type must be 'provided'")
	require.Equal(t, asset.TextTrackReady, track.Status,
		"status must be READY")
	require.True(t, track.IsOriginal,
		"is_original must be true for provided text")

	// ── Priority 2: resolve from DB (no payload needed) ───────────
	// Resolver defaults to "en" for DB lookup when no payload texts
	// are provided. Since we saved with "en", this will find it.
	result2, err := fx.Resolver.Resolve(ctx, clipID, nil)
	require.NoError(t, err, "Resolve from DB must not error")
	require.True(t, result2.Found,
		"Priority 2: resolver must find transcript in DB")
	require.Equal(t, "Broner yells at Pacquiao — stop worrying about Floyd", result2.Transcript,
		"Priority 2: transcript must match DB content")
	require.Equal(t, "en", result2.LanguageCode,
		"Priority 2: language must match DB language_code")

	// ── Empty payload + empty DB → Found=false ────────────────────
	result3, err := fx.Resolver.Resolve(ctx, "yt_nonexistent_clip", nil)
	require.NoError(t, err, "Resolve for missing clip must not error")
	require.False(t, result3.Found,
		"Resolver must return Found=false for clip with no payload and no DB row")
}

// ── Test 2: Qdrant receives multilingual search text ──────────────────
//
// Verifies the full chain: commit clip → insert text tracks → run
// outbox worker → Qdrant payload.search_text contains multilingual
// transcripts from the TextTrackQuerier.
func TestE2E_TextTrack_MultilingualSearchText(t *testing.T) {
	fx := newTextTrackFixture(t, "media_assets_current")
	ctx := context.Background()
	clipID := "yt_tt_multi_001"

	// Commit clip via production adapter.
	require.NoError(t, commitYouTubeClip(t, fx.e2eFixture, clipID,
		"Multilingual test clip — Broner Pacquiao",
		"vid_multi_001",
	))

	// Inject transcript into metadata_json (production shape:
	// Whisper/subtitle stage writes here after the writer commits).
	injectMetadataJSON(t, fx.e2eFixture, clipID, map[string]any{
		"transcript":      "English transcript: Broner yells at Pacquiao during press conference",
		"language":        "en",
		"source_url":      "https://www.youtube.com/watch?v=vid_multi_001",
		"source_provider": "youtube",
	})

	// Insert text tracks: en (same as main transcript — will be
	// deduplicated by youtubeStrategy) + it (different — will be
	// concatenated into search_text).
	require.NoError(t, fx.TTRepo.UpsertBatch(ctx, []asset.TextTrack{
		{
			AssetID:      clipID,
			LanguageCode: "en",
			TextKind:     asset.TextTrackTranscript,
			TextContent:  "English transcript: Broner yells at Pacquiao during press conference",
			SourceType:   asset.TextSourceProvided,
			IsOriginal:   true,
			Status:       asset.TextTrackReady,
		},
		{
			AssetID:      clipID,
			LanguageCode: "it",
			TextKind:     asset.TextTrackTranscript,
			TextContent:  "Trascrizione italiana: Broner urla contro Pacquiao durante la conferenza stampa",
			SourceType:   asset.TextSourceTranslation,
			Status:       asset.TextTrackReady,
		},
	}), "UpsertBatch must succeed for en + it text tracks")

	// Run outbox worker: claim → UpsertFromClip → complete.
	// The PayloadMapper's resolveSearchText will:
	// 1. Build SearchTextInput from AssetData
	// 2. Inject index_languages="en,it" into Additional
	// 3. Fetch TextTracks from DB via TextTrackQuerier
	// 4. Call SearchTextBuilder.Build → youtubeStrategy concatenates
	runOutboxWorkerClaim(t, fx.e2eFixture, clipID, "worker-multi-001")

	// Verify Qdrant payload.
	raw := fx.Qdrant.findUpserted(t, clipID)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload),
		"Qdrant payload must be valid JSON")

	// search_text must contain the Italian transcript (en is deduplicated
	// against the main transcript, it is appended).
	searchText, ok := payload["search_text"].(string)
	require.True(t, ok, "search_text must be a string in Qdrant payload")
	require.Contains(t, searchText, "Trascrizione italiana",
		"search_text must contain Italian transcript from TextTracks")

	// Verify source and lifecycle_state are correct.
	require.Equal(t, "youtube", payload["source"],
		"Qdrant payload.source must be 'youtube'")
	require.Equal(t, "ACTIVE", payload["lifecycle_state"],
		"Qdrant payload.lifecycle_state must be ACTIVE")
}

// ── Test 3: source_version hash changes with text tracks ──────────────
//
// Verifies ComputeContentHashWithTextTracks:
//   - Empty tracks → returns base hash unchanged
//   - Non-empty tracks → returns different hash
//   - Same tracks in different order → same hash (determinism)
func TestE2E_TextTrack_SourceVersionHash(t *testing.T) {
	baseHash := "sha256:abcdef1234567890abcdef1234567890"

	// Empty tracks → same hash.
	hash0 := clipwriter.ComputeContentHashWithTextTracks(baseHash, nil)
	require.Equal(t, baseHash, hash0,
		"empty tracks must return base hash unchanged")

	// Empty slice → same hash.
	hash0b := clipwriter.ComputeContentHashWithTextTracks(baseHash, []asset.TextTrack{})
	require.Equal(t, baseHash, hash0b,
		"empty track slice must return base hash unchanged")

	// With tracks → different hash.
	tracks := []asset.TextTrack{
		{LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextHash: "sha256:en_transcript_hash"},
		{LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextHash: "sha256:it_transcript_hash"},
	}
	hash1 := clipwriter.ComputeContentHashWithTextTracks(baseHash, tracks)
	require.NotEqual(t, baseHash, hash1,
		"text tracks must change the hash")
	require.NotEmpty(t, hash1,
		"computed hash must not be empty")

	// Same tracks in reversed order → same hash (determinism).
	tracksReversed := []asset.TextTrack{
		{LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextHash: "sha256:it_transcript_hash"},
		{LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextHash: "sha256:en_transcript_hash"},
	}
	hash2 := clipwriter.ComputeContentHashWithTextTracks(baseHash, tracksReversed)
	require.Equal(t, hash1, hash2,
		"hash must be deterministic regardless of track order")

	// Single track → different from two tracks.
	singleTrack := []asset.TextTrack{
		{LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextHash: "sha256:en_transcript_hash"},
	}
	hash3 := clipwriter.ComputeContentHashWithTextTracks(baseHash, singleTrack)
	require.NotEqual(t, hash1, hash3,
		"different track sets must produce different hashes")
	require.NotEqual(t, baseHash, hash3,
		"single track must still change the hash")
}

// ── Test 4: Backfill existing clips ───────────────────────────────────
//
// Verifies that adding text tracks to an existing clip (committed
// without text tracks) changes the source_version, which would
// trigger re-indexing in production.
func TestE2E_TextTrack_Backfill(t *testing.T) {
	fx := newTextTrackFixture(t, "media_assets_current")
	ctx := context.Background()
	clipID := "yt_tt_backfill_001"

	// Commit clip WITHOUT text tracks.
	require.NoError(t, commitYouTubeClip(t, fx.e2eFixture, clipID,
		"Backfill test clip",
		"vid_backfill_001",
	))

	// Get initial source_version from DB.
	var initialSV string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT source_version FROM media_assets WHERE id = ?`, clipID,
	).Scan(&initialSV))
	require.NotEmpty(t, initialSV,
		"initial source_version must be non-empty")

	// Verify no text tracks exist yet.
	tracks, err := fx.TTRepo.ListByAsset(ctx, clipID)
	require.NoError(t, err)
	require.Empty(t, tracks,
		"no text tracks must exist before backfill")

	// Compute hash with no tracks → same as base.
	hashNoTracks := clipwriter.ComputeContentHashWithTextTracks(initialSV, nil)
	require.Equal(t, initialSV, hashNoTracks,
		"hash with no tracks must equal base source_version")

	// ── Backfill: add text tracks retroactively ───────────────────
	// Languages must match index_languages ("en,it") so the
	// youtubeStrategy includes them in search_text.
	require.NoError(t, fx.TTRepo.UpsertBatch(ctx, []asset.TextTrack{
		{
			AssetID:      clipID,
			LanguageCode: "it",
			TextKind:     asset.TextTrackTranscript,
			TextContent:  "Trascrizione italiana: Broner grida contro Pacquiao",
			SourceType:   asset.TextSourceTranslation,
			TextHash:     "sha256:it_transcript_hash",
			Status:       asset.TextTrackReady,
		},
	}), "UpsertBatch must succeed for backfilled tracks")

	// Verify tracks now exist.
	tracks, err = fx.TTRepo.ListByAsset(ctx, clipID)
	require.NoError(t, err)
	require.Len(t, tracks, 1,
		"1 text track must exist after backfill")

	// Compute new hash with backfilled tracks.
	newHash := clipwriter.ComputeContentHashWithTextTracks(initialSV, tracks)
	require.NotEqual(t, initialSV, newHash,
		"source_version MUST change after backfilling text tracks")
	require.NotEmpty(t, newHash,
		"new hash must not be empty")

	// Verify determinism: same tracks → same hash.
	newHash2 := clipwriter.ComputeContentHashWithTextTracks(initialSV, tracks)
	require.Equal(t, newHash, newHash2,
		"backfill hash must be deterministic")

	// ── Simulate re-index: update source_version and re-upsert ────
	// In production, the changed source_version triggers a new outbox
	// event. Here we verify the Qdrant payload reflects the updated
	// search text after re-upsert.
	_, err = fx.DB.Exec(
		`UPDATE media_assets SET source_version = ? WHERE id = ?`,
		newHash, clipID,
	)
	require.NoError(t, err, "update source_version must succeed")

	// Re-upsert to Qdrant with the new source_version.
	require.NoError(t, fx.Writer.UpsertFromClip(ctx, clipID),
		"re-upsert after backfill must succeed")

	// Verify Qdrant payload has updated search_text including the
	// backfilled French transcript.
	raw := fx.Qdrant.findUpserted(t, clipID)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))

	searchText, ok := payload["search_text"].(string)
	require.True(t, ok, "search_text must be a string")
	require.Contains(t, searchText, "Trascrizione italiana",
		"search_text must contain backfilled Italian transcript")
}
