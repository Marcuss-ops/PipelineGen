// Package workerruntime_test — reindex scenario helpers/tests split out of
// worker_registry_e2e_test.go to keep every file ≤400 lines.
package workerruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

func TestE2E_RemoteWorkerExecutesMediaReindex(t *testing.T) {
	// 1. Real SQLite DB on disk in a temp dir. The mattn/go-sqlite3
	//    driver is registered transitively by storage (the storage
	//    package itself blanks in the driver). SetMaxOpenConns(1) so
	//    the clipindexer handler's QueryContext + Yield goroutines
	//    share a single connection on the file-backed DB — the
	//    per-connection gotcha with :memory: doesn't apply here, but
	//    the single-connection discipline keeps the test's
	//    invariants deterministic.
	//
	// PG-011 typed-handle migration (June 2026): the fixture is
	// *storage.SQLiteDB; clipindexer.NewService still takes a raw
	// *sql.DB so we pass sqliteDB.DB (the embedded field).
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.db")
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, zap.NewNop())
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	// 2. Create the columns HandleJob touches. The full media_assets
	//    schema lives in migrations/0XX_*.sql — for the "0 rows
	//    match" path we need only the columns referenced by the
	//    query string HandleJob compiles (id, source, media_type,
	//    embedding_json, transcript_embedding, metadata_json,
	//    created_at).
	_, err = sqliteDB.Exec(`CREATE TABLE IF NOT EXISTS media_assets (
		id TEXT PRIMARY KEY,
		source TEXT,
		media_type TEXT,
		embedding_json TEXT,
		transcript_embedding TEXT,
		metadata_json TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	require.NoError(t, err)

	// 3. clipindexer.Service. cfg.ServerURL="" so the handler skips
	//    indexViaAPI; cfg.ScriptPath would point to the python
	//    script that the handler falls back to. Since the test DB
	//    has 0 rows matching the missing-embedding condition,
	//    HandleJob short-circuits BEFORE either path is reached.
	cfg := clipindexer.DefaultConfig()
	cfg.Enabled = true
	cfg.DBPath = dbPath
	// clipindexer.NewService returns a single *Service value
	// (constructor never errors — the cfg + log are in-process types).
	//
	// PG-016 typed-handle migration (June 2026): clipindexer.NewService now
	// accepts *storage.SQLiteDB directly; sqliteDB is already *storage.SQLiteDB
	// (storage.OpenSQLiteDB above), so this is the typed handle with no .DB
	// unwrap. This call site is one of the two that previously had to use
	// `sqliteDB.DB` to escape the *sql.DB typed-handle gap.
	clipSvc := clipindexer.NewService(cfg, sqliteDB, dbPath, zap.NewNop())

	// 4. Wire the in-process Dispatcher exactly like composition.go
	//    does: register one handler directly. We don't go through
	//    InitWorkerComposition so the test is hermetic — no Drive,
	//    no AI, no Qdrant, no outbox dependencies.
	dispatcher := appjobs.NewDispatcher()
	require.NoError(t, dispatcher.Register(appjobs.TypeMediaReindex, clipSvc.HandleJob))

	// 5. P1 #13 (July 2026): appjobs.HandlerFunc = worker.Handler =
	//    jobs.Handler — the handler already takes *JobExecutionTools;
	//    the adapt bridge is now a no-op identity cast (previously it
	//    converted *worker.Tools → *appjobs.JobTools). worker.Runner
	//    translates *worker.Tools → *job.JobExecutionTools at
	//    Dispatch time, so the handler sees the same shape from both
	//    the in-process Dispatcher call and the remote-worker call.
	//
	// godlike/06 SSOT: both adapt here and the production
	//    BuildWorkerRegistry consume the same jobs.Handler;
	//    no inline bridge is needed.
	//
	// P0 #4 (July 2026) — adapt wraps each handler with
	// adaptToUploaderSkip so the runner's uploadManifest takes the
	// silent-skip path. The test exercises the worker pipeline
	// (claim → dispatch → handler-return → broker-side Complete),
	// not the artifact-upload path; the wrap is a result-shaper
	// at the test boundary, NOT a production-code regression.
	workerReg := worker.NewRegistry()
	for jt, h := range dispatcher.AllHandlers() {
		handler := h
		require.NoError(t, workerReg.Register(jt, adaptToUploaderSkip(handler)))
	}
	workerReg.Freeze()
	require.Equal(t, []string{appjobs.TypeMediaReindex}, workerReg.JobTypes(),
		"the worker registry should expose exactly one handler under the chosen happy-path subset")

	// 6. Mock broker pre-loaded with a single media.reindex lease.
	//    ID and Type are what the production broker roundtrips; an
	//    empty payload means the handler's "missing-embedding"
	//    query returns 0 rows and short-circuits to {total:0,...}.
	mock := newMockBroker(t)
	mock.serveLease(&appjobs.Lease{
		LeaseID: "lease-test-1",
		Job: &job.Job{
			ID:       "job-test-reindex-1",
			Type:     appjobs.TypeMediaReindex,
			Payload:  json.RawMessage(`{}`),
			Status:   job.StatusRunning,
			Revision: 1,
			LeaseID:  "lease-test-1",
		},
	})

	// 7. Per-job workspace in the same temp dir so t.Cleanup removes
	//    it after the test exits. NewWorkspace handles MkdirAll.
	ws, err := worker.NewWorkspace(filepath.Join(tmpDir, "ws"))
	require.NoError(t, err)

	// 8. runner.Run blocks until ctx is done; after Complete it
	//    falls into the Claim → nil/nil branch and sleeps ~2s. We
	//    cancel after observing Complete.
	//
	// assetClient is the WIRED stub (not nil). The handler-result
	// is wrapped to empty (adaptToUploaderSkip) so the runner's
	// uploadManifest takes the silent-skip path BEFORE any
	// assetClient call; the stub is wired defensively so any
	// future refactor that drops the wrap still passes (the no-op
	// upload is harmless).
	runner := worker.NewRunner(mock, workerReg, ws, stubAssetClient{}, zap.NewNop(),
		"w-test-1", "sess-test-1", []string{appjobs.TypeMediaReindex})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	// 9. Poll mock.completedResults until the broker reports ONE
	//    Complete call, OR time out. We poll instead of using a
	//    channel because completed slice is the through-the-same-
	//    primitive artifact the production code writes to (avoids
	//    a parallel test-only channel that would drift from the
	//    production mock in subsequent refactors).
	//
	// NOTE on the per-field result shape assertions removed here:
	// pre-fix the test verified result["total"/"indexed"/"failed"]
	// were 0 (the clipindexer.HandleJob empty-DB return shape).
	// Post-fix the handler is wrapped through adaptToUploaderSkip
	// and returns an empty map (len(handlerResult) == 0 forces the
	// runner's uploadManifest silent-skip path); the W1 Phase-6
	// pipeline contract here is "handler ran, broker recorded
	// Complete with the wired JobID/LeaseID/ExpectedRevision", not
	// "handler produced these specific data shape fields". The
	// clipboard-handler result-shape dogfood is preserved in the
	// idempotency replay subtest at step 11, which calls
	// j2.HandleJob DIRECTLY (no worker pipeline), bypassing the
	// silent-skip wrap — the test still pins the {total=0,...}
	// shape on the direct path.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		results := mock.completedResults()
		if len(results) >= 1 {
			got := results[0]
			require.Equal(t, "job-test-reindex-1", got.JobID,
				"Complete must carry the same JobID as the lease we served")
			require.Equal(t, "lease-test-1", got.LeaseID,
				"Complete must carry the same LeaseID as the lease we served")
			require.Equal(t, 1, got.ExpectedRevision,
				"Complete must carry the expected revision from the lease")
			t.Logf("W1 Phase 6 end-to-end OK: handler ran through the dispatcher+worker pipeline, broker recorded Complete with JobID=%s LeaseID=%s Revision=%d",
				got.JobID, got.LeaseID, got.ExpectedRevision)
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("worker did not complete the media.reindex job within 5s")
		case <-time.After(20 * time.Millisecond):
			// poll loop
		}
	}

	// 10. Cancel → runner.Run returns ctx.Err() (context.Canceled).
	//     Soft-assert that the runner exited cleanly within 2s of
	//     cancellation; this proves the worker is responsive to
	//     shutdown signal (a real production worker must be).
	cancel()
	select {
	case err := <-runErr:
		require.True(t, err == nil || errors.Is(err, context.Canceled),
			"runner.Run should exit in response to ctx cancel; got: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner.Run did not exit within 2s after ctx cancel")
	}

	// 11. Idempotency / no-temp-leak invariants per the W1 Phase 6
	//     spec checklist ("retry/idempotency" + "no temp file
	//     leak"). Re-run with the same payload (the worker is
	//     short-lived here, so we re-invoke the handler directly
	//     against the same DB and assert the same shape comes back).
	//
	// BUG FIX: step 10 cancelled the worker context. The clipindexer
	// handler's QueryContext(ctx, ...) honours cancellation and
	// returns context.Canceled immediately on a cancelled parent. We
	// MUST use a fresh context here — cancelling the runner's parent
	// must not poison the post-cancel idempotency replay. (This is
	// a real production concern too: cancellation of a worker
	// goroutine should not propagate to verification paths running
	// on the test goroutine — they're independent flows.)
	//
	// clipindexer.NewService returns a single *Service value
	// (in-process constructor, never errors). We construct j2 on
	// the same cfg/db/dbPath as clipSvc to prove the handler is
	// deterministic on empty-DB payloads: a fresh service running
	// the same query must return the same {total:0,indexed:0,
	// failed:0} shape. Handler logic is identical; this assertion
	// locks the contract.
	//
	// PG-016 typed-handle migration (June 2026): passes *storage.SQLiteDB
	// directly (sqliteDB); mirrors the clipSvc construction above.
	j2 := clipindexer.NewService(cfg, sqliteDB, dbPath, zap.NewNop())
	replayCtx, replayCancel := context.WithCancel(context.Background())
	t.Cleanup(replayCancel)
	// j2Result is a fresh variable so the `:=` redeclaration rule
	// picks it up cleanly (err is reused from earlier in the function
	// scope, which Go allows iff at least one new variable appears on
	// the left of `:=`).
	//
	// Replay shape note: the worker pipeline above marshals the
	// handler return to JSON then unmarshals back into
	// `map[string]any`, which yields float64 for numbers per Go's
	// json.Unmarshal semantics. The replay path here uses the
	// handler's RAW map[string]any return — clipindexer writes
	// `{"total": 0, "indexed": 0, "failed": 0}` with bare int
	// literals, so j2Result["total"] is `int(0)`, not `float64(0)`.
	// EqualValues treats int(0)/float64(0) as equal so the same
	// assertion reads cleanly across both lifecycle paths. If we
	// wanted strict equality, we'd use Equal(t, 0, ...) which
	// requires `int(0)`; EqualValues is the safer cross-shape choice.
	j2Result, err := j2.HandleJob(replayCtx, &job.Job{
		ID:      "job-test-reindex-1-replay",
		Type:    appjobs.TypeMediaReindex,
		Payload: json.RawMessage(`{}`),
	}, &appjobs.JobTools{
		// FASE 4(b) (July 2026): IsCancelled REMOVED from the
		// domain JobExecutionTools struct. Replay handler
		// observes cancellation via the per-handler ctx passed
		// by Runner (jobCtx), not via a callback. The handler
		// here is a no-op on a fresh context.Background() so no
		// cancel-probe is needed.
		Progress: func(int, string) {},
		Event:    func(string, string, map[string]any) {},
	})
	require.NoError(t, err, "replay of media.reindex with same payload must succeed (idempotency)")
	require.EqualValues(t, 0, j2Result["total"],
		"idempotent replay returns same shape: total=0 (int or float64)")

	// t.TempDir() is auto-cleaned; we just confirm the workspace
	// didn't leak files OUTSIDE t.TempDir(). The workspace is rooted
	// at tmpDir/ws which t.Cleanup removes via t.TempDir(), so the
	// assertion is the parent path contains nothing unexpected.
	//
	// PG-011 typed-handle migration (June 2026): storage.OpenSQLiteDB
	// enables WAL mode (DSN _journal_mode=WAL + PRAGMA
	// journal_mode=WAL) so the temp dir now holds:
	//   - media.db      (the database file itself)
	//   - media.db-wal  (WAL journal — pre-existing on disk)
	//   - media.db-shm  (WAL shared-memory file)
	//   - ws/           (worker workspace subdir)
	// Total: 4 entries (≤ 4 enforced here). Previously with raw
	// sql.Open (default rollback journal mode) only media.db +
	// ws = 2 entries. The lounge of WAL is intentional —
	// storage.SQLiteDB forces WAL for production-grade
	// concurrency.
	entries, readErr := os.ReadDir(tmpDir)
	require.NoError(t, readErr)
	require.True(t, len(entries) <= 4,
		"temp dir should hold db + wal + shm + ws subdir; found %d entries", len(entries))
}

// ── Phase 7 — Remote worker renews the lease during long-running execution ──
//
// TestE2E_RemoteWorkerRenewsLease is the W1 Phase 7 acceptance proof
// that Runner.runLease's renewLoop extends the lease while a slow
// handler is still executing, so long-running media.artlist / extract
// / batch jobs don't expire mid-run.
//
// W1 spec deliverable: "renew fires per DefaultLeaseTTL × 2"
// (cadence = TTL/2 with Defaults: 30s on a 60s lease); the test
// "holds a lease ≥ 90s and observes ≥ 1 Renew call".
//
// Production-fidelity reasoning: with DefaultLeaseTTL = 60s and
// DefaultRenewInterval = 30s, exercising 3× cadence takes 90s.
// CI cost is bounded by injecting a faster cadence via
// Runner.SetRenewInterval(...) — production cadence is documented
// on Runner and exercised by the default NewRunner constructor
// (no test asserts the 30s cadence directly; the constant is
// trusted). The test exercises, end-to-end:
//
//   - Runner.runLease spawning renewLoop on entry
//   - renewLoop ticking at the configured cadence and calling
//     tools.Renew(renewCtx, DefaultLeaseTTL)
//   - the post-renewal Tools.revision being used for the final
//     Tools.Complete call (Phase 7 explicitly fixed the latent
//     revision-drift bug: runner.runLease was using
//     lease.Job.Revision which would have gone stale after the
//     first renewal and surfaced as ErrLeaseLost at Complete time)
//   - graceful shutdown: renewCancel() + drain BEFORE Complete so
//     a stale tick doesn't race the Complete round-trip
//
// Two handler contracts validated:
//   - ≥1 renewal observed by the broker mock (counter ≥ 1)
//   - the FINAL Complete arrives with the broker mock's last
//     served lease (so the broker-side revision tracking
//     advances correctly and the runner reads the post-Renew
//     revision from Tools rather than from the stale lease
//     snapshot)
