package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	finalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const minimalSchema = `
CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'QUEUED',
	worker_id TEXT NOT NULL DEFAULT '',
	lease_id TEXT NOT NULL DEFAULT '',
	lease_expiry TEXT,
	revision INTEGER NOT NULL DEFAULT 0,
	retry_count INTEGER NOT NULL DEFAULT 0,
	result_json TEXT NOT NULL DEFAULT '',
	completed_at TEXT,
	started_at TEXT,
	cancelled_at TEXT,
	updated_at TEXT NOT NULL DEFAULT '',
	progress INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE job_events (
	id TEXT PRIMARY KEY,
	job_id TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	data_json TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE outbox_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	aggregate_id TEXT NOT NULL,
	aggregate_type TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}',
	event_key TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
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
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, retry_count, started_at, updated_at, created_at)
		 VALUES (?, 'test.job', 'RUNNING', ?, ?, ?, 0, ?, ?, ?)`,
		jobID, workerID, leaseID, leaseExpiry.UTC().Format(time.RFC3339), now, now, now,
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
		Artifacts: []finalization.PublishedArtifact{{
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
		}},
	}
}

type noopAssetTx struct{}

var _ finalization.AssetFinalizerTx = noopAssetTx{}

func (noopAssetTx) FinalizeAsset(_ context.Context, _ finalization.Transaction, artifact finalization.PublishedArtifact) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	return finalization.ArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		AssetID:       artifact.ArtifactID,
		Kind:          artifact.Kind,
		SourceVersion: artifact.SourceVersion,
		ContentHash:   artifact.SHA256,
		Location:      artifact.Location,
	}, nil, nil
}

func TestE2E_FingerprintPersistedInResultJSON(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-fp", "w-fp", "lease-fp"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry), json.RawMessage(`{"status":"ok"}`), "abc")
	res, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}
	if res.Status != "SUCCEEDED" {
		t.Fatalf("status = %q, want SUCCEEDED", res.Status)
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
		t.Fatalf("unmarshal result_json: %v", err)
	}
	if len(wrapped.CompletionFingerprint) != 64 {
		t.Fatalf("completion fingerprint len = %d, want 64", len(wrapped.CompletionFingerprint))
	}
}

func TestE2E_DoubleCompleteSameFingerprintIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-idemp", "w-idemp", "lease-idemp"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry), json.RawMessage(`{"status":"ok"}`), "abc")
	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("first completion: %v", err)
	}

	var resultBefore, completedBefore string
	if err := db.QueryRow(`SELECT result_json, completed_at FROM jobs WHERE id = ?`, jobID).Scan(&resultBefore, &completedBefore); err != nil {
		t.Fatalf("read pre-state: %v", err)
	}
	res, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil || res == nil || res.Status != "SUCCEEDED" {
		t.Fatalf("idempotent completion = (%+v, %v), want SUCCEEDED", res, err)
	}
	var resultAfter, completedAfter string
	if err := db.QueryRow(`SELECT result_json, completed_at FROM jobs WHERE id = ?`, jobID).Scan(&resultAfter, &completedAfter); err != nil {
		t.Fatalf("read post-state: %v", err)
	}
	if resultAfter != resultBefore || completedAfter != completedBefore {
		t.Fatal("idempotent completion rewrote durable result state")
	}
}

func TestE2E_DoubleCompleteDifferentResultReturnsConflict(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-cr", "w-cr", "lease-cr"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	lease := validLease(jobID, workerID, leaseID, expiry)
	if _, err := f.CompleteWithArtifacts(context.Background(), finalizationRequestWithArtifact(lease, json.RawMessage(`{"status":"ok"}`), "abc")); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	_, err := f.CompleteWithArtifacts(context.Background(), finalizationRequestWithArtifact(lease, json.RawMessage(`{"status":"different"}`), "abc"))
	if !errors.Is(err, finalization.ErrCompletionConflict) {
		t.Fatalf("err = %v, want ErrCompletionConflict", err)
	}
}

func TestE2E_DoubleCompleteDifferentArtifactsReturnsConflict(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-ca", "w-ca", "lease-ca"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	lease := validLease(jobID, workerID, leaseID, expiry)
	if _, err := f.CompleteWithArtifacts(context.Background(), finalizationRequestWithArtifact(lease, json.RawMessage(`{"status":"ok"}`), "abc")); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	_, err := f.CompleteWithArtifacts(context.Background(), finalizationRequestWithArtifact(lease, json.RawMessage(`{"status":"ok"}`), "xyz"))
	if !errors.Is(err, finalization.ErrCompletionConflict) {
		t.Fatalf("err = %v, want ErrCompletionConflict", err)
	}
}

func TestE2E_LeaseExpiryFenceSQLGated(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-exp", "w-exp", "lease-exp"
	pastExpiry := time.Now().Add(-10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, pastExpiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, pastExpiry), json.RawMessage(`{"status":"ok"}`), "abc")
	_, err := f.CompleteWithArtifacts(context.Background(), req)
	if !errors.Is(err, finalization.ErrLeaseExpired) {
		t.Fatalf("err = %v, want ErrLeaseExpired", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "SUCCEEDED" {
		t.Fatal("lease fence allowed a terminal success mutation")
	}
}

func TestE2E_LeaseExpiryNullIsAcceptedBySQLGated(t *testing.T) {
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-null", "w-null", "lease-null"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)

	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop())
	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry), json.RawMessage(`{"status":"ok"}`), "abc")
	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	var leaseExpiry sql.NullString
	if err := db.QueryRow(`SELECT lease_expiry FROM jobs WHERE id = ?`, jobID).Scan(&leaseExpiry); err != nil {
		t.Fatalf("read lease_expiry: %v", err)
	}
	if leaseExpiry.Valid {
		t.Fatalf("lease_expiry after success = %q, want NULL", leaseExpiry.String)
	}
	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("idempotent completion with NULL expiry: %v", err)
	}
}

func TestFinalizer_PublishesTypedJobCompletionEvent_PostFlipRevision(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-bus", "w-bus", "lease-bus"
	expiry := time.Now().Add(10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, expiry)
	if _, err := db.Exec(`UPDATE jobs SET revision = 0 WHERE id = ?`, jobID); err != nil {
		t.Fatalf("anchor revision: %v", err)
	}

	bus := finalize.NewBus()
	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop()).WithBus(bus)
	sub, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, expiry), json.RawMessage(`{"status":"ok"}`), "abc")
	res, err := f.CompleteWithArtifacts(context.Background(), req)
	if err != nil || res == nil || res.Status != "SUCCEEDED" {
		t.Fatalf("completion = (%+v, %v), want SUCCEEDED", res, err)
	}

	awaitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	evt, err := sub.Await(awaitCtx)
	if err != nil {
		t.Fatalf("await event: %v", err)
	}
	if evt.JobID != jobID || evt.FinalStatus != job.StatusSucceeded || evt.Err != nil || evt.Revision != 1 || evt.Attempt != req.Result.Attempt {
		t.Fatalf("unexpected completion event: %+v", evt)
	}

	sub2, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("resubscribe: %v", err)
	}
	defer sub2.Close()
	if _, err := f.CompleteWithArtifacts(context.Background(), req); err != nil {
		t.Fatalf("idempotent re-call: %v", err)
	}
	awaitCtx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, err = sub2.Await(awaitCtx2)
	if !errors.Is(err, finalize.ErrWaitTimedOut) {
		t.Fatalf("idempotent re-call await err = %v, want ErrWaitTimedOut", err)
	}
}

func TestFinalizer_NoPublishOnFailure(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	jobID, workerID, leaseID := "job-bus-fail", "w-bus-fail", "lease-bus-fail"
	pastExpiry := time.Now().Add(-10 * time.Minute)
	insertRunningJob(t, db, jobID, workerID, leaseID, pastExpiry)

	bus := finalize.NewBus()
	f := finalize.New(db, nil, noopAssetTx{}, zap.NewNop()).WithBus(bus)
	sub, err := bus.Subscribe(jobID)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	req := finalizationRequestWithArtifact(validLease(jobID, workerID, leaseID, pastExpiry), json.RawMessage(`{"status":"ok"}`), "abc")
	_, err = f.CompleteWithArtifacts(context.Background(), req)
	if !errors.Is(err, finalization.ErrLeaseExpired) {
		t.Fatalf("err = %v, want ErrLeaseExpired", err)
	}
	awaitCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = sub.Await(awaitCtx)
	if !errors.Is(err, finalize.ErrWaitTimedOut) {
		t.Fatalf("await err = %v, want ErrWaitTimedOut", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "SUCCEEDED" {
		t.Fatal("failure path published success or mutated durable state")
	}
}
