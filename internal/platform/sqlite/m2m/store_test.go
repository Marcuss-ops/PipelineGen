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
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE m2m_clients (client_id TEXT, secret_hash TEXT, scopes_json TEXT, enabled INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, true)
	secret := "velox_m2m_test_secret"
	_, err = db.Exec(`INSERT INTO m2m_clients(client_id, secret_hash, scopes_json, enabled) VALUES (?, ?, ?, 1)`, "computer-editor-01", s.HashClientSecret(secret), `["jobs.submit","jobs.read"]`)
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
	if got, _ := s.LookupClient(context.Background(), s.HashClientSecret("wrong")); got != nil {
		t.Fatalf("wrong secret resolved client: %#v", got)
	}
}
