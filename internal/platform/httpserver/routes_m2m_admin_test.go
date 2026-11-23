package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sqlitem2m "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/m2m"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// newM2MAdminTestStore applies the canonical migration 252 shape to an
// in-memory DB plus the jobs table consumed by the quota projection, so the
// HTTP tests exercise the real store SQL rather than a fake.
func newM2MAdminTestStore(t *testing.T) (*sqlitem2m.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE m2m_clients (
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
		)`,
		`CREATE TABLE jobs (id TEXT PRIMARY KEY, client_id TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return sqlitem2m.NewStore(db, true), db
}

func newM2MAdminTestEngine(store *sqlitem2m.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1/admin/m2m")
	registerM2MKeyAdminRoutes(group, store, zap.NewNop())
	return engine
}

func doM2MAdmin(t *testing.T, engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestM2MAdminKeysCreateListRevoke(t *testing.T) {
	store, _ := newM2MAdminTestStore(t)
	engine := newM2MAdminTestEngine(store)

	w := doM2MAdmin(t, engine, http.MethodPost, "/api/v1/admin/m2m/keys",
		`{"client_id":"pc-remote-01","description":"second pc","scopes":["media.read","jobs.read"],"quota_max_scenes":25,"quota_max_total_secs":300}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var created struct {
		OK     bool   `json:"ok"`
		Secret string `json:"secret"`
		Client struct {
			ClientID string   `json:"client_id"`
			Scopes   []string `json:"scopes"`
			Enabled  bool     `json:"enabled"`
		} `json:"client"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Secret == "" || created.Client.ClientID != "pc-remote-01" || !created.Client.Enabled {
		t.Fatalf("create body = %s", w.Body.String())
	}
	if len(created.Client.Scopes) != 2 {
		t.Fatalf("scopes = %v", created.Client.Scopes)
	}

	// The plaintext secret must authenticate through the runtime lookup.
	client, err := store.LookupClient(t.Context(), store.HashClientSecret(created.Secret))
	if err != nil || client == nil {
		t.Fatalf("LookupClient after create: client=%v err=%v", client, err)
	}
	if client.QuotaMaxScenes != 25 || client.QuotaMaxTotalSecs != 300 {
		t.Fatalf("quota projection = %d/%d, want 25/300", client.QuotaMaxScenes, client.QuotaMaxTotalSecs)
	}

	// GET must never leak secret material.
	lw := doM2MAdmin(t, engine, http.MethodGet, "/api/v1/admin/m2m/keys", "")
	if lw.Code != http.StatusOK {
		t.Fatalf("list status = %d (body=%s)", lw.Code, lw.Body.String())
	}
	if strings.Contains(lw.Body.String(), created.Secret) || strings.Contains(lw.Body.String(), "secret") {
		t.Fatalf("list response leaked secret material: %s", lw.Body.String())
	}
	if !strings.Contains(lw.Body.String(), "pc-remote-01") {
		t.Fatalf("list response missing client: %s", lw.Body.String())
	}

	// Revoke via query param (the script-friendly form).
	rw := doM2MAdmin(t, engine, http.MethodDelete, "/api/v1/admin/m2m/keys?client_id=pc-remote-01", "")
	if rw.Code != http.StatusOK {
		t.Fatalf("revoke status = %d (body=%s)", rw.Code, rw.Body.String())
	}
	client, err = store.LookupClient(t.Context(), store.HashClientSecret(created.Secret))
	if err != nil || client == nil {
		t.Fatalf("LookupClient after revoke: client=%v err=%v", client, err)
	}
	if client.Enabled {
		t.Fatal("revoked client still reports enabled=true")
	}
}

func TestM2MAdminKeysRejectsDuplicateAndUnknownScope(t *testing.T) {
	store, _ := newM2MAdminTestStore(t)
	engine := newM2MAdminTestEngine(store)
	body := `{"client_id":"pc-remote-01","scopes":["media.read"]}`
	if w := doM2MAdmin(t, engine, http.MethodPost, "/api/v1/admin/m2m/keys", body); w.Code != http.StatusCreated {
		t.Fatalf("first create status = %d (body=%s)", w.Code, w.Body.String())
	}
	if w := doM2MAdmin(t, engine, http.MethodPost, "/api/v1/admin/m2m/keys", body); w.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if w := doM2MAdmin(t, engine, http.MethodPost, "/api/v1/admin/m2m/keys",
		`{"client_id":"pc-remote-02","scopes":["media.write"]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown scope status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if w := doM2MAdmin(t, engine, http.MethodDelete, "/api/v1/admin/m2m/keys/absent", ""); w.Code != http.StatusNotFound {
		t.Fatalf("revoke missing status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}
