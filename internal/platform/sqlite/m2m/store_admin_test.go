package m2m

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	_ "github.com/mattn/go-sqlite3"
)

func newAdminTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE m2m_clients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		client_id TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL DEFAULT '',
		secret_hash TEXT NOT NULL UNIQUE,
		scopes_json TEXT NOT NULL DEFAULT '[]',
		enabled INTEGER NOT NULL DEFAULT 1,
		rate_limit_rps REAL NOT NULL DEFAULT 2,
		rate_limit_burst INTEGER NOT NULL DEFAULT 10,
		quota_max_scenes INTEGER NOT NULL DEFAULT 1000,
		quota_max_total_secs INTEGER NOT NULL DEFAULT 14400,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_used_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, true)
}

func TestStoreCreateListRevokeClient(t *testing.T) {
	store := newAdminTestStore(t)
	ctx := context.Background()
	client, secret, err := store.CreateClient(ctx, mw.M2MKeyCreateParams{
		ClientID: "remote-01", Scopes: []string{"jobs.submit", "media.read"},
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if client.ClientID != "remote-01" || secret == "" {
		t.Fatalf("client/secret = %#v / %q", client, secret)
	}

	if _, _, err := store.CreateClient(ctx, mw.M2MKeyCreateParams{ClientID: "remote-01", Scopes: []string{"jobs.read"}}); !errors.Is(err, mw.ErrM2MClientExists) {
		t.Fatalf("duplicate create err = %v, want ErrM2MClientExists", err)
	}

	clients, err := store.ListClients(ctx)
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(clients) != 1 || clients[0].ClientID != "remote-01" || !clients[0].Enabled {
		t.Fatalf("clients = %#v", clients)
	}
	if len(clients[0].Scopes) != 2 {
		t.Fatalf("scopes = %#v", clients[0].Scopes)
	}

	if err := store.RevokeClient(ctx, "remote-01"); err != nil {
		t.Fatalf("RevokeClient: %v", err)
	}
	clients, err = store.ListClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if clients[0].Enabled {
		t.Fatal("client is still enabled after revoke")
	}
	if err := store.RevokeClient(ctx, "missing"); !errors.Is(err, mw.ErrM2MClientNotFound) {
		t.Fatalf("revoke missing err = %v, want ErrM2MClientNotFound", err)
	}
}
