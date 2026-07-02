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

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
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
				ArtifactID:    "art-" + sha,
				Kind:          finalization.KindImage,
				Filename:      "frame.png",
				MIMEType:      "image/png",
				SizeBytes:     1234,
				SHA256:        sha,
				SourceVersion: 1,
				Required:      true,
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
