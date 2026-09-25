package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func TestRecordOverlayDriveLinkMergesCompletedLinks(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	const jobID = "job-overlay"
	initial := `{"run_id":"run-overlay","result":{"ok":true,"items":[{"item_id":"demo"}]}}`
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs(id,status) VALUES(?,?)`, jobID, "SUCCEEDED"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO job_results(job_id,attempt,result_hash,codec_id,result_payload,created_at) VALUES(?,0,'initial','json',?,'now')`, jobID, initial); err != nil {
		t.Fatal(err)
	}
	store := NewSQLiteStore(db, zap.NewNop())
	links := []OverlayDriveLink{
		{ItemID: "phrase", Language: "it", DriveFileID: "f1", DriveLink: "https://drive/f1", FolderID: "folder"},
		{ItemID: "image", Language: "it", DriveFileID: "f2", DriveLink: "https://drive/f2", FolderID: "folder"},
		{ItemID: "phrase", Language: "it", DriveFileID: "f1-new", DriveLink: "https://drive/f1-new", FolderID: "folder"},
	}
	for _, link := range links {
		if err := store.RecordOverlayDriveLink(ctx, jobID, link); err != nil {
			t.Fatal(err)
		}
	}
	var payload string
	if err := db.QueryRowContext(ctx, `SELECT result_payload FROM job_results WHERE job_id=? ORDER BY attempt DESC,id DESC LIMIT 1`, jobID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var got struct {
		RunID  string `json:"run_id"`
		Result struct {
			OK    bool               `json:"ok"`
			Items []json.RawMessage  `json:"items"`
			Links []OverlayDriveLink `json:"overlay_links"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatal(err)
	}
	if got.RunID != "run-overlay" || !got.Result.OK || len(got.Result.Items) != 1 {
		t.Fatalf("unrelated result data changed: %s", payload)
	}
	if len(got.Result.Links) != 2 {
		t.Fatalf("overlay links = %d, want 2: %+v", len(got.Result.Links), got.Result.Links)
	}
	if got.Result.Links[0].ItemID != "image" || got.Result.Links[1].DriveLink != "https://drive/f1-new" {
		t.Fatalf("links not stable/idempotent: %+v", got.Result.Links)
	}
}

func TestRecordOverlayDriveLinkWaitsForSuccessfulJob(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT NOT NULL); INSERT INTO jobs(id,status) VALUES('job-running','RUNNING'); CREATE TABLE job_results (id INTEGER PRIMARY KEY, job_id TEXT, attempt INTEGER, result_hash TEXT, codec_id TEXT, result_payload TEXT, created_at TEXT); INSERT INTO job_results VALUES(1,'job-running',0,'h','json','{}','now')`); err != nil {
		t.Fatal(err)
	}
	err = NewSQLiteStore(db, zap.NewNop()).RecordOverlayDriveLink(context.Background(), "job-running", OverlayDriveLink{ItemID: "phrase", Language: "it", DriveLink: "https://drive/f"})
	if err == nil {
		t.Fatal("expected non-terminal job to be retried by outbox")
	}
}
