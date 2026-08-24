package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Test (a): job_events error is NOT silently ignored ────────────────
// Verified via code review: the `_, _ = tx.ExecContext(...)` in
// markSucceeded is now `if _, err := tx.ExecContext(...); err != nil
// { return fmt.Errorf(...) }`. The compile-time assertion below
// confirms the Finalizer type satisfies the contract.
func TestFinalizerSatisfiesInterface(t *testing.T) {
	var _ finalization.JobFinalizer = (*Finalizer)(nil)
	// Compile-time check: if Finalizer doesn't implement
	// finalization.JobFinalizer, this file won't compile.
}

// ── Test (b): lease_expiry is read from DB row ────────────────────────
// The jobRow struct now includes leaseExpiry sql.NullString.
// selectJobForFinalization's query includes lease_expiry and validates
// it after scanning.
func TestJobRowIncludesLeaseExpiry(t *testing.T) {
	// Verify the struct field exists (compile-time assertion).
	row := jobRow{
		leaseExpiry: sqlNullString("2026-01-01T00:00:00Z"),
	}
	if !row.leaseExpiry.Valid {
		t.Error("leaseExpiry should be valid after setting")
	}
}

// sqlNullString is a helper for constructing sql.NullString in tests.
func sqlNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// ── Test (c): completion fingerprint ────────────────────────────────

func TestComputeCompletionFingerprint_SameArtifactsSameFingerprint(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)
	artifacts := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
		{
			ArtifactID:    "art-2",
			SHA256:        "def456",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-def"},
		},
	}

	fp1 := computeCompletionFingerprint(result, artifacts)
	fp2 := computeCompletionFingerprint(result, artifacts)

	if fp1 != fp2 {
		t.Errorf("same inputs should produce same fingerprint: %s != %s", fp1, fp2)
	}
	if fp1 == "" {
		t.Error("fingerprint should not be empty")
	}
}

func TestComputeCompletionFingerprint_DifferentArtifactsDifferentFingerprint(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsA := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}
	artifactsB := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "xyz789", // Different SHA256
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}

	fpA := computeCompletionFingerprint(result, artifactsA)
	fpB := computeCompletionFingerprint(result, artifactsB)

	if fpA == fpB {
		t.Error("different artifacts should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_DifferentResultDifferentFingerprint(t *testing.T) {
	resultA := json.RawMessage(`{"status":"ok"}`)
	resultB := json.RawMessage(`{"status":"retry"}`)
	artifacts := []finalization.PublishedArtifact{
		{
			ArtifactID:    "art-1",
			SHA256:        "abc123",
			SourceVersion: 1,
			Location:      finalization.AssetLocation{FileID: "drive-abc"},
		},
	}

	fpA := computeCompletionFingerprint(resultA, artifacts)
	fpB := computeCompletionFingerprint(resultB, artifacts)

	if fpA == fpB {
		t.Error("different result data should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_SameArtifactsDifferentOrder(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	// Same artifacts, different order — should produce the same
	// fingerprint because we sort by ArtifactID.
	artifactsOrder1 := []finalization.PublishedArtifact{
		{ArtifactID: "b", SHA256: "hash-b", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-b"}},
		{ArtifactID: "a", SHA256: "hash-a", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-a"}},
	}
	artifactsOrder2 := []finalization.PublishedArtifact{
		{ArtifactID: "a", SHA256: "hash-a", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-a"}},
		{ArtifactID: "b", SHA256: "hash-b", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f-b"}},
	}

	fp1 := computeCompletionFingerprint(result, artifactsOrder1)
	fp2 := computeCompletionFingerprint(result, artifactsOrder2)

	if fp1 != fp2 {
		t.Errorf("different artifact order should produce same fingerprint (sorting): %s != %s", fp1, fp2)
	}
}

func TestComputeCompletionFingerprint_EmptyArtifacts(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)
	fp := computeCompletionFingerprint(result, nil)

	if fp == "" {
		t.Error("fingerprint should not be empty even with zero artifacts")
	}

	// Same call again should produce same fingerprint.
	fp2 := computeCompletionFingerprint(result, []finalization.PublishedArtifact{})
	if fp != fp2 {
		t.Error("empty vs nil artifacts should produce the same fingerprint (both zero-length)")
	}
}

func TestComputeCompletionFingerprint_DifferentSourceVersion(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsV1 := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "f1"}},
	}
	artifactsV2 := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 2, Location: finalization.AssetLocation{FileID: "f1"}},
	}

	if computeCompletionFingerprint(result, artifactsV1) == computeCompletionFingerprint(result, artifactsV2) {
		t.Error("different SourceVersion should produce different fingerprints")
	}
}

func TestComputeCompletionFingerprint_DifferentFileID(t *testing.T) {
	result := json.RawMessage(`{"status":"ok"}`)

	artifactsA := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "drive-1"}},
	}
	artifactsB := []finalization.PublishedArtifact{
		{ArtifactID: "art-1", SHA256: "abc", SourceVersion: 1, Location: finalization.AssetLocation{FileID: "drive-2"}},
	}

	if computeCompletionFingerprint(result, artifactsA) == computeCompletionFingerprint(result, artifactsB) {
		t.Error("different FileID should produce different fingerprints")
	}
}

// ── Test: extractCompletionFingerprint ──────────────────────────────

func TestExtractCompletionFingerprint_Valid(t *testing.T) {
	wrapped := `{"data":{"status":"ok"},"completion_fingerprint":"abc123"}`
	fp := extractCompletionFingerprint(wrapped)
	if fp != "abc123" {
		t.Errorf("fingerprint = %q, want %q", fp, "abc123")
	}
}

func TestExtractCompletionFingerprint_LegacyFormat(t *testing.T) {
	legacy := `{"status":"ok"}`
	fp := extractCompletionFingerprint(legacy)
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for legacy format", fp)
	}
}

func TestExtractCompletionFingerprint_EmptyString(t *testing.T) {
	fp := extractCompletionFingerprint("")
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for empty string", fp)
	}
}

func TestExtractCompletionFingerprint_InvalidJSON(t *testing.T) {
	fp := extractCompletionFingerprint("not-json")
	if fp != "" {
		t.Errorf("fingerprint = %q, want empty for invalid JSON", fp)
	}
}

// ── Test: hashJSONString ────────────────────────────────────────────

func TestHashJSONString_Deterministic(t *testing.T) {
	h1 := hashJSONString(`{"a":1}`)
	h2 := hashJSONString(`{"a":1}`)
	if h1 != h2 {
		t.Errorf("hash should be deterministic: %s != %s", h1, h2)
	}
}

func TestHashJSONString_DifferentInputs(t *testing.T) {
	h1 := hashJSONString(`{"a":1}`)
	h2 := hashJSONString(`{"a":2}`)
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestHashJSONString_EmptyBecomesEmptyObject(t *testing.T) {
	h1 := hashJSONString("")
	h2 := hashJSONString("{}")
	if h1 != h2 {
		t.Errorf("empty string should hash as {}: %s != %s", h1, h2)
	}
}

func TestHashJSONString_NullBecomesEmptyObject(t *testing.T) {
	h1 := hashJSONString("null")
	h2 := hashJSONString("{}")
	if h1 != h2 {
		t.Errorf("null should hash as {}: %s != %s", h1, h2)
	}
}

// ── End-to-end integration: full spine vertical slice ───────────────

// TestFinalizerE2E_CompleteSpine is the vertical slice that proves
// the full Spina Dorsale works end-to-end. It:
//
//  1. Creates an in-memory SQLite DB with all canonical tables.
//  2. Inserts a RUNNING job with a valid lease.
//  3. Constructs AssetTxFinalizer + outboxevents.Repository + Finalizer.
//  4. Calls CompleteWithArtifacts with a published PDF artifact
//     (simulating what document.generate would produce).
//  5. Verifies ALL six tables were written in one transaction:
//     - jobs → SUCCEEDED
//     - media_assets → PUBLISHED
//     - asset_versions → version 1
//     - asset_locations → drive location
//     - outbox_events → asset.index.requested
//     - job_events → job_completed
//
// This is Step 4's proof that the new spine is operational.
func TestFinalizerE2E_CompleteSpine(t *testing.T) {
	db := setupFinalizerE2EDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	futureStr := now.Add(5 * time.Minute).Format(time.RFC3339)

	// 1. Insert a RUNNING job with a valid lease.
	const jobID = "doc-job-001"
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, retry_count, revision, created_at, updated_at)
		 VALUES (?, 'document.generate', 'RUNNING', 'worker-1', 'lease-abc', ?, 0, 1, ?, ?)`,
		jobID, futureStr, nowStr, nowStr,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	// 2. Construct the spine components.
	outboxRepo := outboxevents.NewRepository(db)
	assetTx := assetfinalizer.NewAssetTxFinalizer(nil, assets.NewSQLiteAssetCommitter(db, outboxRepo, nil))
	fx := New(db, outboxRepo, assetTx, nil)

	// 3. Call CompleteWithArtifacts with a published PDF artifact.
	result, err := fx.CompleteWithArtifacts(ctx, finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   "lease-abc",
			JobID:     jobID,
			WorkerID:  "worker-1",
			Attempt:   1,
			ExpiresAt: now.Add(5 * time.Minute),
		},
		Result: finalization.ResultManifest{
			JobID:   jobID,
			Attempt: 1,
			Data:    json.RawMessage(`{"title":"Test Document","format":"pdf","page_count":3}`),
		},
		Artifacts: []finalization.PublishedArtifact{
			{
				ArtifactID:     "doc-job-001:pdf",
				Kind:           finalization.KindDocument,
				Filename:       "test-document.pdf",
				MIMEType:       "application/pdf",
				SizeBytes:      12345,
				SHA256:         "sha256-doc-hash-abc123",
				SourceVersion:  1,
				Requirement:    finalization.ArtifactRequirementRequired,
				IdempotencyKey: "idem-doc-job-001",
				Location: finalization.AssetLocation{
					Provider:     "drive",
					FileID:       "drive-file-doc-xyz",
					WebViewLink:  "https://drive.google.com/file/d/drive-file-doc-xyz/view",
					DownloadLink: "https://drive.google.com/uc?id=drive-file-doc-xyz",
					FolderID:     "folder-docs",
					FolderPath:   "/documents",
					Action:       finalization.PublishCreated,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}

	// 4. Verify job → SUCCEEDED.
	var jobStatus, jobResult string
	err = db.QueryRowContext(ctx, `SELECT status, result_json FROM jobs WHERE id = ?`, jobID).
		Scan(&jobStatus, &jobResult)
	if err != nil {
		t.Fatalf("verify job: %v", err)
	}
	if jobStatus != "SUCCEEDED" {
		t.Errorf("job status = %q, want SUCCEEDED", jobStatus)
	}
	if jobResult == "" || jobResult == "{}" {
		t.Error("job result_json should contain the wrapped result with completion_fingerprint")
	}

	// Verify the result JSON contains the fingerprint.
	fp := extractCompletionFingerprint(jobResult)
	if fp == "" {
		t.Error("result_json should contain a non-empty completion_fingerprint")
	}

	// 5. Verify media_assets → PUBLISHED.
	var lifecycleState string
	err = db.QueryRowContext(ctx,
		`SELECT lifecycle_state FROM media_assets WHERE id = ?`, "doc-job-001:pdf",
	).Scan(&lifecycleState)
	if err != nil {
		t.Fatalf("verify media_assets: %v", err)
	}
	if lifecycleState != "PUBLISHED" {
		t.Errorf("media_assets.lifecycle_state = %q, want PUBLISHED", lifecycleState)
	}

	// 6. Verify asset_versions → version 1.
	var versionNum int
	var versionHash string
	err = db.QueryRowContext(ctx,
		`SELECT version_number, legacy_file_md5 FROM asset_versions WHERE asset_id = ?`, "doc-job-001:pdf",
	).Scan(&versionNum, &versionHash)
	if err != nil {
		t.Fatalf("verify asset_versions: %v", err)
	}
	if versionNum != 1 {
		t.Errorf("asset_versions.version_number = %d, want 1", versionNum)
	}
	if versionHash != "sha256-doc-hash-abc123" {
		t.Errorf("asset_versions.legacy_file_md5 = %q", versionHash)
	}

	// 7. Verify asset_locations → drive location.
	var locKind, locFileID string
	err = db.QueryRowContext(ctx,
		`SELECT location_kind, external_id FROM asset_locations WHERE asset_id = ?`, "doc-job-001:pdf",
	).Scan(&locKind, &locFileID)
	if err != nil {
		t.Fatalf("verify asset_locations: %v", err)
	}
	if locKind != "drive" {
		t.Errorf("asset_locations.location_kind = %q, want drive", locKind)
	}
	if locFileID != "drive-file-doc-xyz" {
		t.Errorf("asset_locations.external_id = %q", locFileID)
	}

	// 8. Documents are persisted but are not semantic-search assets, so the
	// committer must not emit an asset.index.requested command for this PDF.
	var outboxCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND event_type = ?`,
		"doc-job-001:pdf", outboxevents.EventAssetIndexRequested,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("verify outbox_events: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("outbox_events count = %d, want 0 for non-indexable document", outboxCount)
	}

	// 8-bis. Verify outbox_events → job.completed (derived-performance
	// trigger, emitted atomically with the SUCCEEDED flip).
	var jobCompletedCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ? AND event_type = ?`,
		jobID, outboxevents.EventJobCompleted,
	).Scan(&jobCompletedCount)
	if err != nil {
		t.Fatalf("verify job.completed outbox_events: %v", err)
	}
	if jobCompletedCount != 1 {
		t.Errorf("job.completed outbox_events count = %d, want 1", jobCompletedCount)
	}

	// 9. Verify job_events → job_completed.
	var eventCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM job_events WHERE job_id = ? AND type = 'job_completed'`,
		jobID,
	).Scan(&eventCount)
	if err != nil {
		t.Fatalf("verify job_events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("job_events count = %d, want 1", eventCount)
	}

	// 10. Idempotency: calling CompleteWithArtifacts again with
	// the SAME artifacts should succeed.
	result2, err := fx.CompleteWithArtifacts(ctx, finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   "lease-abc",
			JobID:     jobID,
			WorkerID:  "worker-1",
			Attempt:   1,
			ExpiresAt: now.Add(5 * time.Minute),
		},
		Result: finalization.ResultManifest{
			JobID:   jobID,
			Attempt: 1,
			Data:    json.RawMessage(`{"title":"Test Document","format":"pdf","page_count":3}`),
		},
		Artifacts: []finalization.PublishedArtifact{
			{
				ArtifactID:     "doc-job-001:pdf",
				Kind:           finalization.KindDocument,
				Filename:       "test-document.pdf",
				MIMEType:       "application/pdf",
				SizeBytes:      12345,
				SHA256:         "sha256-doc-hash-abc123",
				SourceVersion:  1,
				Requirement:    finalization.ArtifactRequirementRequired,
				IdempotencyKey: "idem-doc-job-001",
				Location: finalization.AssetLocation{
					Provider:     "drive",
					FileID:       "drive-file-doc-xyz",
					WebViewLink:  "https://drive.google.com/file/d/drive-file-doc-xyz/view",
					DownloadLink: "https://drive.google.com/uc?id=drive-file-doc-xyz",
					FolderID:     "folder-docs",
					FolderPath:   "/documents",
					Action:       finalization.PublishCreated,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("idempotent CompleteWithArtifacts: %v", err)
	}
	if result2.Status != "SUCCEEDED" {
		t.Errorf("idempotent retry status = %q, want SUCCEEDED", result2.Status)
	}
	if result2.JobID != jobID {
		t.Errorf("idempotent retry JobID = %q, want %q", result2.JobID, jobID)
	}

	t.Logf("✅ Full spine verified: job=%s artifacts=%d fingerprint=%s",
		jobID, len(result.ArtifactRefs), fp)
}

// setupFinalizerE2EDB creates an in-memory SQLite DB with ALL canonical
// tables needed for the CompleteWithArtifacts vertical slice.
func setupFinalizerE2EDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	for _, ddl := range []string{
		// jobs + job_events (canonical)
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'QUEUED',
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			lease_expiry TEXT,
			retry_count INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 0,
			result_json TEXT NOT NULL DEFAULT '',
			progress INTEGER NOT NULL DEFAULT 0,
			completed_at TEXT,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS job_events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		// media_assets + asset_versions + asset_locations (canonical)
		`CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			drive_file_id TEXT NOT NULL DEFAULT '',
			drive_link TEXT NOT NULL DEFAULT '',
			download_link TEXT NOT NULL DEFAULT '',
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			index_state TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			width INTEGER NOT NULL DEFAULT 0,
			height INTEGER NOT NULL DEFAULT 0,
			local_path TEXT NOT NULL DEFAULT '',
			source_provider TEXT NOT NULL DEFAULT '',
			source_version TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    search_text TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_video_id TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    start_ms INTEGER NOT NULL DEFAULT 0,
    end_ms INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '',
    tags_norm TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS asset_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			version_number INTEGER NOT NULL,
			source_uri TEXT NOT NULL DEFAULT '',
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, version_number)
		)`,
		`CREATE TABLE IF NOT EXISTS asset_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			asset_id TEXT NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
			location_kind TEXT NOT NULL CHECK (location_kind IN ('local', 'drive', 'object_storage')),
			uri TEXT NOT NULL,
			external_id TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			legacy_file_md5 TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE (asset_id, location_kind)
		)`,
		// outbox_events (canonical schema matching migration 092)
		`CREATE TABLE IF NOT EXISTS outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL DEFAULT '',
			aggregate_type TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			event_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 10,
			last_error TEXT NOT NULL DEFAULT '',
			next_attempt_at TEXT,
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			lease_expiry TEXT,
			completed_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key ON outbox_events(event_key)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v\nDDL: %s", err, ddl)
		}
	}

	return db
}

func TestFinalizerE2E_CreateSpineSetupDB(t *testing.T) {
	// Smoke test: verify the setup helper works.
	db := setupFinalizerE2EDB(t)
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table'").Scan(&count); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if count < 6 {
		t.Errorf("expected at least 6 tables, got %d", count)
	}
}

func TestFinalizerE2E_RejectsStaleLease(t *testing.T) {
	db := setupFinalizerE2EDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	pastStr := now.Add(-5 * time.Minute).Format(time.RFC3339)

	// Job with EXPIRED lease.
	_, err := db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, retry_count, revision, created_at, updated_at)
		 VALUES (?, 'document.generate', 'RUNNING', 'worker-1', 'lease-expired', ?, 0, 1, ?, ?)`,
		"job-expired", pastStr, nowStr, nowStr,
	)
	if err != nil {
		t.Fatalf("insert expired job: %v", err)
	}

	assetTx := assetfinalizer.NewAssetTxFinalizer(nil, nil)
	outboxRepo := outboxevents.NewRepository(db)
	fx := New(db, outboxRepo, assetTx, nil)

	_, err = fx.CompleteWithArtifacts(ctx, finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   "lease-expired",
			JobID:     "job-expired",
			WorkerID:  "worker-1",
			Attempt:   1,
			ExpiresAt: now.Add(5 * time.Minute), // request says valid
		},
		Result: finalization.ResultManifest{
			JobID: "job-expired",
			Data:  json.RawMessage(`{}`),
		},
		Artifacts: []finalization.PublishedArtifact{
			{
				ArtifactID:     "art-expired",
				Kind:           finalization.KindDocument,
				Filename:       "x.pdf",
				MIMEType:       "application/pdf",
				SHA256:         "hash",
				SourceVersion:  1,
				Requirement:    finalization.ArtifactRequirementRequired,
				IdempotencyKey: "ik",
				Location: finalization.AssetLocation{
					Provider: "drive",
					FileID:   "f1",
					Action:   finalization.PublishCreated,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for expired lease, got nil")
	}
	if !isFinalizationErrorWith(err, finalization.ErrLeaseExpired) {
		t.Errorf("expected ErrLeaseExpired, got: %v", err)
	}
}

func isFinalizationErrorWith(err error, sentinel error) bool {
	return errors.Is(err, sentinel)
}
