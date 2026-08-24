// Package main — tests for the semantic search readiness canary
// (PR-AGENTE2-READINESS — Agente 2, Azione 5, July 2026).
package qdrant

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// stubSemanticSearcher implements search.SearchFanOut for tests.
type stubSemanticSearcher struct {
	searchFn func(ctx context.Context, q search.Query) (*search.Result, error)
}

func (s *stubSemanticSearcher) Search(ctx context.Context, q search.Query) (*search.Result, error) {
	if s.searchFn != nil {
		return s.searchFn(ctx, q)
	}
	return &search.Result{}, nil
}

func (s *stubSemanticSearcher) Stats() map[string]search.BackendStats {
	return nil
}

// openSemanticTestDB creates an in-memory SQLite DB with the
// media_assets schema and inserts a canary row.
func openSemanticTestDB(t *testing.T, assetID, workspaceID, searchText, lifecycleState, embedding string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS media_assets (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			search_text TEXT,
			name TEXT,
			lifecycle_state TEXT,
			embedding_json TEXT,
			local_path TEXT,
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    asset_version TEXT NOT NULL DEFAULT '',
    asset_location TEXT NOT NULL DEFAULT '',
    rendition TEXT NOT NULL DEFAULT '',
    source_provider TEXT NOT NULL DEFAULT '',
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
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
	`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO media_assets (id, workspace_id, search_text, name, lifecycle_state, embedding_json) VALUES (?, ?, ?, ?, ?, ?)`,
		assetID, workspaceID, searchText, searchText, lifecycleState, embedding,
	); err != nil {
		t.Fatalf("INSERT canary: %v", err)
	}
	return db
}

// TestReadinessFailsWithoutSemanticBackend verifies the check
// fails when SemanticSearch is nil.
func TestReadinessFailsWithoutSemanticBackend(t *testing.T) {
	db := openSemanticTestDB(t, "asset-001", "ws-prod", "sunset", "ACTIVE", "[0.1,0.2]")
	defer db.Close()

	deps := readinessDeps{
		DB:  db,
		Log: zap.NewNop(),
		Root: &compositionRoot{
			SemanticSearch: nil,
		},
	}

	res := checkSemanticSearchReal(context.Background(), deps)
	if res.Pass {
		t.Error("expected fail when SemanticSearch is nil, got pass")
	}
	if !strings.Contains(res.Err, "not wired") {
		t.Errorf("error should mention not wired, got: %s", res.Err)
	}
}

// TestReadinessRunsRealSemanticSearch verifies the happy path.
func TestReadinessRunsRealSemanticSearch(t *testing.T) {
	db := openSemanticTestDB(t, "asset-sunset", "ws-prod", "beautiful sunset over mountains", "ACTIVE", "[0.1,0.2,0.3]")
	defer db.Close()

	searcher := &stubSemanticSearcher{
		searchFn: func(_ context.Context, q search.Query) (*search.Result, error) {
			if q.Actor.WorkspaceID != "ws-prod" {
				// Cross-workspace call: return empty (no canary). The
				// readiness check requires a DIFFERENT workspace to NOT
				// see the canary asset; returning empty here is the
				// canonical "workspace isolation working" stub. Pre-fix
				// (2026-07-04) this branch logged t.Errorf and STILL
				// returned the canary items, which made the happy-path
				// test fail with a false cross-workspace-leak detection.
				return &search.Result{}, nil
			}
			return &search.Result{
				Items: []search.Candidate{
					{AssetID: "asset-sunset", Title: "Sunset", Score: 0.95},
					{AssetID: "other-002", Title: "Ocean", Score: 0.80},
				},
			}, nil
		},
	}

	deps := readinessDeps{
		DB:  db,
		Log: zap.NewNop(),
		Root: &compositionRoot{
			SemanticSearch: searcher,
		},
	}

	res := checkSemanticSearchReal(context.Background(), deps)
	if !res.Pass {
		t.Errorf("expected pass, got fail: %s", res.Err)
	}
}

// TestReadinessRejectsCrossWorkspaceResult verifies cross-workspace isolation.
func TestReadinessRejectsCrossWorkspaceResult(t *testing.T) {
	db := openSemanticTestDB(t, "asset-leak", "ws-alpha", "leak test", "ACTIVE", "[0.9]")
	defer db.Close()

	searcher := &stubSemanticSearcher{
		searchFn: func(_ context.Context, q search.Query) (*search.Result, error) {
			// Always return the canary asset — regardless of workspace
			return &search.Result{
				Items: []search.Candidate{
					{AssetID: "asset-leak", Title: "Leaked", Score: 0.99},
				},
			}, nil
		},
	}

	deps := readinessDeps{
		DB:  db,
		Log: zap.NewNop(),
		Root: &compositionRoot{
			SemanticSearch: searcher,
		},
	}

	res := checkSemanticSearchReal(context.Background(), deps)
	if res.Pass {
		t.Error("expected cross-workspace leak detection to fail the check, got pass")
	}
	if !strings.Contains(res.Err, "cross-workspace") {
		t.Errorf("error should mention cross-workspace, got: %s", res.Err)
	}
}

// TestReadinessSemanticCanaryUnavailable verifies the check fails
// when no ACTIVE+indexed asset exists in SQLite.
func TestReadinessSemanticCanaryUnavailable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS media_assets (id TEXT, workspace_id TEXT, search_text TEXT, name TEXT, lifecycle_state TEXT, embedding_json TEXT)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	deps := readinessDeps{
		DB:  db,
		Log: zap.NewNop(),
		Root: &compositionRoot{
			SemanticSearch: &stubSemanticSearcher{},
		},
	}

	res := checkSemanticSearchReal(context.Background(), deps)
	if res.Pass {
		t.Error("expected fail when no canary asset, got pass")
	}
	if !strings.Contains(res.Err, "semantic canary unavailable") {
		t.Errorf("error should mention 'semantic canary unavailable', got: %s", res.Err)
	}
}

// TestReadinessSemanticLocalPathLeak REMOVED (2026-07-04): the underlying
// check was retired in the production file (qdrant_readiness_semantic.go)
// because search.Candidate no longer exposes a LocalPath field per
// PR-SEARCH-PORTS-SPLIT (QDRANT-004 invariant — see types_result.go
// preamble). The godlike/07 no-fake-availability invariant is enforced at
// the type level; the test was both uncompilable (LocalPath undefined on
// Candidate) and obsolete (the invariant it pinned now lives in the
// struct shape itself, not in a runtime check).
