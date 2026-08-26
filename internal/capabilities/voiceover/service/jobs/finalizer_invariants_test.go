// Package jobs — finalizer_invariants_test.go (PR-VO-COMPLETEPATH-FIX
// closure mirror tests, July 2026).
//
// 3 TDD regression tests that pin the canonical contracts of the
// voiceover pipeline after PR-VO-COMPLETEPATH-FIX (commit db2f3b1e,
// July 4 2026). Each test is a regression guard for the post-fix
// invariants; if a future change re-introduces ProducesArtifacts=true
// on the voiceover job types OR breaks the finalizer's atomic-write
// contract, the matching test will fail loudly.
//
// Note: the 4th historical test (TestParentAggregator_TriggeredOnlyAfterWaitingChildren)
// was moved to parent_aggregator_eligibility_test.go by
// PR-SPLIT-VO-PARENT-AGG-TESTS (July 2026) to mirror the production
// split of parent_aggregator.go → parent_eligibility.go. The
// eligibility-gate test is the canonical owner of
// IsParentAwaitingAggregation; isolating it in a dedicated sibling
// honours godlike/06 SSOT (one canonical owner per fact) and avoids
// fragmenting the finalizer-invariants scope (which is VO-finalizer
// specific, not aggregator-eligibility specific).
//
// Test map (per the original Italian audit action plan):
//
//  1. TestVoiceoverGenerate_RoutesToLegacyComplete
//     Verifies that the canonical registry (appjobs.Compose) declares
//     job.TypeVoiceoverGenerate with ProducesArtifacts=false post-fix.
//     The runner's runLease uses registry.ProducesArtifacts(jobType)
//     to decide between tools.Complete and tools.CompleteWithArtifacts;
//     the contract value drives the routing.
//
//  2. TestVoiceoverGenerateItem_RoutesToLegacyComplete
//     Same as #1 for the per-language child job type. Mirrors the
//     production canonical registration at internal/capabilities/jobs/queue/
//     registry.go:548.
//
//  3. TestVoiceoverFinalizer_PersistsMediaAssetsInSameTxn
//     Verifies that voiceover.finalizer.Finalize writes voiceovers +
//     media_assets + outbox_events in the SAME caller-owned *sql.Tx.
//     Uses the canonical rollback trick: call Finalize with a tx,
//     then rollback the tx — if the writes were in the caller's tx,
//     the rollback undoes them; if the finalizer had opened its own
//     tx, the writes would survive. The 3 adapters (voiceoverRepo,
//     lifecycleService, outbox) record the *sql.Tx they receive and
//     assert.Same verifies tx-pointer identity (= SAME tx as caller).
//
//     Honest scope-lock (audit discovery, NOT a regression of this
//     fix): voiceover.finalizer does NOT write to asset_locations.
//     The asset_locations table is written by AssetFinalizerTx
//     (internal/application/assets/finalizer/asset_finalizer_tx.go)
//     for artlist + stock pipelines; voiceover's canonical location
//     surface is media_assets (Source='voiceover', MediaType='audio',
//     DriveFileID, DriveLink, DownloadLink). This test is calibrated
//     on path B (the chosen fix): the 3 tables the finalizer DOES
//     write. The asset_locations gap is a forward-pointer
//     (PR-VO-ASSET-LOCATIONS-CONSUMER-AUDIT) tracked separately.
//
//  4. TestParentAggregator_TriggeredOnlyAfterWaitingChildren [MOVED]
//     Moved to parent_aggregator_eligibility_test.go by
//     PR-SPLIT-VO-PARENT-AGG-TESTS (Step 5). The eligibility-gate
//     test is owned by a dedicated sibling file to mirror the
//     production split (parent_aggregator.go → parent_eligibility.go).
package jobs

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─────────────────────────────────────────────────────────────────────
// Test 1: voiceover.generate → legacy Complete, NOT CompleteWithArtifacts
// ─────────────────────────────────────────────────────────────────────

// TestVoiceoverGenerate_RoutesToLegacyComplete pins the AZIONE 7
// routing contract for the parent voiceover job type. The runner's
// runLease consults registry.ProducesArtifacts(jobType) to decide
// between tools.Complete (legacy, plain) and tools.CompleteWithArtifacts
// (artifact-emitter spine). Post-PR-VO-COMPLETEPATH-FIX the canonical
// registry MUST declare job.TypeVoiceoverGenerate with ProducesArtifacts=
// false so the runner routes to legacy Complete — voiceover.finalizer
// is the canonical owner of the per-item artifact writes
// (voiceovers + media_assets + outbox events) inside the caller-owned
// tx, NOT the broker's CompleteWithArtifacts path.
//
// Regression guard: if a future commit re-introduces ProducesArtifacts=
// true on job.TypeVoiceoverGenerate, the SQL-layer guard at
// internal/platform/sqlite/jobs/repository_lifecycle.go:115
// will reject the legacy Complete with domainremote.ErrCompleteJobPathViolation
// (the typed sentinel declared at internal/domain/remote/complete_job.go:148),
// causing the voiceover.generate parent to be marked FAILED instead of
// SUCCEEDED — the bug PR-VO-COMPLETEPATH-FIX closed.
//
// Forward-pointer (godlike/07 no-revert rationale): the rejection of
// the Path A design (parent false + child true + CompleteWithArtifacts
// fork when child terminates) is EXPLICITLY LOCKED in
// architecture/current.yaml#PR-VO-COMPLETEPATH-FIX
// (status: keep, migration_phase: not applicable, removal_date: never).
// The record documents 3 reasons: (1) godlike/06 SSOT double-write
// race between broker's media_assets UPSERT and finalizer's
// media_assets UPSERT; (2) godlike/07 no-fake-availability + the
// SQL-layer guard at repository_lifecycle.go:115 doesn't model the
// broker's "fork" (binary legacy-Complete-vs-reject surface, no 3rd
// code path); (3) godlike/07 fail-fast-at-boot vs fail-slow-at-first-
// /run (Path A's bug surfaces at runtime, Path B's invariant is
// enforced at registration time). Any revert to Path A MUST open
// architecture/current.yaml#PR-VOICEOVER-PATH-A-REVISITED (deadline
// TBD) with a 3-reason rebuttal before the registry change is
// permitted. The 4 TDD tests in this file are the load-bearing seam
// — any revert breaks tests (a) Test 1 + (b) Test 2 at the
// registry-contract level.
func TestVoiceoverGenerate_RoutesToLegacyComplete(t *testing.T) {
	reg := appjobs.Compose()
	require.NotNil(t, reg)
	require.True(t, reg.IsRegistered(job.TypeVoiceoverGenerate),
		"job.TypeVoiceoverGenerate MUST be registered in the canonical registry (Compose())")

	assert.False(t, reg.ProducesArtifacts(job.TypeVoiceoverGenerate),
		"job.TypeVoiceoverGenerate MUST have ProducesArtifacts=false post-PR-VO-COMPLETEPATH-FIX (commit db2f3b1e, 2026-07-04). "+
			"voiceover.finalizer.Finalize is the canonical owner of media_assets + outbox writes inside the per-item tx; "+
			"the broker's legacy Complete is the canonical mark-SUCCEEDED seam. "+
			"If ProducesArtifacts=true is reintroduced, the SQL-layer guard at "+
			"internal/platform/sqlite/jobs/repository_lifecycle.go:115 will reject the legacy Complete "+
			"with domainremote.ErrCompleteJobPathViolation, causing the voiceover.generate parent to be marked FAILED "+
			"instead of SUCCEEDED — the exact bug PR-VO-COMPLETEPATH-FIX closed.")

	// Also verify the canonical registration surface (registry.go:541)
	// uses the JobPolicy literal and not the ProducesArtifacts flag.
	entry, ok := reg.Get(job.TypeVoiceoverGenerate)
	require.True(t, ok, "job.TypeVoiceoverGenerate must be a registered entry")
	assert.Equal(t, "voiceover.generate", entry.Completion.JobType)
	assert.Equal(t, appjobs.ArtifactOwnershipApplication, entry.Completion.ArtifactOwnership,
		"registry entry must declare application-owned artifact persistence")
	assert.Equal(t, appjobs.FinalizationStrategyLegacyComplete, entry.Completion.FinalizationStrategy,
		"application-owned voiceover artifacts must use legacy broker completion")
	assert.Contains(t, entry.Description, "voiceover.Finalizer",
		"registry entry's Description MUST mention voiceover.Finalizer as the canonical artifact owner (godlike/06 SSOT documentation discipline)")
}

// ─────────────────────────────────────────────────────────────────────
// Test 2: voiceover.generate_item → legacy Complete, NOT CompleteWithArtifacts
// ─────────────────────────────────────────────────────────────────────

// TestVoiceoverGenerateItem_RoutesToLegacyComplete pins the AZIONE 7
// routing contract for the per-language child job type. Same shape
// as Test 1 but for the child fan-out. Each per-language child
// persists its own voiceovers row + media_assets projection + outbox
// events inside its own per-item tx via the unified finalizer; the
// broker's legacy Complete is the canonical mark-SUCCEEDED seam.
//
// Regression guard: same SQL-layer ErrCompleteJobPathViolation
// trigger if ProducesArtifacts=true is reintroduced on the child.
//
// Forward-pointer: architecture/current.yaml#PR-VO-COMPLETEPATH-FIX
// (canonical "Path A rejected" record; 3 documented reasons; see
// Test 1's forward-pointer block for the full rationale).
func TestVoiceoverGenerateItem_RoutesToLegacyComplete(t *testing.T) {
	reg := appjobs.Compose()
	require.NotNil(t, reg)
	require.True(t, reg.IsRegistered(appjobs.TypeVoiceoverGenerateItem),
		"job.TypeVoiceoverGenerateItem MUST be registered in the canonical registry (Compose())")

	assert.False(t, reg.ProducesArtifacts(appjobs.TypeVoiceoverGenerateItem),
		"job.TypeVoiceoverGenerateItem MUST have ProducesArtifacts=false post-PR-VO-COMPLETEPATH-FIX (commit db2f3b1e, 2026-07-04). "+
			"Each per-language child persists its own voiceovers row + media_assets projection + outbox events inside "+
			"its own per-item tx via the unified finalizer. The broker's legacy Complete is the canonical mark-SUCCEEDED "+
			"seam for this child type as well. "+
			"If ProducesArtifacts=true is reintroduced, the SQL-layer guard at "+
			"internal/platform/sqlite/jobs/repository_lifecycle.go:115 will reject the legacy Complete "+
			"with domainremote.ErrCompleteJobPathViolation, mirroring the parent-type bug.")

	entry, ok := reg.Get(appjobs.TypeVoiceoverGenerateItem)
	require.True(t, ok)
	assert.Equal(t, "voiceover.generate_item", entry.Completion.JobType)
	assert.Equal(t, appjobs.ArtifactOwnershipApplication, entry.Completion.ArtifactOwnership,
		"registry entry must declare application-owned artifact persistence")
	assert.Equal(t, appjobs.FinalizationStrategyLegacyComplete, entry.Completion.FinalizationStrategy,
		"application-owned voiceover artifacts must use legacy broker completion")
	assert.Equal(t, 4, entry.Concurrency,
		"job.TypeVoiceoverGenerateItem Concurrency MUST remain 4 (per-language sibling throttle — production canonical)")
}

// ─────────────────────────────────────────────────────────────────────
// Test 3: voiceover.finalizer writes in same caller-owned *sql.Tx
// ─────────────────────────────────────────────────────────────────────

// txRecordingOutbox records the *sql.Tx passed to each Enqueue*Event call.
// The proof of "same-tx atomicity" is tx-pointer identity: if the production
// TxOutboxEnqueuer implementation receives the SAME *sql.Tx that the
// caller opened via VoiceoverRepository.BeginTx, the writes it issues
// via tx.ExecContext are part of the caller's tx. Rolling back the
// caller's tx would undo them.
type txRecordingOutbox struct {
	// lastTx is the most-recent *sql.Tx pointer passed to any
	// Enqueue* call. Used to assert tx-pointer identity (the
	// canonical atomicity proof: every Enqueue call MUST receive
	// the SAME *sql.Tx the caller opened).
	lastTx *sql.Tx
	// indexEventCalls + cleanupEventCalls record the specific call
	// sequences for the per-step execution-marker assertions.
	indexEventCalls   int
	cleanupEventCalls int
}

func (s *txRecordingOutbox) EnqueueIndexEvent(_ context.Context, tx *sql.Tx, _ string, _, _ string) error {
	s.lastTx = tx
	s.indexEventCalls++
	return nil
}
func (s *txRecordingOutbox) EnqueueCleanupEvent(_ context.Context, tx *sql.Tx, _ string, _ string, _ string, _ []string) error {
	s.lastTx = tx
	s.cleanupEventCalls++
	return nil
}

var _ voiceover.TxOutboxEnqueuer = (*txRecordingOutbox)(nil)

// txRecordingLifecycle records the *sql.Tx passed to UpsertVoiceoverProjectionTx.
// The production concrete *lifecycle.Service.UpsertVoiceoverProjectionTx
// (internal/application/assets/lifecycle/service.go:405) takes a
// *sql.Tx and uses it for the media_assets UPSERT — if the test stub
// receives the SAME tx as the caller, the production impl would also
// write to that tx.
type txRecordingLifecycle struct {
	// lastTx is the *sql.Tx passed to UpsertVoiceoverProjectionTx.
	// Used to assert the production concrete would have written the
	// media_assets row in the caller's tx.
	lastTx *sql.Tx
	calls  int
}

func (s *txRecordingLifecycle) UpsertVoiceoverProjectionTx(_ context.Context, tx *sql.Tx, _ *voiceover.VoiceoverProjectionInput) error {
	s.lastTx = tx
	s.calls++
	return nil
}

var _ voiceover.LifecycleProjectionUpserter = (*txRecordingLifecycle)(nil)

// txRecordingVoiceoverRepo records the *sql.Tx passed to each
// VoiceoverRepository call. The InsertTx is exercised in the test
// (the finalizer's Step 3 calls it for the voiceovers row). The
// actual SQL write to the in-memory voiceovers table is verified
// by the rollback trick below.
type txRecordingVoiceoverRepo struct {
	db      *sql.DB
	lastTx  *sql.Tx
	insertN int
	deleteN int
	dedupeN int
}

func (s *txRecordingVoiceoverRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, nil)
}

func (s *txRecordingVoiceoverRepo) InsertTx(_ context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	s.lastTx = tx
	s.insertN++
	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO voiceovers (
			id, request_id, text_hash, text_preview, language, voice, filename,
			local_path, cleaned_path, folder_id, folder_path, drive_file_id,
			drive_link, download_link, file_hash, status, terror, strategy,
			metadata, idempotency_key, job_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.ID, rec.RequestID, rec.TextHash, rec.TextPreview, rec.Language, rec.Voice, rec.Filename,
		rec.LocalPath, rec.CleanedPath, rec.FolderID, rec.FolderPath, rec.DriveFileID,
		rec.DriveLink, rec.DownloadLink, rec.LegacyFileMD5, rec.Status, rec.Error, rec.Strategy,
		rec.Metadata, rec.IdempotencyKey, rec.JobID, rec.CreatedAt, rec.UpdatedAt,
	)
	return err
}

func (s *txRecordingVoiceoverRepo) DeleteByIDTx(_ context.Context, tx *sql.Tx, _ string) error {
	s.lastTx = tx
	s.deleteN++
	return nil
}

func (s *txRecordingVoiceoverRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	// The production finalizer does NOT call PreReadByID (the dedupe
	// gate uses CountByDriveFileIDTx). This stub is present only to
	// satisfy the persistence.Repository interface contract; callers
	// MUST NOT observe it during a canonical Finalize() call.
	return nil, nil
}

func (s *txRecordingVoiceoverRepo) CountByDriveFileIDTx(_ context.Context, tx *sql.Tx, _ string, _ string) (string, int, error) {
	s.lastTx = tx
	s.dedupeN++
	// count=0 → DecideDedupe returns DedupeContinue → finalizer proceeds
	// with Step 2 (DELETE) + Step 3 (INSERT) + Step 4 (projection) +
	// Step 5 (outbox index) + Step 6 (outbox cleanup, guard-skipped if
	// ShouldSwap=false). Step 1 dedupe-lookup is recorded (s.dedupeN++)
	// but the dedupe-lookup is the FIRST tx-bound call.
	return "", 0, nil
}

func (s *txRecordingVoiceoverRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	return "", sql.ErrNoRows
}

var _ persistence.Repository = (*txRecordingVoiceoverRepo)(nil)

// openInvariantsTestDB opens an in-memory SQLite with the canonical
// voiceovers table schema. The finalizer's Step 3 InsertTx writes to
// this table; the rollback trick at the end of the test verifies the
// write was in the caller's tx.
func openInvariantsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE voiceovers (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL DEFAULT '',
			text_hash TEXT NOT NULL DEFAULT '',
			text_preview TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT '',
			voice TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			cleaned_path TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			file_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			terror TEXT NOT NULL DEFAULT '',
			strategy TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)
	`)
	require.NoError(t, err)
	return db
}

// TestVoiceoverFinalizer_PersistsMediaAssetsInSameTxn pins the
// canonical atomicity contract of voiceover.finalizer.Finalize (P0.4
// Fase 3a, July 2026): the 6-step commit sequence runs inside the
// caller-owned *sql.Tx, so all writes (voiceovers + media_assets
// projection + outbox asset.index + outbox voiceover.cleanup) commit
// or rollback together.
//
// The proof is a 2-arm assertion:
//
//	(a) tx-pointer identity: each of the 3 adapters (voiceoverRepo,
//	    lifecycleService, outbox) MUST receive the SAME *sql.Tx that
//	    the caller opened via BeginTx. If the finalizer had opened
//	    a separate tx for any of the 3 writes, the *sql.Tx pointer
//	    would differ.
//
//	(b) rollback trick: after Finalize returns, the caller rolls
//	    back the tx. If the writes were in the caller's tx, the
//	    voiceovers table will be EMPTY afterwards. If the finalizer
//	    had committed to a separate tx, the voiceovers row would
//	    survive the rollback.
//
// Honest scope-lock (audit discovery, NOT a regression of this fix):
// voiceover.finalizer does NOT write to asset_locations. The
// asset_locations table is written by AssetFinalizerTx
// (internal/application/assets/finalizer/asset_finalizer_tx.go) for
// artlist + stock pipelines; voiceover's canonical location surface
// is media_assets (Source='voiceover', MediaType='audio', DriveFileID,
// DriveLink, DownloadLink). Forward-pointer: PR-VO-ASSET-LOCATIONS-
// CONSUMER-AUDIT (deadline TBD) decides between Strada A (adapt
// SceneRenderer to media_assets.DriveLink) or Strada B (extend the
// finalizer with a 7th step writing asset_locations in the same tx).
func TestVoiceoverFinalizer_PersistsMediaAssetsInSameTxn(t *testing.T) {
	db := openInvariantsTestDB(t)

	outbox := &txRecordingOutbox{}
	lifecycle := &txRecordingLifecycle{}
	repo := &txRecordingVoiceoverRepo{db: db}

	// Construct the production concrete finalizer via the exported
	// canonical constructor (godlike/06 Pattern 0 — voiceover.finalizer
	// is the SINGLE canonical implementation of the VoiceoverFinalizer
	// port per P0.4 Fase 3a, July 2026).
	f := voiceover.NewVoiceoverFinalizer(repo, outbox, lifecycle, nil, zap.NewNop()) // PR-ASSET-COMMITTER-COMMITASSET: committer=nil (legacy path)

	// Open the caller's tx — the canonical "atomicity envelope" the
	// finalizer is expected to honour.
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err, "BeginTx must succeed (in-memory SQLite)")

	// Call Finalize with the caller's tx. All 6 sub-steps (Step 1
	// dedupe-lookup, Step 2 DeleteByIDTx, Step 3 InsertTx, Step 4
	// UpsertVoiceoverProjectionTx, Step 5 EnqueueIndexEvent, Step 6
	// executeCleanupOutboxStep → EnqueueCleanupEvent) run inside this tx.
	// FinalizeCommand.Language is the typed voiceover.Language named
	// string type (PR-VO-TYPED-PRIMITIVES, July 2026); the test
	// converts the literal "en" via voiceover.Language("en") to match
	// the canonical typed surface.
	res, err := f.Finalize(context.Background(), tx, &voiceover.FinalizeCommand{
		ID:            "vo-atomicity",
		RequestID:     "req-atomicity",
		TextHash:      "hash-atomicity",
		Text:          "atomicity-test text",
		Language:      voiceover.Language("en"),
		Voice:         "en_female",
		Filename:      "atomicity.mp3",
		LocalPath:     "/tmp/atomicity.mp3",
		DriveFileID:   "drive-atomicity",
		DriveLink:     "https://drive.google.com/file/d/drive-atomicity/view",
		DownloadLink:  "https://drive.google.com/uc?id=drive-atomicity",
		LegacyFileMD5: "abc123",
		FolderID:      "folder-atomicity",
		FolderPath:    "/tmp/vo-atomicity",
		// ShouldSwap=false → Step 6 (cleanup outbox) is guard-skipped.
		// Step 5 (index outbox) executes because LegacyFileMD5="abc123".
		ShouldSwap: false,
	})
	require.NoError(t, err, "Finalize must succeed with all required deps wired (no nil-receiver / wiring errors)")
	require.NotNil(t, res)
	assert.False(t, res.Reused, "Step 1 dedupe-lookup returned count=0 → Reused must be false")

	// ── Assertion (a): tx-pointer identity ──
	// Every adapter received the SAME *sql.Tx as the caller. This
	// proves each adapter's write (had it been issued) would land in
	// the caller's tx — atomicity-by-pointer-identity.
	assert.Same(t, tx, repo.lastTx,
		"VoiceoverRepository.InsertTx (Step 3) MUST receive the caller's *sql.Tx (atomicity contract)")
	assert.Same(t, tx, lifecycle.lastTx,
		"LifecycleService.UpsertVoiceoverProjectionTx (Step 4, media_assets projection) MUST receive the caller's *sql.Tx")
	assert.Same(t, tx, outbox.lastTx,
		"Outbox.EnqueueIndexEvent (Step 5) MUST receive the caller's *sql.Tx (most-recent tx pointer)")

	// Step 1 (dedupe-lookup) MUST have been called once. The dedupe
	// is a tx-bound SELECT — proves the dedupe gate also runs in the
	// caller's tx.
	assert.Equal(t, 1, repo.dedupeN,
		"Step 1 (CountByDriveFileIDTx) MUST run inside the caller's tx (so the count is consistent with Step 3 INSERT)")
	assert.Equal(t, 1, repo.insertN, "Step 3 (InsertTx) MUST have been called once")
	assert.Equal(t, 1, repo.deleteN, "Step 2 (DeleteByIDTx) MUST have been called once")
	assert.Equal(t, 1, lifecycle.calls, "Step 4 (UpsertVoiceoverProjectionTx) MUST have been called once")
	assert.Equal(t, 1, outbox.indexEventCalls, "Step 5 (EnqueueIndexEvent) MUST have been called once")
	// Step 6 (cleanup outbox) is guard-skipped because ShouldSwap=false
	// → EnqueueCleanupEvent NOT called. This is the data-state guard
	// documented in finalizer.go step 6.
	assert.Equal(t, 0, outbox.cleanupEventCalls,
		"Step 6 (EnqueueCleanupEvent) MUST be guard-skipped when ShouldSwap=false (data-state guard, not wiring error)")

	// ── Assertion (b): rollback trick ──
	// Rollback the caller's tx. The voiceovers row written by Step 3
	// (InsertTx) MUST be undone. If it survives, the InsertTx was NOT
	// in the caller's tx (regression: finalizer opened its own tx).
	require.NoError(t, tx.Rollback(), "rollback after Finalize — proves writes were in the caller's tx")

	// Open a fresh read-only tx to verify the rollback undid the writes.
	readTx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = readTx.Rollback() }()

	var voiceoverCount int
	err = readTx.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM voiceovers WHERE id = ?`, "vo-atomicity").Scan(&voiceoverCount)
	require.NoError(t, err, "read-back query must succeed")
	assert.Equal(t, 0, voiceoverCount,
		"voiceovers row MUST NOT survive rollback (proves Step 3 InsertTx was in the caller's tx — atomicity contract)")

	// FinalizeResult.RequiredSteps MUST report all 3 required steps
	// as executed (Steps 4 + 5 + 6; Step 6 is guarded in this test).
	// This pins the per-step execution-state marker discipline
	// (audit P0 #2, July 2026).
	require.NotEmpty(t, res.RequiredSteps,
		"FinalizeResult.RequiredSteps MUST be populated (audit P0 #2 surface contract)")
	assert.Contains(t, res.RequiredSteps, "media_assets_projection: executed",
		"Step 4 MUST record 'media_assets_projection: executed' (RequiredStep marker)")
	assert.Contains(t, res.RequiredSteps, "index_outbox: executed",
		"Step 5 MUST record 'index_outbox: executed' (RequiredStep marker)")
	assert.Contains(t, res.RequiredSteps, "cleanup_outbox: guarded (ShouldSwap=false)",
		"Step 6 MUST record 'cleanup_outbox: guarded (ShouldSwap=false)' (data-state guard, not wiring error)")
}

// ─────────────────────────────────────────────────────────────────────
// Test 4 (TestParentAggregator_TriggeredOnlyAfterWaitingChildren)
// MOVED to parent_aggregator_eligibility_test.go by
// PR-SPLIT-VO-PARENT-AGG-TESTS (Step 5). The file no longer owns
// the eligibility-gate test. Run `go test -run
// TestParentEligibility_TriggeredOnlyAfterWaitingChildren ./internal/application/voiceover/jobs/...`
// for the migrated test (with sub-cases A/B/C covering waiting /
// succeeded / cancelled parent_state).
// ─────────────────────────────────────────────────────────────────────
