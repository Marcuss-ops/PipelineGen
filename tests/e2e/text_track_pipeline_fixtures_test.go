package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Job-pipeline DDL, translator stub, broker/handler fixture, and
// database/outbox helper functions.

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
    parent_job_id       TEXT    NOT NULL DEFAULT '',
    root_job_id         TEXT    NOT NULL DEFAULT '',
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
