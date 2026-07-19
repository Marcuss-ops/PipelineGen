package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestDepsMemo(t *testing.T, serverURL, seedID string) *preflightDeps {
	t.Helper()
	return &preflightDeps{
		URL:         serverURL,
		QdrantURL:   serverURL,
		Collection:  "media_assets_v3_e5_768_siglip_768",
		AdminToken:  "test-admin-token",
		WorkerToken: "test-worker-token",
		HTTPClient:  &http.Client{Timeout: 5 * time.Second},
		Log:         zap.NewNop(),
		SeedAssetID: seedID,
	}
}

func TestMediaAssetsIndexStateIndexed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": "asset-1", "index_state": "INDEXED", "lifecycle_state": "ACTIVE",
		})
	}))
	defer server.Close()

	if err := testMediaAssetsIndexStateIndexed(context.Background(), newTestDepsMemo(t, server.URL, "asset-1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := testMediaAssetsIndexStateIndexed(context.Background(), newTestDepsMemo(t, server.URL, "")); err == nil || !strings.Contains(err.Error(), "SeedAssetID") {
		t.Fatalf("missing seed must fail clearly, got %v", err)
	}
}

func TestQdrantScrollFindsAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"points": []map[string]any{{"id": "point-1"}}},
		})
	}))
	defer server.Close()

	if err := testQdrantScrollFindsAsset(context.Background(), newTestDepsMemo(t, server.URL, "asset-1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHybridSearchScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-worker-token" {
			t.Errorf("authorization=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{"score": 0.65, "source": "youtube"}},
		})
	}))
	defer server.Close()

	if err := testHybridSearchScore(context.Background(), newTestDepsMemo(t, server.URL, "asset-1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSupersedeGateIsExplicitlyRetired(t *testing.T) {
	err := testSupersedeGate(context.Background(), newTestDepsMemo(t, "http://127.0.0.1", "asset-1"))
	if err == nil || !strings.HasPrefix(err.Error(), "skip: ") {
		t.Fatalf("retired supersede probe must surface an explicit skip, got %v", err)
	}
	if strings.Contains(err.Error(), "generate-from-clips") {
		t.Fatalf("retired endpoint name leaked into runtime error: %v", err)
	}
}
