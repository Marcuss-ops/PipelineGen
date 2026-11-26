package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func driveDeliveryStore(t *testing.T) (*sql.DB, *SQLiteStore) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, schema := range []string{
		`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT NOT NULL)`,
		`CREATE TABLE job_results (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, attempt INTEGER NOT NULL, result_hash TEXT NOT NULL, codec_id TEXT NOT NULL, result_payload TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(job_id, attempt, result_hash))`,
	} {
		if _, err := db.ExecContext(ctx, schema); err != nil {
			t.Fatal(err)
		}
	}
	return db, NewSQLiteStore(db, zap.NewNop())
}

// TestRecordArtifactDriveDeliveryMergesConfirmedReceipt pins the write half of
// the rendered/delivered split: the async outbox records the confirmed Drive
// identity beside the render evidence, without rewriting it.
func TestRecordArtifactDriveDeliveryMergesConfirmedReceipt(t *testing.T) {
	db, store := driveDeliveryStore(t)
	ctx := context.Background()
	const jobID = "job-final-video"
	initial := `{"run_id":"run-1","result":{"remote_final_job":{"job_id":"master-1","status":"SUCCEEDED","sha256":"` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `"}}}`
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs(id,status) VALUES(?,?)`, jobID, "SUCCEEDED"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_results(job_id,attempt,result_hash,codec_id,result_payload,created_at) VALUES(?,0,'initial','json',?,'now')`, jobID, initial); err != nil {
		t.Fatal(err)
	}

	first := DriveDelivery{ArtifactID: jobID + ":final_video", Kind: "video", DriveFileID: "drive-video-1", DriveLink: "https://drive/video-1", Status: "completed"}
	if err := store.RecordArtifactDriveDelivery(ctx, jobID, first); err != nil {
		t.Fatal(err)
	}
	// A later retry/sidecar delivery must merge, not replace the video receipt.
	second := DriveDelivery{ArtifactID: jobID + ":script_json", Kind: "script", DriveFileID: "drive-script-1", Status: "completed"}
	if err := store.RecordArtifactDriveDelivery(ctx, jobID, second); err != nil {
		t.Fatal(err)
	}

	var payload string
	if err := db.QueryRowContext(ctx, `SELECT result_payload FROM job_results WHERE job_id=? ORDER BY attempt DESC,id DESC LIMIT 1`, jobID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var got struct {
		RunID  string `json:"run_id"`
		Result struct {
			RemoteFinalJob struct {
				Status string `json:"status"`
				SHA256 string `json:"sha256"`
			} `json:"remote_final_job"`
			Deliveries map[string]DriveDelivery `json:"drive_deliveries"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatal(err)
	}
	if got.RunID != "run-1" || got.Result.RemoteFinalJob.Status != "SUCCEEDED" || got.Result.RemoteFinalJob.SHA256 == "" {
		t.Fatalf("render evidence changed: %s", payload)
	}
	if len(got.Result.Deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2: %+v", len(got.Result.Deliveries), got.Result.Deliveries)
	}
	if video := got.Result.Deliveries[jobID+":final_video"]; video.DriveFileID != "drive-video-1" || video.Kind != "video" || video.Status != "completed" {
		t.Fatalf("video delivery = %+v, want the confirmed Drive identity", video)
	}
}

// TestRecordArtifactDriveDeliveryRequiresTerminalJob keeps the projection
// honest: an outbox consumer that races the completion is retried instead of
// writing a delivery a later completion could overwrite.
func TestRecordArtifactDriveDeliveryRequiresTerminalJob(t *testing.T) {
	db, store := driveDeliveryStore(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs(id,status) VALUES('job-running','RUNNING')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_results(job_id,attempt,result_hash,codec_id,result_payload,created_at) VALUES('job-running',0,'h','json','{}','now')`); err != nil {
		t.Fatal(err)
	}
	err := store.RecordArtifactDriveDelivery(ctx, "job-running", DriveDelivery{ArtifactID: "job-running:final_video", Kind: "video", DriveFileID: "drive-1"})
	if err == nil {
		t.Fatal("expected a non-terminal job to be retried by the outbox")
	}
}
