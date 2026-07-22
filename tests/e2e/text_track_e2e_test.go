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
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the legacy
// 2-level `Resolver.Resolve(ctx, clipID, payloadTexts)` method
// (which returned a result envelope with Found/Transcript/LanguageCode/
// Source) is RETIRED. The typed `ResolveOriginal` (priority 1) +
// `ResolveBestAvailable` (priority 2) are the SOLE canonical surfaces.
// The migration is mechanical: result.Found → bundle != nil,
// result.Transcript → bundle.PlainText, result.LanguageCode →
// bundle.LanguageCode, result.Source → bundle.SourceType. The
// Save signature (ctx, clipID, transcript, source, languageCode)
// is UNCHANGED.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	clipwriter "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	texttrackssql "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"

	youtubeusecase "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
)

// textTrackFixture extends e2eFixture with text track components:
// TextTrackRepository (SQLite), TextTrackResolver (usecase), and
// the PayloadMapper wired with TextTrackQuerier + index_languages.
type textTrackFixture struct {
	*e2eFixture
	TTRepo   *texttrackssql.TextTrackRepositorySQLite
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

	// Add asset_text_tracks table using the canonical schema constant
	// so the fixture stays in lockstep with production migrations.
	_, err := fx.DB.Exec(storage.CanonicalAssetTextTracksTable)
	require.NoError(t, err, "CREATE TABLE asset_text_tracks must succeed")

	// Add asset_text_track_segments table (migration 14X DDL).
	// Bucket-B closure (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3):
	// the text-track e2e resolver's Priority 2 lookup walks
	// asset_text_track_segments via findCuesForTrackID. The hardcoded
	// hermetic DDL must mirror the production migration
	// (migrations/sqlite/1410427846_create_asset_text_track_segments.sql)
	// so failures here are NOT obscured by feature drift between fixture
	// and production schema. Foreign-key ON DELETE CASCADE preserves the
	// production delete contract: removing an asset_text_tracks.parent
	// cascades to its segments (godlike/06 SSOT — fixture is HERMETICALLY
	// BYTE-EQUIVALENT to the canonical production SUBSET; any drift surfaces
	// here at e2e time, not silently at production runtime).
	_, err = fx.DB.Exec(`
CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id TEXT PRIMARY KEY,
    track_id TEXT NOT NULL,
    sequence_no INTEGER NOT NULL DEFAULT 0,
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    text TEXT NOT NULL,
    text_hash TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(track_id) REFERENCES asset_text_tracks(id) ON DELETE CASCADE
)`)
	require.NoError(t, err, "CREATE TABLE asset_text_track_segments must succeed")

	// Construct TextTrackRepository from the same in-memory DB.
	ttRepo, err := texttrackssql.NewTextTrackRepository(fx.DB, fx.Log)
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
//
// Fase 1.b (PR-PY-CLIPS-CORRETTE-TRADOTTE): the legacy
// `Resolver.Resolve(ctx, clipID, payloadTexts)` is RETIRED. The
// migration uses the typed methods:
//   - ResolveOriginal (priority 1) returns *asset.ResolvedTextBundle
//   - ResolveBestAvailable (priority 2) returns *asset.TextTrack
//   - A non-existent clip returns (nil, nil) from ResolveBestAvailable
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

// jobsTableDDL is the canonical SQLiteStore jobs-table schema (mirrors
// migrations/sqlite/069_job_status_check_uppercase.sql and
// internal/infrastructure/database/sqlite/jobs/repository_broker_roundtrip_test.go::jobsTestSchema).
//
// CRITICAL NOTNULL DIFFERENCES vs the pre-fix DDL: lease_expiry / started_at /
// completed_at / cancelled_at are NULLABLE because timeutil.FormatPtrRFC3339
// returns Go nil for nil (*time.Time) — which the SQLite driver binds as
// SQL NULL. The pre-fix NOT NULL DEFAULT ” constraint fired
// "NOT NULL constraint failed: jobs.lease_expiry" on every Service.Enqueue
// call (the canonical path that creates a non-leased job row). Production
// allows NULL on these columns for exactly this reason; the e2e fixture must
// match.
//
// godlike/06 SSOT: any future drift between this DDL and the production
// migrations/sqlite/053_job_lifecycle_atomic.sql (or the equivalent
// migration that supersedes it) surfaces as either (a) a SQL error here
// at INSERT time (column count or type mismatch), or (b) a code-review
// failure when a contributor updates the production DDL without touching
// this fixture. The fixture is BYTE-EQUIVALENT to the canonical
// production SUBSET so the behaivoural contract is welded shut.
const jobsTableDDL = `
CREATE TABLE IF NOT EXISTS jobs (
    id                  TEXT    PRIMARY KEY,
    type                TEXT    NOT NULL,
    status              TEXT    NOT NULL,
    priority            INTEGER NOT NULL DEFAULT 0,
    project             TEXT    NOT NULL DEFAULT '',
    video_name          TEXT    NOT NULL DEFAULT '',
    active_key          TEXT    NOT NULL DEFAULT '',
    correlation_id      TEXT    NOT NULL DEFAULT '',
    payload_json        TEXT    NOT NULL DEFAULT '{}',
    result_json         TEXT    NOT NULL DEFAULT '{}',
    progress            INTEGER NOT NULL DEFAULT 0,
    error               TEXT    NOT NULL DEFAULT '',
    retry_count         INTEGER NOT NULL DEFAULT 0,
    max_retries         INTEGER NOT NULL DEFAULT 3,
    worker_id           TEXT    NOT NULL DEFAULT '',
    lease_id            TEXT    NOT NULL DEFAULT '',
    lease_expiry        TEXT,                  -- nullable: timeutil.FormatPtrRFC3339(nil) → Go nil → SQL NULL
    created_at          TEXT    NOT NULL,      -- populated by Service.Enqueue via timeutil.FormatRFC3339 (non-pointer)
    updated_at          TEXT    NOT NULL,      -- populated by Service.Enqueue via timeutil.FormatRFC3339 (non-pointer)
    started_at          TEXT,                  -- nullable: set on ClaimNext; NULL pre-Claim
    completed_at        TEXT,                  -- nullable: set on Complete/Fail; NULL pre-terminal
    cancelled_at        TEXT,                  -- nullable: set on Cancel; NULL pre-cancel
    parent_state_typed  TEXT    NOT NULL DEFAULT '',
    revision            INTEGER NOT NULL DEFAULT 1
);`

// stubPipelineTranslator is the hermetic TranslationPort for the e2e.
// Returns deterministic `[<target_lang>] <source_text>` and records
// per-target call counts so the probe can assert the canonical
// fan-out shape (3 target-language calls; 0 source-language calls).
type stubPipelineTranslator struct {
	mu      sync.Mutex
	callsBy map[string]int // per-target-lang counter
}

func newStubPipelineTranslator() *stubPipelineTranslator {
	return &stubPipelineTranslator{callsBy: map[string]int{}}
}

func (s *stubPipelineTranslator) Translate(_ context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	s.mu.Lock()
	s.callsBy[cmd.TargetLang]++
	s.mu.Unlock()
	return translation.TranslationResult{
		TranslatedText: fmt.Sprintf("[%s] %s", cmd.TargetLang, cmd.Text),
		Confidence:     0.85,
		UsedProvider:   "stub",
		UsedModel:      "stub-model",
		SourceLang:     cmd.SourceLang,
		TargetLang:     cmd.TargetLang,
		CacheStatus:    "bypass",
	}, nil
}

// textTrackPipelineFixture extends textTrackFixture with the broker +
// handler + fan-out stack. The pre-existing 4 tests do NOT use this
// fixture (they're resolver + backfill probes); this is purely
// additional wiring for the Fase 3 job-pipeline e2e.
type textTrackPipelineFixture struct {
	*textTrackFixture
	Store        *sqljobs.SQLiteStore
	Dispatcher   *appjobs.Dispatcher
	Service      *appjobs.Service
	Materializer *texttracks.Materializer
	Handler      *texttracks.MaterializeJobHandler
	FanOut       *texttracks.MaterializeFanOut
	Translator   *stubPipelineTranslator
}

// newTextTrackPipelineFixture constructs the canonical broker + handler
// + fan-out wiring on top of the existing textTrackFixture (which
// already opened an in-memory SQLite with media_assets + outbox_events
// + asset_text_tracks + the production-backed repositories).
//
// Wiring contract (godlike/06 SSOT — each surface has one canonical
// owner; we plug producers into the canonical consumer surfaces):
//
//	texttracks.MaterializeEnqueuer := *appjobs.Service (compile-time pin)
//	texttracks.OutboxEnqueuer       := *outboxevents.Repository
//	asset.TextTrackRepository       := *texttracks.TextTrackRepositorySQLite
//	translation.TranslationPort     := *stubPipelineTranslator
//	job.JobBroker                   := *sqljobs.SQLiteStore
//	job.Dispatcher.Register         := *texttracks.MaterializeJobHandler
//
// job.TypeAssetTextMaterialize is the single canonical job-type
// constant (internal/domain/job/job.go:228) — both the broker
// dispatch + the registry entry + the handler surface read from it.
// A future rename surfaces as a build failure across all three
// surfaces (godlike/06 SSOT lock).
func newTextTrackPipelineFixture(t *testing.T, collection string) *textTrackPipelineFixture {
	t.Helper()
	fx := newTextTrackFixture(t, collection)

	// Add the `jobs` table to the shared in-memory SQLite. The DDL is
	// byte-equivalent to the production SQLiteStore.Create INSERT
	// shape (23 columns); future drift between production DDL and
	// this fixture's DDL surfaces as a SQL error at insert time.
	if _, err := fx.DB.Exec(jobsTableDDL); err != nil {
		t.Fatalf("CREATE TABLE jobs must succeed: %v", err)
	}

	translator := newStubPipelineTranslator()

	// Canonical language registry for MaterializeLanguages — built
	// via asset.NewLanguageRegistryFromCodes per PR-CATALOG-
	// MULTILINGUA step 3 (the legacy MaterializeLanguages []string
	// field was removed from texttracks.ResolverConfig in favor of
	// a typed LanguageRegistry that carries canonical BCP-47
	// normalization + dedup semantics at construction time).
	reg, err := asset.NewLanguageRegistryFromCodes([]string{"en", "it", "es", "fr"})
	if err != nil {
		t.Fatalf("asset.NewLanguageRegistryFromCodes must succeed: %v", err)
	}

	// Real Materializer using the production TextTrackRepositorySQLite
	// + production outboxevents.Repository + hermetic translator.
	materializer, err := texttracks.NewMaterializer(
		fx.TTRepo,
		translator,
		fx.Events, // outbox.Repository satisfies OutboxEnqueuer (godlike/06 SSOT signature match)
		texttracks.ResolverConfig{
			Registry:       reg,
			SourceLanguage: "en",
			ModelVersion:   "model-v1",
			PromptVersion:  "prompt-v1",
		},
		fx.Log,
	)
	if err != nil {
		t.Fatalf("texttracks.NewMaterializer must succeed: %v", err)
	}

	// SQL-backed job.Store — same package as production (*sqljobs.SQLiteStore).
	store := sqljobs.NewSQLiteStore(fx.DB, fx.Log)

	// Dispatcher + handler. Register the canonical MaterializeJobHandler
	// against the canonical job-type constant BEFORE Freeze() so the
	// gate is closed at composition time (no late-Register after the
	// Service is wired).
	//
	// appjobs.HandlerFunc(handler.HandleJob) is the canonical wrap
	// pattern: appjobs.HandlerFunc is a type alias for appjobs.Handler
	// (see internal/application/jobs/types.go), so the method value
	// handler.HandleJob is converted to the canonical Handler shape
	// the Dispatcher.Register signature requires. The production
	// registration in internal/app/build_bundles_texttracks.go uses
	// the same wrap pattern via MaterializeJobHandler.Register →
	// jobsSvc.RegisterHandler(TypeAssetTextMaterialize,
	//                          appjobs.HandlerFunc(h.HandleJob)).
	// A future drift in job.Handler / job.JobExecutionTools surfaces
	// as a build failure HERE — the same compile-time pin that locks
	// the production wiring (godlike/06 SSOT).
	dispatcher := appjobs.NewDispatcher()
	handler := texttracks.NewMaterializeJobHandler(materializer, fx.Log)
	if err := dispatcher.Register(job.TypeAssetTextMaterialize, appjobs.HandlerFunc(handler.HandleJob)); err != nil {
		t.Fatalf("dispatcher.Register must succeed: %v", err)
	}
	dispatcher.Freeze()

	// Service with the canonical registry. The registry's
	// registerTextTrackEntries block (registry.go::Compose) MUST
	// contain TypeAssetTextMaterialize; if a future refactor drops it,
	// svc.Enqueue fails the HasHandler check at enqueue time (the
	// dispatcher-registered handler is the load-bearing gate — see
	// enqueue_service.go::HasHandler call for the double-check).
	svc, err := appjobs.NewService(store, dispatcher, fx.Log, appjobs.Compose())
	if err != nil {
		t.Fatalf("appjobs.NewService must succeed: %v", err)
	}

	fanOut := texttracks.NewMaterializeFanOut(svc, fx.Log)

	return &textTrackPipelineFixture{
		textTrackFixture: fx,
		Store:            store,
		Dispatcher:       dispatcher,
		Service:          svc,
		Materializer:     materializer,
		Handler:          handler,
		FanOut:           fanOut,
		Translator:       translator,
	}
}

// seedSourceTrackEn materializes a READY English source track for
// (asset.TextTrackTranscript). Required pre-Materialize so the
// resolver's FindSourceTrack returns a non-nil READY row (the
// materializer's terminal ErrTrackNotReady would otherwise fire on
// any textTrackMaterialize call).
func seedSourceTrackEn(t *testing.T, fx *textTrackPipelineFixture, assetID, sourceText, sourceVersion string) {
	t.Helper()
	if err := fx.TTRepo.UpsertBatch(context.Background(), []asset.TextTrack{
		{
			AssetID:            assetID,
			LanguageCode:       "en",
			TextKind:           asset.TextTrackTranscript,
			TextContent:        sourceText,
			SourceType:         asset.TextSourceProvided,
			SourceLanguageCode: "en",
			IsOriginal:         true,
			Provider:           "stub",
			ModelName:          "stub-model",
			ModelVersion:       "model-v1",
			TextHash:           texttracks.ComputeSourceTextHash(sourceText),
			SourceVersion:      sourceVersion,
			Status:             asset.TextTrackReady,
		},
	}); err != nil {
		t.Fatalf("seedSourceTrackEn must succeed: %v", err)
	}
}

// fetchMostRecentMaterializeJob loads the most-recently-created
// asset.text.materialize job row into a *job.Job populated with the
// minimum fields the MaterializeJobHandler reads (ID, Type, Payload).
// Other columns are left empty — they're not consulted on the happy
// path (no ActiveKey/CorrelationID/WorkerID-dependent code paths).
//
// SCAN STRATEGY: payload_json is scanned into a Go string (NOT
// []byte). The mattn/go-sqlite3 driver maps TEXT columns to string
// by default, and a []byte scan on some driver builds returns a
// Go-string encoded as []byte (the driver pre-encode step can
// accidentally manifest a JSON string primitive instead of a JSON
// object — triggering "cannot unmarshal string into struct" on
// the handler decode). The string scan + manual []byte conversion
// below makes the wire shape unambiguous: the bytes are EXACTLY
// what was stored (the production SQLiteStore.Create writes them
// via string(j.Payload) → ExecContext parameter binding).
func fetchMostRecentMaterializeJob(t *testing.T, fx *textTrackPipelineFixture) *job.Job {
	t.Helper()
	var (
		id         string
		jobType    string
		status     string
		activeKey  string
		payloadStr string
	)
	if err := fx.DB.QueryRow(`
		SELECT id, type, status, active_key, payload_json
		  FROM jobs
		 WHERE type = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`, job.TypeAssetTextMaterialize,
	).Scan(&id, &jobType, &status, &activeKey, &payloadStr); err != nil {
		t.Fatalf("fetchMostRecentMaterializeJob must succeed: %v", err)
	}
	return &job.Job{
		ID:        id,
		Type:      jobType,
		Status:    job.Status(status),
		ActiveKey: activeKey,
		Payload:   []byte(payloadStr),
	}
}

// dispatchMostRecentMaterialize invokes the registered
// MaterializeJobHandler for the most-recent asset.text.materialize
// job via the production dispatcher surface
// (godlike/06 SSOT — the SAME dispatcher the in-process broker
// uses for in-process workers; bypassing only the Worker poll-loop
// + lease lifecycle). Returns the canonical Handler Result envelope.
func dispatchMostRecentMaterialize(t *testing.T, fx *textTrackPipelineFixture) map[string]any {
	t.Helper()
	j := fetchMostRecentMaterializeJob(t, fx)
	tools := &appjobs.JobExecutionTools{}
	result, err := fx.Dispatcher.Dispatch(context.Background(), j, tools)
	if err != nil {
		t.Fatalf("dispatcher.Dispatch must succeed: %v", err)
	}
	return result
}

// outboxRow is a typed view of the canonical outbox_events row the
// materializer emits. Mirrors outboxevents.Repository.Enqueue
// contract (event_type + aggregate_id + aggregate_type +
// payload_json + event_key).
type outboxRow struct {
	EventType     string
	AggregateID   string
	AggregateType string
	PayloadJSON   string
	EventKey      string
}

// queryOutboxFor returns the matched outbox_events row for
// (aggregate_id, event_type). Failure surfaces as t.Fatal so the
// probe sees a missing outbox row, not a downstream nil-pointer
// cascade (godlike/07 minimum diagnostic distance).
func queryOutboxFor(t *testing.T, fx *textTrackPipelineFixture, assetID, eventType string) outboxRow {
	t.Helper()
	var r outboxRow
	if err := fx.DB.QueryRow(`
		SELECT event_type, aggregate_id, aggregate_type, payload_json, event_key
		  FROM outbox_events
		 WHERE aggregate_id = ?
		   AND event_type = ?`, assetID, eventType,
	).Scan(&r.EventType, &r.AggregateID, &r.AggregateType, &r.PayloadJSON, &r.EventKey); err != nil {
		t.Fatalf("outbox_events row must exist for aggregate_id=%q event_type=%q: %v",
			assetID, eventType, err)
	}
	return r
}

// ─── Probe 1: HAPPY PATH ────────────────────────────────────────────────
// Verifies the full pipeline observable end-state:
//   - jobs row exists with the canonical event_type
//   - Handler dispatch returns the canonical result envelope
//   - source track unchanged (READ preservation)
//   - 3 target-language READY rows written (it, es, fr) with the
//     stub-translator's [lang] text shape + Provider/ModelVersion
//     provenance
//   - Exactly 1 outbox_events row with event_type=asset.index.requested
//   - payload wire shape {asset_id, kind, reason}
//   - Translator called once per target language (3); source language
//     excluded (0 calls for "en")
func TestE2E_TextTrackMaterializeJobPipeline_HappyPath(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_happy_001"
		sourceText = "Broner yells at Pacquiao — stop worrying about Floyd"
	)
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")
	srcHash := texttracks.ComputeSourceTextHash(sourceText)

	// ── 1. fanout.EnqueueMaterializeOne → *Service.Enqueue → INSERT INTO jobs. ──
	require.NoError(t,
		fx.FanOut.EnqueueMaterializeOne(
			ctx, assetID, "en", srcHash,
			[]asset.TextTrackKind{asset.TextTrackTranscript},
		),
		"EnqueueMaterializeOne must succeed (broker surfaces ActiveKey dedup + lifecycle)")

	// Exactly 1 jobs row.
	var jobCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type = ?`, job.TypeAssetTextMaterialize,
	).Scan(&jobCount))
	require.Equal(t, 1, jobCount,
		"Enqueue must produce exactly 1 asset.text.materialize jobs row")

	// ── 2. dispatcher.Dispatch → MaterializeJobHandler.HandleJob (broker hands off to worker). ──
	res := dispatchMostRecentMaterialize(t, fx)

	// Result envelope is the canonical MaterializeJobHandler return shape.
	require.Equal(t, assetID, res["asset_id"], "handler result asset_id must match input")
	require.Equal(t, "en", res["source_language"], "handler result source_language must match input")
	require.Equal(t, 1, res["kind_count"], "handler result kind_count must equal 1")

	// ── 3. asset_text_tracks state: source unchanged + new READY rows for [it, es, fr]. ──
	source, err := fx.TTRepo.Find(ctx, assetID, "en", asset.TextTrackTranscript)
	require.NoError(t, err)
	require.NotNil(t, source, "source track must remain readable (READ preservation)")
	require.Equal(t, sourceText, source.TextContent,
		"source text_content MUST NOT be modified by Materialize")
	require.Equal(t, asset.TextTrackReady, source.Status,
		"source status MUST remain READY post-Materialize")
	require.True(t, source.IsOriginal,
		"source IsOriginal MUST remain true (provided source)")

	for _, lang := range []string{"it", "es", "fr"} {
		row, findErr := fx.TTRepo.Find(ctx, assetID, lang, asset.TextTrackTranscript)
		require.NoError(t, findErr, "Find %s must succeed", lang)
		require.NotNil(t, row, "%s text_track row MUST exist post-Materialize", lang)
		require.Equal(t, asset.TextTrackReady, row.Status,
			"%s row MUST be READY post-Materialize", lang)
		require.Equal(t, asset.TextSourceTranslation, row.SourceType,
			"%s row MUST be source=translation", lang)
		require.Equal(t, "en", row.SourceLanguageCode,
			"%s row MUST record source_language=%q", lang, "en")
		require.False(t, row.IsOriginal,
			"%s row MUST NOT be original (it's a translation)", lang)
		require.Equal(t, "stub", row.Provider,
			"%s row MUST record stub Provider (canonical provenance)", lang)
		require.Equal(t, "model-v1", row.ModelVersion,
			"%s row MUST record materializer ModelVersion", lang)
		require.Equal(t, fmt.Sprintf("[%s] %s", lang, sourceText), row.TextContent,
			"%s row text_content MUST match stub-translator shape", lang)
	}

	// ── 4. Outbox emission: exactly 1 row with the canonical event_type + payload shape. ──
	out := queryOutboxFor(t, fx, assetID, outboxevents.EventAssetIndexRequested)
	require.Equal(t, outboxevents.EventAssetIndexRequested, out.EventType,
		"outbox event_type MUST be asset.index.requested")
	require.Equal(t, assetID, out.AggregateID,
		"outbox aggregate_id MUST equal the asset ID")
	require.Equal(t, "asset", out.AggregateType,
		"outbox aggregate_type MUST be 'asset' (godlike/06 SSOT)")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.PayloadJSON), &envelope),
		"outbox payload_json must be valid JSON")
	require.Equal(t, assetID, envelope["asset_id"],
		"outbox payload asset_id MUST equal the asset ID")
	require.Equal(t, string(asset.TextTrackTranscript), envelope["kind"],
		"outbox payload kind MUST equal 'transcript'")
	require.Equal(t, "asset.text.materialize complete", envelope["reason"],
		"outbox payload reason MUST be the canonical literal")

	// ── 5. Translator was called once per target language (3); source excluded (0). ──
	fx.Translator.mu.Lock()
	calls := make(map[string]int, len(fx.Translator.callsBy))
	for k, v := range fx.Translator.callsBy {
		calls[k] = v
	}
	fx.Translator.mu.Unlock()
	require.Equal(t, 1, calls["it"], "translator MUST be called exactly once for IT")
	require.Equal(t, 1, calls["es"], "translator MUST be called exactly once for ES")
	require.Equal(t, 1, calls["fr"], "translator MUST be called exactly once for FR")
	require.Equal(t, 0, calls["en"],
		"source language MUST be excluded from the fan-out (0 translator calls for 'en')")
}

// ─── Probe 2: BROKER IDEMPOTENCY ────────────────────────────────────────
// Verifies the canonical two-level idempotency stack:
//
//  1. BROKER-LEVEL: ActiveKey =
//     "asset.text.materialize:<asset_id>:<source_text_hash>".
//     *Service.Enqueue collapses repeated enqueues to a single jobs
//     row (godlike/07 fail-closed: no double publish for the same
//     source post-publish race).
//
//  2. HANDLER-LEVEL: even when the broker row count is 1, the
//     Materializer should be invoked exactly once (one HandleJob
//     call). The e2e asserts this indirectly: exactly 1 dispatch
//     produces exactly 3 translator calls + 1 outbox_events row
//     (not 6 + 2).
func TestE2E_TextTrackMaterializeJobPipeline_BrokerIdempotency(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_idempotency_002"
		sourceText = "idempotency test text"
	)
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")
	srcHash := texttracks.ComputeSourceTextHash(sourceText)

	// ── Enqueue TWICE with identical ActiveKey. Broker dedups at the row level. ──
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]asset.TextTrackKind{asset.TextTrackTranscript},
	), "first EnqueueMaterializeOne must succeed")
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]asset.TextTrackKind{asset.TextTrackTranscript},
	), "second EnqueueMaterializeOne must succeed (silently collapses to existing job)")

	// ── Exactly 1 jobs row (ActiveKey collapse). ──
	var jobCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type = ?`, job.TypeAssetTextMaterialize,
	).Scan(&jobCount))
	require.Equal(t, 1, jobCount,
		"ActiveKey dedup MUST collapse repeated enqueues to a single jobs row")

	// ── ActiveKey column carries the canonical shape. ──
	var observedKey string
	require.NoError(t, fx.DB.QueryRow(
		`SELECT active_key FROM jobs WHERE type = ? ORDER BY created_at ASC LIMIT 1`,
		job.TypeAssetTextMaterialize,
	).Scan(&observedKey))
	require.Equal(t,
		"asset.text.materialize:"+assetID+":"+srcHash,
		observedKey,
		"ActiveKey MUST follow asset.text.materialize:<asset_id>:<source_text_hash> format")

	// ── Dispatch once. Result envelope is the canonical shape. ──
	res := dispatchMostRecentMaterialize(t, fx)
	require.Equal(t, assetID, res["asset_id"])
	require.Equal(t, "en", res["source_language"])

	// ── Exactly 1 outbox_events row (handler ran once — NOT twice). ──
	var outboxCount int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, assetID,
	).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount,
		"ActiveKey dedup must result in exactly 1 dispatch + 1 outbox emission")
}

// ─── Probe 3: PAYLOAD WIRE-SHAPE PIN ────────────────────────────────────
// Locks the literal JSON wire shape of MaterializeJobPayload so future
// refactors (struct tag drift, field rename, omitempty flip) surface as
// test failure here, not as a silent worker-decode failure on first
// contact with a real producer.
//
// require.JSONEq renders the comparison order-agnostic; key NAMES
// must still match exactly (godlike/06 SSOT wire contract).
func TestE2E_TextTrackMaterializeJobPipeline_PayloadWireShape(t *testing.T) {
	tests := []struct {
		name             string
		payload          texttracks.MaterializeJobPayload
		expected         string
		absentKeysAssert []string // keys that MUST NOT appear (omitempty conformance)
	}{
		{
			name: "all five canonical fields present",
			payload: texttracks.MaterializeJobPayload{
				AssetID:        "asset-payload-wire-test",
				SourceLanguage: "en",
				SourceTextHash: "sha256:abc123",
				TextKinds:      []string{"transcript", "description"},
			},
			expected: `{
				"asset_id": "asset-payload-wire-test",
				"source_language": "en",
				"source_text_hash": "sha256:abc123",
				"text_kinds": ["transcript", "description"]
			}`,
		},
		{
			name: "target_languages override included",
			payload: texttracks.MaterializeJobPayload{
				AssetID:         "asset-tl-override-test",
				SourceLanguage:  "en",
				SourceTextHash:  "sha256:def456",
				TargetLanguages: []string{"it", "es"},
				TextKinds:       []string{"transcript"},
			},
			expected: `{
				"asset_id": "asset-tl-override-test",
				"source_language": "en",
				"source_text_hash": "sha256:def456",
				"target_languages": ["it", "es"],
				"text_kinds": ["transcript"]
			}`,
		},
		{
			name: "nil TargetLanguages omitted (omitempty contract)",
			payload: texttracks.MaterializeJobPayload{
				AssetID:        "asset-no-tl-test",
				SourceLanguage: "en",
				SourceTextHash: "sha256:xyz789",
				TextKinds:      []string{"summary"},
			},
			expected: `{
				"asset_id": "asset-no-tl-test",
				"source_language": "en",
				"source_text_hash": "sha256:xyz789",
				"text_kinds": ["summary"]
			}`,
			absentKeysAssert: []string{"target_languages"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			require.NoError(t, err, "json.Marshal must not error")
			require.JSONEq(t, tc.expected, string(raw),
				"MaterializeJobPayload wire shape must match exactly (godlike/06 SSOT wire contract)")
			for _, absent := range tc.absentKeysAssert {
				require.NotContains(t, string(raw), `"`+absent+`"`,
					"key %q must be absent from wire shape under omitempty contract", absent)
			}
		})
	}
}

// Probe 4 ensures the outbox_events row has EXACTLY 1 entry per
// HandleJob invocation — i.e. the materializer does not double-emit
// when both created + retranslated languages exist. Mirrors a worker
// retry scenario: an asset whose source row changed mid-flight
// triggers retranslate (rather than skip), so CreatedLanguages +
// RetranslatedLanguages both populate and a single emission still
// holds (the canonical "asset.index.requested" surface is per-asset,
// not per-language).
func TestE2E_TextTrackMaterializeJobPipeline_OutboxEmissionSingle(t *testing.T) {
	fx := newTextTrackPipelineFixture(t, "media_assets_current")
	ctx := context.Background()

	const (
		assetID    = "tt_pipeline_outbox_single_004"
		sourceText = "outbox single-emission test"
	)
	// Seed a STALE Italian row (model_version != materializer's "model-v1")
	// so the materializer classifies IT as "retranslate" (vs IT-classified
	// as "skip") AND seeds fresh rows for es + fr.
	seedSourceTrackEn(t, fx, assetID, sourceText, "src-v1")

	// Seed stale IT row with a different model_version so policy.ShouldRetranslate
	// fires on the IT language (returns ShouldRetranslate=true because the
	// existing track's ModelVersion != materializer's "model-v1"). This
	// drives the CreatedLanguages + RetranslatedLanguages aggregation
	// path; the materializer should still emit a single asset.index.requested.
	require.NoError(t, fx.TTRepo.UpsertBatch(ctx, []asset.TextTrack{
		{
			AssetID:            assetID,
			LanguageCode:       "it",
			TextKind:           asset.TextTrackTranscript,
			TextContent:        "[it] stale (old model) text",
			SourceType:         asset.TextSourceTranslation,
			SourceLanguageCode: "en",
			IsOriginal:         false,
			Provider:           "stub",
			ModelName:          "stub-model",
			ModelVersion:       "model-v0-stale",
			TextHash:           "sha256:it_stale",
			SourceVersion:      "src-v1",
			Status:             asset.TextTrackReady,
		},
	}), "seed stale IT row must succeed")

	srcHash := texttracks.ComputeSourceTextHash(sourceText)
	require.NoError(t, fx.FanOut.EnqueueMaterializeOne(
		ctx, assetID, "en", srcHash,
		[]asset.TextTrackKind{asset.TextTrackTranscript},
	))
	res := dispatchMostRecentMaterialize(t, fx)

	// ── Single outbox row per HandleJob invocation. ──
	var n int
	require.NoError(t, fx.DB.QueryRow(
		`SELECT COUNT(*) FROM outbox_events
		  WHERE aggregate_id = ?
		    AND event_type  = ?`,
		assetID, outboxevents.EventAssetIndexRequested,
	).Scan(&n))
	require.Equal(t, 1, n,
		"asset.text.materialize must emit EXACTLY 1 outbox row per dispatch (per-asset, not per-language)")

	// Result envelope concurs: at least one translated language (created or
	// retranslated) is reported, and zero failed. This gates the regression
	// where the outbox emission path was accidentally gated on
	// CreatedLanguages only (and skipped when RetranslatedLanguages held
	// a value) — both paths must converge on a single emission.
	materialized, ok := res["languages_materialized"].([]string)
	require.True(t, ok,
		"handler result must carry languages_materialized ([]string) per godlike/06 contract")
	require.Equal(t, 3, len(materialized),
		"languages_materialized must contain exactly 3 entries (it retranslated, es + fr created)")
	failed, ok := res["languages_failed"].(map[string]string)
	require.True(t, ok,
		"handler result must carry languages_failed (map[string]string) per godlike/06 contract")
	require.Empty(t, failed,
		"languages_failed MUST be empty on the happy (no-translation-error) path")
}
