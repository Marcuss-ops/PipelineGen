package m2m

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestStoreLookupClientHashesOnly(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE m2m_clients (client_id TEXT, secret_hash TEXT, scopes_json TEXT, enabled INTEGER, rate_limit_rps REAL NOT NULL DEFAULT 2, rate_limit_burst INTEGER NOT NULL DEFAULT 10, quota_max_scenes INTEGER NOT NULL DEFAULT 1000, quota_max_total_secs INTEGER NOT NULL DEFAULT 14400, last_used_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, client_id TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, true)
	secret := "velox_m2m_test_secret"
	_, err = db.Exec(`INSERT INTO m2m_clients(client_id, secret_hash, scopes_json, enabled, rate_limit_rps, rate_limit_burst, quota_max_scenes, quota_max_total_secs) VALUES (?, ?, ?, 1, 3, 7, 50, 600)`, "computer-editor-01", s.HashClientSecret(secret), `["jobs.submit","jobs.read"]`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO jobs(id, client_id, duration_ms) VALUES ('j1','computer-editor-01',12000),('j2','computer-editor-01',8000),('j3','other',99000)`)
	if err != nil {
		t.Fatal(err)
	}
	client, err := s.LookupClient(context.Background(), s.HashClientSecret(secret))
	if err != nil {
		t.Fatal(err)
	}
	if client == nil || client.ClientID != "computer-editor-01" || !client.Enabled || !client.HasScope("jobs.submit") {
		t.Fatalf("unexpected client projection: %#v", client)
	}
	if client.RateLimitRPS != 3 || client.RateLimitBurst != 7 {
		t.Fatalf("rate limit projection = %v/%d, want 3/7", client.RateLimitRPS, client.RateLimitBurst)
	}
	if client.QuotaMaxScenes != 50 || client.QuotaMaxTotalSecs != 600 {
		t.Fatalf("quota max projection = %d/%d, want 50/600", client.QuotaMaxScenes, client.QuotaMaxTotalSecs)
	}
	if client.QuotaUsedScenes != 2 || client.QuotaUsedTotalSecs != 20 {
		t.Fatalf("quota usage projection = %d/%d, want 2/20", client.QuotaUsedScenes, client.QuotaUsedTotalSecs)
	}
	if got, _ := s.LookupClient(context.Background(), s.HashClientSecret("wrong")); got != nil {
		t.Fatalf("wrong secret resolved client: %#v", got)
	}
}

func TestStoreTouchClientRecordsLastUsedAt(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE m2m_clients (client_id TEXT PRIMARY KEY, last_used_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO m2m_clients(client_id) VALUES ('client-a')`); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, true)
	if err := s.TouchClient(context.Background(), "client-a"); err != nil {
		t.Fatal(err)
	}
	var lastUsed sql.NullString
	if err := db.QueryRow(`SELECT last_used_at FROM m2m_clients WHERE client_id='client-a'`).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if !lastUsed.Valid || lastUsed.String == "" {
		t.Fatal("last_used_at was not recorded")
	}
}
