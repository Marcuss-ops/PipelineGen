package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	clipwriter "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Resolver, multilingual search-text, source-version, and backfill cases.

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

	// Fase 1.b typed method: ResolveOriginal returns a non-nil
	// *asset.ResolvedTextBundle on payload hit. The legacy envelope
	// shape (result.Found/Transcript/LanguageCode/Source) is
	// replaced by bundle field access (PlainText/LanguageCode/SourceType).
	bundle, err := fx.Resolver.ResolveOriginal(ctx, clipID, payloadTexts)
	require.NoError(t, err, "ResolveOriginal must not error")
	require.NotNil(t, bundle,
		"Priority 1: resolver must find transcript in payload Texts[]")
	require.Equal(t, "Broner yells at Pacquiao — stop worrying about Floyd", bundle.PlainText,
		"Priority 1: transcript must match payload text")
	require.Equal(t, "en", bundle.LanguageCode,
		"Priority 1: language must match payload language_code")
	require.Equal(t, asset.TextSourceProvided, bundle.SourceType,
		"Priority 1: source must be 'provided' (from payload)")

	// ── Save persists to asset_text_tracks ────────────────────────
	// Save signature is UNCHANGED: (ctx, clipID, transcript, source, languageCode).
	err = fx.Resolver.Save(ctx, clipID, bundle.PlainText, bundle.SourceType, bundle.LanguageCode)
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
	// Fase 1.b: ResolveBestAvailable is the canonical typed lookup.
	row2, err := fx.Resolver.ResolveBestAvailable(ctx, clipID, []string{"en"}, asset.TextTrackTranscript)
	require.NoError(t, err, "ResolveBestAvailable from DB must not error")
	require.NotNil(t, row2,
		"Priority 2: resolver must find transcript in DB")
	require.Equal(t, "Broner yells at Pacquiao — stop worrying about Floyd", row2.TextContent,
		"Priority 2: transcript must match DB content")
	require.Equal(t, "en", row2.LanguageCode,
		"Priority 2: language must match DB language_code")

	// ── Empty payload + empty DB → nil row ────────────────────────
	// Fase 1.b: a non-existent clipID returns (nil, nil) from
	// ResolveBestAvailable (no DB row, no payload, no Whisper).
	row3, err := fx.Resolver.ResolveBestAvailable(ctx, "yt_nonexistent_clip", []string{"en"}, asset.TextTrackTranscript)
	require.NoError(t, err, "ResolveBestAvailable for missing clip must not error")
	require.Nil(t, row3,
		"Resolver must return nil for clip with no payload and no DB row")
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

// ─────────────────────────────────────────────────────────────────────────────
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 e2e: text-track job pipeline (July 2026).
//
// Hermetic end-to-end probe of the broker + job-handler + outbox chain for
// the asset.text.materialize job. Pins observable state across three
// SQLite tables in one round-trip:
//
//   1. `jobs`                  — Enqueue writes the row; broker rejects
//                                   unregistered types; ActiveKey dedup
//                                   collapses repeated enqueues.
//   2. `asset_text_tracks`     — Materializer reads the source READY
//                                   track, fans out per target language,
//                                   writes new READY rows for [it, es, fr]
//                                   (or whatever the test configures).
//   3. `outbox_events`          — Materializer emits ONE asset.index.requested
//                                   event with payload {asset_id, kind, reason}.
//
// HERMETIC BOUNDARY
//   - SQLite in-memory (`:memory:`); `media_assets` + `outbox_events`
//     + `asset_text_tracks` + `jobs` tables; no external IO.
//   - Stub TranslationPort — deterministic `[lang] text` + 0.85
//     confidence; matches the canonical TranslationPort surface so the
//     production Ollama adapter can drop in.
//   - The in-process broker poll loop (Worker.Start) is bypassed: we
//     invoke dispatcher.Dispatch directly on the same production
//     dispatcher the broker uses to hand the job to the worker. The
//     Hand-off observation (jobs row + handler dispatch + DB state
//     changes) is the load-bearing e2e boundary; the broker Claim/
//     Renew/Complete cycle is already pinned by
//     internal/infrastructure/jobs/local/broker_test.go + the
//     in-package repository_claims_test.go.
//   - Qdrant (IndexWriter) is OUT of scope; this probe stops at the
//     durable outbox-events row, which is the canonical pre-Qdrant
//     seam per outboxevents.Repository.Enqueue contract.
//
// FAIL-CLOSED: every "must" assertion surfaces as test failure with
// the exact field name + actual-vs-expected witness. Tests are
// hermetic (sub-second; no Qdrant/Drive/HTTP); they pin the canonical
// production wiring so future drift in the broker surface, the job
// payload shape, or the outbox event payload is a test failure here.
// ─────────────────────────────────────────────────────────────────────────────
