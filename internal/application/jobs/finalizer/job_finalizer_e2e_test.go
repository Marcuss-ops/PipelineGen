package finalizer_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// minimalSchema mirrors the columns the JobFinalizer reads/writes. The
// asset-related tables (media_assets/asset_versions/asset_locations) are
// NOT needed because the noopAssetTx mock bypasses the assetTx write
// surface entirely.
const minimalSchema = `
CREATE TABLE jobs (
	id             TEXT PRIMARY KEY,
	type           TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT 'QUEUED',
	worker_id      TEXT NOT NULL DEFAULT '',
	lease_id       TEXT NOT NULL DEFAULT '',
	lease_expiry   TEXT,
	revision       INTEGER NOT NULL DEFAULT 0,
	retry_count    INTEGER NOT NULL DEFAULT 0,
	result_json    TEXT NOT NULL DEFAULT '',
	completed_at   TEXT,
	started_at     TEXT,
	cancelled_at   TEXT,
	updated_at     TEXT NOT NULL DEFAULT '',
	progress       INTEGER NOT NULL DEFAULT 0,
	error          TEXT NOT NULL DEFAULT '',
	created_at     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE job_events (
	id          TEXT PRIMARY KEY,
	job_id      TEXT NOT NULL DEFAULT '',
	type        TEXT NOT NULL DEFAULT '',
	message     TEXT NOT NULL DEFAULT '',
	data_json   TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE outbox_events (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type     TEXT NOT NULL,
	aggregate_id   TEXT NOT NULL,
	aggregate_type TEXT NOT NULL DEFAULT '',
	payload_json   TEXT NOT NULL DEFAULT '{}',
	event_key      TEXT NOT NULL,
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);

CREATE UNIQUE INDEX ux_outbox_events_event_key ON outbox_events(event_key);
`

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open in-memory DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(minimalSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertRunningJob(t *testing.T, db *sql.DB, jobID, workerID, leaseID string, leaseExpiry time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, retry_count, started_at, updated_at, created_at)
		 VALUES (?, 'test.job', 'RUNNING', ?, ?, ?, 0, ?, ?, ?)`,
		jobID, workerID, leaseID, leaseExpiry.UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		t.Fatalf("insert running job: %v", err)
	}
}

func validLease(jobID, workerID, leaseID string, expiresAt time.Time) finalization.Lease {
	return finalization.Lease{LeaseID: leaseID, JobID: jobID, WorkerID: workerID, Attempt: 1, ExpiresAt: expiresAt}
}

func finalizationRequestWithArtifact(lease finalization.Lease, data json.RawMessage, sha string) finalization.FinalizationRequest {
	return finalization.FinalizationRequest{
		Lease:  lease,
		Result: finalization.ResultManifest{SchemaVersion: "v1", JobID: lease.JobID, Attempt: 1, Data: data},
		Artifacts: []finalization.PublishedArtifact{
			{
				ArtifactID:     "art-" + sha,
				Kind:           finalization.KindImage,
				Filename:       "frame.png",
				MIMEType:       "image/png",
				SizeBytes:      1234,
				SHA256:         sha,
				SourceVersion:  1,
				Requirement:    finalization.ArtifactRequirementRequired,
				IdempotencyKey: "ik-" + sha,
				Location: finalization.AssetLocation{
					Provider: "drive",
					FileID:   "drive-" + sha,
					Action:   finalization.PublishCreated,
				},
			},
		},
	}
}

// noopAssetTx satisfies finalization.AssetFinalizerTx with a value-type
// method (production concrete uses pointer-receiver, but finalizer.New
// now accepts the interface — value-receiver works because Go's
// interface satisfaction is value-vs-pointer agnostic at the
// implementation site).
type noopAssetTx struct{}

var _ finalization.AssetFinalizerTx = noopAssetTx{}

func (noopAssetTx) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	return finalization.ArtifactRef{
		ArtifactID: artifact.ArtifactID, AssetID: artifact.ArtifactID, Kind: artifact.Kind,
		SourceVersion: artifact.SourceVersion, ContentHash: artifact.SHA256, Location: artifact.Location,
	}, nil, nil
}

// ── Test (b): fingerprint persisted ─────────────────────────────────

func TestE2E_FingerprintPersistedInResultJSON(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-fp", "w-fp", "lease-fp"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "abc")

	res, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil {
		t.Fatalf("first CompleteWithArtifacts: %v", err)
	}
	if res.Status != "SUCCEEDED" {
		t.Errorf("res = %+v, want Status=SUCCEEDED", res)
	}

	var resultJSON string
	if err := db.QueryRow(`SELECT result_json FROM jobs WHERE id = ?`, jobID).Scan(&resultJSON); err != nil {
		t.Fatalf("read result_json: %v", err)
	}
	var wrapped struct {
		Data                  json.RawMessage `json:"data"`
		CompletionFingerprint string          `json:"completion_fingerprint"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &wrapped); err != nil {
		t.Fatalf("unmarshal wrapped: %v (json=%s)", err, resultJSON)
	}
	if wrapped.CompletionFingerprint == "" {
		t.Error("completion_fingerprint should be present in result_json")
	}
	if len(wrapped.CompletionFingerprint) != 64 {
		t.Errorf("fingerprint len=%d, want 64 (SHA-256 hex)", len(wrapped.CompletionFingerprint))
	}
}

// ── Test (d): double-completion idempotency ──────────────────────────

// TestE2E_DoubleCompleteSameFingerprintIsIdempotent: the FIRST call's
// result is discarded (we read the DB before the second call to anchor
// the canonical invariant that an idempotent re-call does NOT rewrite
// the row's result_json or completed_at — the canonical
// idempotent-success invariant per Piano d'Azione §4.5).
func TestE2E_DoubleCompleteSameFingerprintIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-idemp", "w-idemp", "lease-idemp"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "abc")

	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("first call: %v", err)
	}

	var rjBefore, caBefore string
	if err := db.QueryRow(`SELECT result_json, completed_at FROM jobs WHERE id = ?`, jobID).Scan(&rjBefore, &caBefore); err != nil {
		t.Fatalf("read pre-state: %v", err)
	}

	r2, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil {
		t.Fatalf("second call (idempotent): %v", err)
	}
	if r2 == nil || r2.Status != "SUCCEEDED" {
		t.Errorf("r2 = %+v, want non-nil SUCCEEDED", r2)
	}

	var rjAfter, caAfter string
	if err := db.QueryRow(`SELECT result_json, completed_at FROM jobs WHERE id = ?`, jobID).Scan(&rjAfter, &caAfter); err != nil {
		t.Fatalf("read post-state: %v", err)
	}
	if rjAfter != rjBefore {
		t.Errorf("idempotent call MUST NOT rewrite result_json")
	}
	if caAfter != caBefore {
		t.Errorf("idempotent call MUST NOT rewrite completed_at")
	}
}

func TestE2E_DoubleCompleteDifferentResultReturnsConflict(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-cr", "w-cr", "lease-cr"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req1 := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "abc")
	if _, err := f.CompleteWithArtifacts(context.Background(), req1); err != nil {
		t.Fatalf("first: %v", err)
	}

	req2 := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"different"}`), "abc")
	_, err := f.CompleteWithArtifacts(context.Background(), req2)
	if err == nil {
		t.Fatal("want ErrCompletionConflict on different result data, got nil")
	}
	if !errors.Is(err, finalization.ErrCompletionConflict) {
		t.Errorf("err = %v, want ErrCompletionConflict", err)
	}
}

func TestE2E_DoubleCompleteDifferentArtifactsReturnsConflict(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-ca", "w-ca", "lease-ca"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req1 := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "abc")
	if _, err := f.CompleteWithArtifacts(context.Background(), req1); err != nil {
		t.Fatalf("first: %v", err)
	}

	req2 := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "xyz")
	_, err := f.CompleteWithArtifacts(context.Background(), req2)
	if err == nil {
		t.Fatal("want ErrCompletionConflict on different artifact SHA256, got nil")
	}
	if !errors.Is(err, finalization.ErrCompletionConflict) {
		t.Errorf("err = %v, want ErrCompletionConflict", err)
	}
}

// ── Test (a): SQL lease_expiry fence ─────────────────────────────────

func TestE2E_LeaseExpiryFenceSQLGated(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-exp", "w-exp", "lease-exp"
	pastExpiry := time.Now().Add(-10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, pastExpiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, pastExpiry),
		json.RawMessage(`{"status":"ok"}`), "abc")

	_, err := f.CompleteWithArtifacts(context.Background(), req)
	if err == nil {
		t.Fatal("want ErrLeaseExpired for past expiry, got nil")
	}
	if !errors.Is(err, finalization.ErrLeaseExpired) {
		t.Errorf("err = %v, want ErrLeaseExpired", err)
	}

	var st string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&st); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st == "SUCCEEDED" {
		t.Errorf("status = %q, want NON-SUCCEEDED (fence must reject before mutation)", st)
	}
}

func TestE2E_LeaseExpiryNullIsAcceptedBySQLGated(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-null", "w-null", "lease-null"
	futureExpiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, futureExpiry)

	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, futureExpiry),
		json.RawMessage(`{"status":"ok"}`), "abc")

	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("first (future expiry): %v", err)
	}

	var le sql.NullString
	if err := db.QueryRow(`SELECT lease_expiry FROM jobs WHERE id = ?`, jobID).Scan(&le); err != nil {
		t.Fatalf("read lease_expiry: %v", err)
	}
	if le.Valid {
		t.Errorf("lease_expiry after SUCCEEDED should be NULL, got %q", le.String)
	}

	// Second call after SUCCEEDED — lease_expiry is NULL → the SQL
	// fence's `IS NULL OR > CURRENT_TIMESTAMP` clause accepts →
	// handleIdempotentCompletion kicks in.
	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("second (NULL expiry after SUCCEEDED): %v", err)
	}
}

// ── FASE 6 Cut 6.5 typed JobCompletionBus integration ──────────────

// TestFinalizer_PublishesTypedJobCompletionEvent_PostFlipRevision
// pins the FASE 6 Cut 6.5 contract: when JobFinalizer.CompleteWithArtifacts
// flips a job row to SUCCEEDED, the canonical completion.JobCompletionBus
// MUST publish a typed JobCompletionEvent whose Revision field equals
// the post-flip optimistic-concurrency counter (jobRow.revision + 1).
//
// It covers THREE invariants in one shot — the load-bearing part of
// user-spec Cut 6.5 step 4.
//
//  1. Publish fires on the SUCCEEDED tx.Commit path. The post-flip
//     Revision assertion alone proves the SUCCEEDED path fired
//     (handleIdempotentCompletion's no-publish early-return cannot
//     reach the post-commit branch).
//  2. evt.Revision = jobRow.revision + 1 — the post-flip OC counter,
//     NOT req.Result.Attempt. A future reader changing markSucceeded's
//     UPDATE statement to `revision = revision + 2` (silent value
//     drift) would fail THIS assertion.
//  3. evt.FinalStatus = jobs.StatusSucceeded (typed-port invariant:
//     the Finalizer's terminal transition is shaped as a typed event,
//     not a free-form string).
//
// Sister test TestFinalizer_NoPublishOnFailure (below) covers the
// failure-path-no-phantom-publish half of the contract.
//
// godlike/06 SSOT placement: this test lives in finalizer_test (the
// canonical Finalizer e2e package) NOT cancellation_test.go because
// package-level imports of `internal/application/jobs` form a closed
// cycle when cancellation_test.go imports finalizer+completion (see
// the FASE 6 Cut 6.5 commit body for the cycle path). Reuses the
// existing e2e helpers (openTestDB, insertRunningJob, validLease,
// finalizationRequestWithArtifact, noopAssetTx) — zero new helper
// code. Inserted revision=0 is the schema default (mirrors the
// existing e2e tests' implicit-invariant pattern).
func TestFinalizer_PublishesTypedJobCompletionEvent_PostFlipRevision(t *testing.T) {
	t.Parallel()

	// ── Canonical Finalizer e2e harness: in-memory SQLite + 1 RUNNING
	// job row with revision=0 (schema default → post-flip = 0+1 = 1).
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-bus", "w-bus", "lease-bus"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	// Belt-and-braces pin of the pre-flip revision invariant. The
	// schema default is revision=0 but a future reader changing the
	// default could invalidate this test's `Revision == 1` assertion
	// without any other test failing. The explicit UPDATE below
	// anchors the test's pre-flip invariant in the test itself.
	if _, err := db.Exec(`UPDATE jobs SET revision = 0 WHERE id = ?`, jobID); err != nil {
		t.Fatalf("anchor pre-flip revision=0: %v", err)
	}

	// ── Cut 6.5 fluent composition-root pattern: NewBus() → WithBus().
	bus := completion.NewBus()
	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop()).WithBus(bus)

	// ── Subscribe BEFORE the SUCCEEDED commit so the post-commit
	// Publish reaches a live subscriber (canonical migration template).
	sub, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("bus.Subscribe(%s): %v", jobID, err)
	}
	defer sub.Close()

	// ── Drive a real CompleteWithArtifacts through the Finalizer.
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry),
		json.RawMessage(`{"status":"ok"}`), "abc")
	res, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}
	if res == nil || res.Status != "SUCCEEDED" {
		t.Fatalf("res = %+v, want non-nil SUCCEEDED", res)
	}

	// ── Await the typed event with a generous budget (SQLite commit
	// + post-commit Publish is single-digit ms in practice; 2s is
	// generous slack for CI). The Await MUST return the typed event;
	// anything else is a contract violation.
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer awaitCancel()
	evt, err := sub.Await(awaitCtx)
	if err != nil {
		t.Fatalf("sub.Await did not receive a JobCompletionEvent: %v", err)
	}

	// ── Invariant 1: typed JobID + FinalStatus + nil Err (the
	// canonical typed-port contract — the bus payload MUST be typed,
	// NOT free-form string).
	if evt.JobID != jobID {
		t.Errorf("evt.JobID = %q, want %q", evt.JobID, jobID)
	}
	if evt.FinalStatus != jobs.StatusSucceeded {
		t.Errorf("evt.FinalStatus = %q, want %q (StatusSucceeded)",
			evt.FinalStatus, jobs.StatusSucceeded)
	}
	if err := evt.Err; err != nil {
		t.Errorf("evt.Err = %v, want nil on SUCCEEDED event", err)
	}

	// ── Invariant 2 (load-bearing per FASE 6 Cut 6.5 part-3):
	// evt.Revision == jobRow.revision + 1 (post-flip OC counter).
	// Pre-flip = 0 (pinned above); markSucceeded does `revision =
	// revision + 1` atomically inside the tx → post-flip = 1.
	if evt.Revision != 1 {
		t.Fatalf("evt.Revision = %d, want 1 (post-flip OC counter == jobRow.revision+1)", evt.Revision)
	}

	// ── Invariant 3: evt.Attempt matches the request's retry
	// counter (a counter-attempt, NOT a row-revision — the
	// disambiguation is the point of the two-field split per
	// code-reviewer recommendation).
	if evt.Attempt != req.Result.Attempt {
		t.Errorf("evt.Attempt = %d, want %d (req.Result.Attempt)",
			evt.Attempt, req.Result.Attempt)
	}

	// ── Invariant 4: the bus does NOT emit a phantom SUCCEEDED on
	// the idempotent re-call path (the worker-side post-call sees
	// the SAME fingerprint → handleIdempotentCompletion early-returns
	// and does NOT re-publish). A FRESH subscriber on the same jobID
	// MUST observe NO event arriving on the 2nd Publish-path.
	//
	// Test design: re-subscribe (the existing `sub` is already
	// drained from the 1st Await — using it again would conflate
	// the single-shot-drain contract with the no-phantom-publish
	// contract, which is exactly the bug the prior-turn review
	// caught). The 2nd CompleteWithArtifacts with the same request
	// triggers handleIdempotentCompletion (no Publish fires). The
	// fresh subscriber's Await with 200ms budget MUST time out — a
	// phantom event arrival here would FAIL this assertion
	// (godlike/07 fail-closed: bus does not lie about success on
	// the idempotent re-call path).
	sub2, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("bus.Subscribe(%s) (re-subscribe for no-republish check): %v", jobID, err)
	}
	defer sub2.Close()

	res2, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotent re-call should succeed silently: %v", err)
	}
	if res2 == nil || res2.Status != "SUCCEEDED" {
		t.Fatalf("res2 = %+v, want non-nil SUCCEEDED (idempotent)", res2)
	}
	awaitCtx2, awaitCancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer awaitCancel2()
	_, err = sub2.Await(awaitCtx2)
	if err == nil {
		t.Errorf("idempotent re-call MUST NOT re-publish a JobCompletionEvent (a phantom event was received on a fresh subscriber)")
	} else if !errors.Is(err, completion.ErrWaitTimedOut) {
		t.Errorf("idempotent re-call Await err = %v, want ErrWaitTimedOut (no phantom event on fresh subscriber)", err)
	}
}

// TestFinalizer_NoPublishOnFailure pins the FAILURE-PATH half of
// the Cut 6.5 contract: when JobFinalizer.CompleteWithArtifacts
// returns a typed error (e.g., ErrLeaseExpired via past-expiry
// lease), the bus MUST NOT publish a phantom SUCCEEDED event. A
// waiter awaiting job completion MUST observe
// completion.ErrWaitTimedOut (subscribed but no Publish fired).
//
// This is the canonical "bus does not lie about success" guard
// (godlike/07 fail-closed: a misfire here would fool downstream
// handlers into thinking a job SUCCEEDED when it actually errored,
// silently losing the failure signal). The pattern is the mirror of
// TestFinalizer_PublishesTypedJobCompletionEvent_PostFlipRevision:
// same harness, but with a past-expiry lease that the lease-fence
// SELECT rejects.
func TestFinalizer_NoPublishOnFailure(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	jobID, workerID, leaseID := "job-bus-fail", "w-bus-fail", "lease-bus-fail"
	// Past-expiry triggers the SQL lease-fence's rejection BEFORE
	// any COMMIT — the typed-error path mirrors the existing
	// TestE2E_LeaseExpiryFenceSQLGated pattern.
	pastExpiry := time.Now().Add(-10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, pastExpiry)

	bus := completion.NewBus()
	f := finalizer.New(db, nil, noopAssetTx{}, zap.NewNop()).WithBus(bus)

	sub, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("bus.Subscribe(%s): %v", jobID, err)
	}
	defer sub.Close()

	// Drive CompleteWithArtifacts with the past-expiry lease — the
	// SQL fence rejects, CompleteWithArtifacts returns ErrLeaseExpired.
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, pastExpiry),
		json.RawMessage(`{"status":"ok"}`), "abc")
	_, err = f.CompleteWithArtifacts(context.Background(), req)
	if err == nil {
		t.Fatal("CompleteWithArtifacts should return ErrLeaseExpired on past-expiry lease; got nil")
	}
	if !errors.Is(err, finalization.ErrLeaseExpired) {
		t.Errorf("err = %v, want ErrLeaseExpired", err)
	}

	// The lease-fence rejection MUST NOT fire a JobCompletionEvent
	// (the canonical Publish hook is the SUCCESSFUL tx.Commit branch
	// only — see Cut 6.5 part-2 doc on CompleteWithArtifacts). Await
	// must time out — a phantom event arriving here would FAIL this
	// assertion (godlike/07 fail-closed: bus does not lie about
	// success).
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer awaitCancel()
	_, err = sub.Await(awaitCtx)
	if err == nil {
		t.Errorf("a phantom SUCCEEDED event was published on the failure path — the bus MUST NOT lie about success")
	} else if !errors.Is(err, completion.ErrWaitTimedOut) {
		t.Errorf("Await err = %v, want ErrWaitTimedOut (no phantom event on failure path)", err)
	}

	// ── Belt-and-braces: confirm the row is NOT in SUCCEEDED state
	// (mutual cross-check with TestE2E_LeaseExpiryFenceSQLGated's
	// post-conditions).
	var st string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&st); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st == "SUCCEEDED" {
		t.Errorf("status = %q, want NON-SUCCEEDED (fence rejected before any mutation, and bus MUST mirror the durable state)", st)
	}
}
