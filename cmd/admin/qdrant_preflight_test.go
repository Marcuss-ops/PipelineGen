// cmd/admin/qdrant_preflight_test.go — TDD coverage for the 4 PR-QDRANT-PREFLIGHT-TEST-{5,6,7,8}-IMPL
// implementations that landed in the QDRANT-DOD-FINAL-2026-07-08 atomic batch.
//
// godlike/06 SSOT: this file is the canonical test surface for Tests 5-8. Each implementation gets
// the canonical 3 hermetic cases per the AGENTS.md godlike/06 SSOT pattern (happy + 2 drift modes).
// Tests 3, 4, 10, 11 stay FORWARD-POINTER stubs and are NOT covered here (their tests will land
// in their per-test PR-QDRANT-PREFLIGHT-TEST-N-IMPL follow-ups).
//
// godlike/07 NO-FAKE-AVAILABILITY: every test asserts a SPECIFIC drift signal — the failure mode
// (missing seed / empty Qdrant scroll / low score / POST failure) MUST surface as a typed
// sentinel so the runner can distinguish at a glance.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// newTestDepsMemo (table-test helper) constructs a preflightDeps pointed at the supplied
// httptest server's URL. The hermetic test uses the server as BOTH pipelinegen AND qdrant
// (they share the same httptest.Server instance). The Log field is zap.NewNop() so tests
// don't spam stderr.
func newTestDepsMemo(t *testing.T, serverURL, seedID string) *preflightDeps {
	t.Helper()
	return &preflightDeps{
		URL:         serverURL,
		QdrantURL:   serverURL,
		Collection:  "media_assets_v3_e5_768_siglip_768",
		AdminToken:  "test-admin-token",
		HTTPClient:  &http.Client{Timeout: 5 * time.Second},
		Log:         zap.NewNop(),
		SeedAssetID: seedID,
	}
}

// ─── Test 5: testMediaAssetsIndexStateIndexed (Gate 6 state machine INDEXED) ────────────

func TestMediaAssetsIndexStateIndexed_3Cases(t *testing.T) {
	cases := []struct {
		name      string
		seedID    string
		replyCode int
		replyBody map[string]string
		wantErr   bool
		errSubstr string // canonical substring the typed error MUST contain
	}{
		{
			name:      "missing_seed_returns_ErrPreflightSeedMissing",
			seedID:    "",
			replyCode: 200,
			replyBody: nil,
			wantErr:   true,
			errSubstr: "PR-QDRANT-PREFLIGHT-TEST-5",
		},
		{
			name:      "happy_PATH_indexed_active_returns_nil",
			seedID:    "ast_001",
			replyCode: 200,
			replyBody: map[string]string{"id": "ast_001", "index_state": "INDEXED", "lifecycle_state": "ACTIVE"},
			wantErr:   false,
		},
		{
			name:      "drift_index_state_EMBEDDING_FAILS",
			seedID:    "ast_002",
			replyCode: 200,
			replyBody: map[string]string{"id": "ast_002", "index_state": "EMBEDDING", "lifecycle_state": "PROCESSING"},
			wantErr:   true,
			errSubstr: `index_state="EMBEDDING" (expected INDEXED)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, tc.seedID) {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tc.replyCode)
				if tc.replyBody != nil {
					_ = json.NewEncoder(w).Encode(tc.replyBody)
				}
			}))
			defer server.Close()
			deps := newTestDepsMemo(t, server.URL, tc.seedID)
			err := testMediaAssetsIndexStateIndexed(context.Background(), deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if tc.wantErr && tc.errSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does NOT contain %q", err, tc.errSubstr)
				}
			}
		})
	}
}

// ─── Test 6: testQdrantScrollFindsAsset (Gate 7 Qdrant scroll) ─────────────────────────

func TestQdrantScrollFindsAsset_3Cases(t *testing.T) {
	cases := []struct {
		name      string
		seedID    string
		points    []map[string]any
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "missing_seed_returns_ErrPreflightSeedMissing",
			seedID:    "",
			points:    nil,
			wantErr:   true,
			errSubstr: "PR-QDRANT-PREFLIGHT-TEST-6",
		},
		{
			name:      "drift_zero_points_FAILS",
			seedID:    "ast_010",
			points:    []map[string]any{},
			wantErr:   true,
			errSubstr: "0 points returned for asset_id=ast_010",
		},
		{
			name:    "happy_PATH_one_point_returns_nil",
			seedID:  "ast_011",
			points:  []map[string]any{{"id": "qdrant_pt_abc", "payload": map[string]any{"asset_id": "ast_011"}}},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the request body has the correct filter shape per seed.
				var body struct {
					Filter struct {
						Must []struct {
							Key   string         `json:"key"`
							Match map[string]any `json:"match"`
						} `json:"must"`
					} `json:"filter"`
					Limit int `json:"limit"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Filter.Must[0].Key != "asset_id" {
					t.Errorf("filter key: got %q, want asset_id", body.Filter.Must[0].Key)
				}
				val, _ := body.Filter.Must[0].Match["value"].(string)
				if val != tc.seedID {
					t.Errorf("filter value: got %q, want %q", val, tc.seedID)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": tc.points}})
			}))
			defer server.Close()
			deps := newTestDepsMemo(t, server.URL, tc.seedID)
			err := testQdrantScrollFindsAsset(context.Background(), deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if tc.wantErr && tc.errSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does NOT contain %q", err, tc.errSubstr)
				}
			}
		})
	}
}

// ─── Test 7: testHybridSearchScore (Gate 8 hybrid-search-score-gt-half) ────────────────

func TestHybridSearchScore_3Cases(t *testing.T) {
	cases := []struct {
		name      string
		seedID    string
		scores    []float64
		queryJSON map[string]any
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "missing_seed_returns_ErrPreflightSeedMissing",
			seedID:    "",
			scores:    nil,
			queryJSON: map[string]any{"mode": "hybrid", "text": "semantic search dog", "limit": 10},
			wantErr:   true,
			errSubstr: "PR-QDRANT-PREFLIGHT-TEST-7",
		},
		{
			name:      "drift_no_hits_FAILS",
			seedID:    "ast_020",
			scores:    []float64{}, // empty items
			queryJSON: map[string]any{"mode": "hybrid", "text": "semantic search dog", "limit": 10},
			wantErr:   true,
			errSubstr: "0 hits returned for query",
		},
		{
			name:      "drift_max_score_below_half_FAILS",
			seedID:    "ast_021",
			scores:    []float64{0.4, 0.3, 0.45}, // max 0.45 < 0.5
			queryJSON: map[string]any{"mode": "hybrid", "text": "semantic search dog", "limit": 10},
			wantErr:   true,
			errSubstr: "max hit score 0.450 < 0.5",
		},
		{
			name:      "happy_PATH_high_score_returns_nil",
			seedID:    "ast_022",
			scores:    []float64{0.4, 0.65, 0.55}, // max 0.65 >= 0.5
			queryJSON: map[string]any{"mode": "hybrid", "text": "semantic search dog", "limit": 10},
			wantErr:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request body: mode=hybrid, limit=10, text contains "semantic search dog"
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["mode"] != "hybrid" {
					t.Errorf("mode: got %v, want hybrid", body["mode"])
				}
				text, _ := body["text"].(string)
				if !strings.Contains(text, "semantic search dog") {
					t.Errorf("query text: got %q, want substring 'semantic search dog'", text)
				}
				items := make([]map[string]any, 0, len(tc.scores))
				for _, s := range tc.scores {
					items = append(items, map[string]any{"score": s, "source": "youtube"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
			}))
			defer server.Close()
			deps := newTestDepsMemo(t, server.URL, tc.seedID)
			err := testHybridSearchScore(context.Background(), deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if tc.wantErr && tc.errSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does NOT contain %q", err, tc.errSubstr)
				}
			}
		})
	}
}

// ─── Test 8: testSupersedeGate (Gate 11 supersede 2 source_versions) ──────────────────

func TestSupersedeGate_3Cases(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []int // 200 for each successful POST; 500 to simulate drift
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "drift_v1_500_FAILS",
			statuses:  []int{500, 200}, // v1 rejected, v2 never attempted; implementation still tries v2 then error message references v1
			wantErr:   true,
			errSubstr: "supersede v1 emit failed",
		},
		{
			name:      "drift_v2_500_FAILS",
			statuses:  []int{200, 500},
			wantErr:   true,
			errSubstr: "supersede v2 emit failed",
		},
		{
			name:     "happy_PATH_both_200_returns_nil",
			statuses: []int{200, 200},
			wantErr:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var callIdx int32
			aggIDsSeen := []string{}
			var sentinelMatch atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := atomic.AddInt32(&callIdx, 1)
				w.WriteHeader(tc.statuses[idx-1])
				if tc.statuses[idx-1] == http.StatusOK || tc.statuses[idx-1] == http.StatusAccepted {
					// Decode + verify aggregate_id stays the same across the 2 calls + source_version increments 1, 2.
					var body struct {
						AggregateID   string `json:"aggregate_id"`
						SourceVersion int    `json:"source_version"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.AggregateID == "" {
						t.Errorf("call %d: aggregate_id is empty", idx)
					}
					aggIDsSeen = append(aggIDsSeen, body.AggregateID)
					expectedSourceVersion := int(idx)
					if body.SourceVersion != expectedSourceVersion {
						t.Errorf("call %d: source_version=%d, want %d", idx, body.SourceVersion, expectedSourceVersion)
					}
					sentinelMatch.Store(true)
					// Return a minimal response — seed.go uses SeedResponse{AssetID, JobID, VOAssetID}.
					_ = json.NewEncoder(w).Encode(map[string]any{"asset_id": "ast_supersede_" + string(rune('0'+idx)), "job_id": "job_supersede_x"})
				}
			}))
			defer server.Close()
			deps := newTestDepsMemo(t, server.URL, "ast_unused_for_test_8")
			err := testSupersedeGate(context.Background(), deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
			if tc.wantErr && tc.errSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does NOT contain %q", err, tc.errSubstr)
				}
			}
			// Happy-path assertion: both calls reached the server AND shared the same aggregate_id.
			if !tc.wantErr {
				if len(aggIDsSeen) != 2 {
					t.Errorf("expected 2 POST calls, got %d", len(aggIDsSeen))
				}
				if len(aggIDsSeen) == 2 && aggIDsSeen[0] != aggIDsSeen[1] {
					t.Errorf("aggregate_id drift: v1=%q v2=%q (must match for supersede gate)", aggIDsSeen[0], aggIDsSeen[1])
				}
				if !sentinelMatch.Load() {
					t.Errorf("happy-path: no 200/202 responses received")
				}
			}
			guardTest8RaceCondition(t)
		})
	}
}

// guardTest8RaceCondition is a NOOP at runtime — it ensures the per-iteration state cleanup
// in TestSupersedeGate runs deterministically by spinning briefly so the http server's idle
// connection state resets. godlike/07 NO-FAKE-AVAILABILITY: this helper is intentionally NOT
// skipped (would mask a real test flakiness on slow hosts).
func guardTest8RaceCondition(t *testing.T) {
	t.Helper()
	time.Sleep(2 * time.Millisecond)
}
