package jobs

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/payloadcap"
	_ "github.com/mattn/go-sqlite3"
)

// TestPersistJobResultCapsOversizedPayload pins audit DB-04 at the write path:
// a pathological result is stored as the explicit truncation envelope, while
// normal results keep their bytes. The cap is a storage guard, not a
// transformation of ordinary payloads.
func TestPersistJobResultCapsOversizedPayload(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE job_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id TEXT NOT NULL,
		attempt INTEGER NOT NULL,
		result_hash TEXT NOT NULL,
		codec_id TEXT NOT NULL,
		result_payload TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(job_id, attempt, result_hash))`); err != nil {
		t.Fatalf("schema: %v", err)
	}

	oversized := `{"data":"` + strings.Repeat("x", payloadcap.ResultBytes) + `"}`
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := persistJobResult(ctx, tx, "job-big", 0, oversized); err != nil {
		t.Fatalf("persist oversized: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var stored string
	if err := db.QueryRow(`SELECT result_payload FROM job_results WHERE job_id = 'job-big'`).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(stored, `"truncated":true`) {
		t.Fatalf("oversized payload was not capped: %d bytes stored", len(stored))
	}
	if len(stored) >= len(oversized) {
		t.Fatalf("capped payload (%d bytes) is not smaller than the original (%d bytes)", len(stored), len(oversized))
	}

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin small: %v", err)
	}
	small := `{"data":"ok"}`
	if err := persistJobResult(ctx, tx, "job-small", 0, small); err != nil {
		t.Fatalf("persist small: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit small: %v", err)
	}
	var storedSmall string
	if err := db.QueryRow(`SELECT result_payload FROM job_results WHERE job_id = 'job-small'`).Scan(&storedSmall); err != nil {
		t.Fatalf("read back small: %v", err)
	}
	if storedSmall != small {
		t.Fatalf("normal payload changed: got %q want %q", storedSmall, small)
	}
}
