// seed_test.go — TDD tests for the seed_test_asset CLI.
//
// Uses httptest.Server for both the PipelineGen server and the Qdrant stub.
// Each test mocks the canonical 3-step flow (POST /api/script/generate-from-clips
// + GET /api/assets/clips/{id} + POST /collections/.../points/scroll) and
// asserts the SeedResult shape + typed sentinel errors.
//
// godlike/06 SSOT: this file is the canonical test surface for the seed
// flow. Per-test follow-up PRs (Tests 3-8 + 10) re-use the SeedResult
// shape asserted here.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestDeps builds a SeedDeps with the canonical 4-second timeout for
// httptest scenarios. The default SeedConfig.Timeout is 2 minutes; tests
// override it per scenario to keep the suite fast.
func newTestDeps(serverURL, qdrantURL, collection, token string) SeedDeps {
	return SeedDeps{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Config: SeedConfig{
			URL:        serverURL,
			QdrantURL:  qdrantURL,
			Collection: collection,
			AdminToken: token,
			Timeout:    2 * time.Second,
			PollEvery:  50 * time.Millisecond,
			AssetName:  "test-asset",
		},
	}
}

func TestRun_Success(t *testing.T) {
	var seedCalls, statusCalls, scrollCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/script/generate-from-clips":
			atomic.AddInt32(&seedCalls, 1)
			_ = json.NewEncoder(w).Encode(SeedResponse{AssetID: "ast_001", JobID: "job_001"})
		case "/api/assets/clips/ast_001":
			atomic.AddInt32(&statusCalls, 1)
			_ = json.NewEncoder(w).Encode(AssetStatus{ID: "ast_001", IndexState: "INDEXED", LifecycleState: "ACTIVE"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&scrollCalls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"points": []any{map[string]any{"id": "p1"}}},
		})
	}))
	defer qdrant.Close()

	deps := newTestDeps(server.URL, qdrant.URL, "media_assets_current", "tok")
	result, err := Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.AssetID != "ast_001" {
		t.Errorf("AssetID: got %q, want ast_001", result.AssetID)
	}
	if result.JobID != "job_001" {
		t.Errorf("JobID: got %q, want job_001", result.JobID)
	}
	if result.Status != "INDEXED" {
		t.Errorf("Status: got %q, want INDEXED", result.Status)
	}
	if result.AggregateID == "" {
		t.Errorf("AggregateID is empty")
	}
	if atomic.LoadInt32(&seedCalls) != 1 {
		t.Errorf("seed endpoint called %d times, want 1", seedCalls)
	}
	if atomic.LoadInt32(&statusCalls) == 0 {
		t.Errorf("status endpoint never called")
	}
	if atomic.LoadInt32(&scrollCalls) != 1 {
		t.Errorf("scroll endpoint called %d times, want 1", scrollCalls)
	}
}

func TestRun_EmptyServerURL(t *testing.T) {
	deps := SeedDeps{Config: SeedConfig{}}
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedStackDown) {
		t.Errorf("got %v, want ErrSeedStackDown", err)
	}
}

func TestRun_EmptyAdminToken(t *testing.T) {
	deps := SeedDeps{Config: SeedConfig{URL: "http://x"}}
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedHTTPFailed) {
		t.Errorf("got %v, want ErrSeedHTTPFailed", err)
	}
}

func TestRun_ServerDown(t *testing.T) {
	// Use a guaranteed-unreachable port for the stack-down test.
	deps := newTestDeps("http://127.0.0.1:1", "http://127.0.0.1:1", "c", "tok")
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedStackDown) {
		t.Errorf("got %v, want ErrSeedStackDown", err)
	}
}

func TestRun_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()
	deps := newTestDeps(server.URL, server.URL, "c", "tok")
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedHTTPFailed) {
		t.Errorf("got %v, want ErrSeedHTTPFailed", err)
	}
}

func TestRun_IndexTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/script/generate-from-clips":
			_ = json.NewEncoder(w).Encode(SeedResponse{AssetID: "ast_002", JobID: "job_002"})
		case "/api/assets/clips/ast_002":
			_ = json.NewEncoder(w).Encode(AssetStatus{ID: "ast_002", IndexState: "PENDING", LifecycleState: "PROCESSING"})
		}
	}))
	defer server.Close()
	deps := newTestDeps(server.URL, server.URL, "c", "tok")
	deps.Config.Timeout = 100 * time.Millisecond
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedIndexTimeout) {
		t.Errorf("got %v, want ErrSeedIndexTimeout", err)
	}
}

func TestRun_QdrantNotSynced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/script/generate-from-clips":
			_ = json.NewEncoder(w).Encode(SeedResponse{AssetID: "ast_003", JobID: "job_003"})
		case "/api/assets/clips/ast_003":
			_ = json.NewEncoder(w).Encode(AssetStatus{ID: "ast_003", IndexState: "INDEXED", LifecycleState: "ACTIVE"})
		}
	}))
	defer server.Close()
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": []any{}}})
	}))
	defer qdrant.Close()
	deps := newTestDeps(server.URL, qdrant.URL, "c", "tok")
	_, err := Run(context.Background(), deps)
	if !errors.Is(err, ErrSeedQdrantNotSynced) {
		t.Errorf("got %v, want ErrSeedQdrantNotSynced", err)
	}
}

func TestRun_VOAssetIDPropagation(t *testing.T) {
	var seedCalls int32
	var capturedVO string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/script/generate-from-clips":
			atomic.AddInt32(&seedCalls, 1)
			// Capture the vo_asset_id from the request.
			var req SeedRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedVO = req.Clips[0].VOAssetID
			_ = json.NewEncoder(w).Encode(SeedResponse{AssetID: "ast_004", JobID: "job_004", VOAssetID: req.VOAssetID})
		case "/api/assets/clips/ast_004":
			_ = json.NewEncoder(w).Encode(AssetStatus{ID: "ast_004", IndexState: "INDEXED", LifecycleState: "ACTIVE"})
		}
	}))
	defer server.Close()
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": []any{map[string]any{"id": "p4"}}}})
	}))
	defer qdrant.Close()
	deps := newTestDeps(server.URL, qdrant.URL, "c", "tok")
	deps.Config.VOAssetID = "vo_007"
	result, err := Run(context.Background(), deps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.VOAssetID != "vo_007" {
		t.Errorf("VOAssetID in result: got %q, want vo_007", result.VOAssetID)
	}
	if capturedVO != "vo_007" {
		t.Errorf("vo_asset_id propagated to clip: got %q, want vo_007", capturedVO)
	}
	if atomic.LoadInt32(&seedCalls) != 1 {
		t.Errorf("seed endpoint called %d times, want 1", seedCalls)
	}
}

// TestBuildAggregateID_ExplicitValue covers godlike/06 SSOT: when the CLI flag
// --aggregate-id is provided, the value is used verbatim. This is the
// canonical path for Test 8 (supersede-gate-2-source-versions): the
// preflight binary invokes the seed CLI twice with the same --aggregate-id
// but different --source-version values to create 2 source versions per
// the same logical asset.
func TestBuildAggregateID_ExplicitValue(t *testing.T) {
	cfg := SeedConfig{AggregateID: "agg_explicit_xyz", AssetName: "test-asset"}
	got := buildAggregateID(cfg)
	if got != "agg_explicit_xyz" {
		t.Errorf("buildAggregateID with explicit value: got %q, want %q", got, "agg_explicit_xyz")
	}
}

// TestBuildAggregateID_EmptyValueFallback covers godlike/07 NO-FAKE-AVAILABILITY:
// when the CLI flag is not provided, the helper falls back to the
// deterministic agg_<asset_name>_<unix_nano> format (NOT a fake empty value).
func TestBuildAggregateID_EmptyValueFallback(t *testing.T) {
	cfg := SeedConfig{AggregateID: "", AssetName: "test-asset"}
	got := buildAggregateID(cfg)
	if got == "" {
		t.Errorf("buildAggregateID with empty value: got empty string, want deterministic prefix")
	}
	if !strings.HasPrefix(got, "agg_test-asset_") {
		t.Errorf("buildAggregateID with empty value: got %q, want prefix agg_test-asset_", got)
	}
}

// TestBuildSourceVersion_ExplicitValue covers the Test 8 second-call path:
// the preflight binary passes --source-version=2 for the second seed
// invocation; the helper must return 2 verbatim.
func TestBuildSourceVersion_ExplicitValue(t *testing.T) {
	cfg := SeedConfig{SourceVersion: 2}
	if got := buildSourceVersion(cfg); got != 2 {
		t.Errorf("buildSourceVersion=2: got %d, want 2", got)
	}
}

// TestBuildSourceVersion_ZeroValueDefault covers godlike/07 explicit-zero-value:
// when the CLI flag is not provided (zero int value), the helper defaults
// to 1 (the canonical initial source_version for PipelineGen assets).
func TestBuildSourceVersion_ZeroValueDefault(t *testing.T) {
	cfg := SeedConfig{SourceVersion: 0}
	if got := buildSourceVersion(cfg); got != 1 {
		t.Errorf("buildSourceVersion=0 (default): got %d, want 1", got)
	}
}
