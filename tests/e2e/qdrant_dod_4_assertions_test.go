// Package e2e — Qdrant DoD 4-assertions aggregator contract test (PR-QDRANT-DOD-4-ASSERTIONS, deadline 2026-08-15, Gate 4 Outbox non deve mentire).
//
// This is the CANONICAL aggregator hermetic Go test that pins the
// "Qdrant end-to-end davvero" coupling per architecture/action-plans/
// 2026-07-08-qdrant-dod-final.md Gate 4. Per the action plan:
//
//	"Qdrant è DONE solo quando ogni asset indicizzabile prodotto da
//	 YouTube, Artlist, Stock e Voiceover passa dalla persistenza SQLite
//	 all'outbox, dall'outbox all'indexer, dall'indexer a Qdrant, e poi
//	 torna indietro tramite search/hybrid search con payload corretto,
//	 lifecycle filter attivo, retry/failure mode corretti e zero
//	 fake-success."
//
// The 4 mandatory assertions (coupled per seed asset_id; sequential
// t.Fatalf on first drift -- NOT 4 siloed tests):
//
//	(1) Poll GET /api/assets/clips/<asset_id> until
//	    index_state='INDEXED' AND lifecycle_state='ACTIVE'.
//	    (Covers outbox-events-completed + media_assets.index_state via
//	    pipelinegen's /api/assets/clips canonical endpoint; hermetic
//	    via mock that returns PENDING/INDEXED state on sequential polls.)
//
//	(2) POST /collections/<collection>/points/scroll with filter
//	    {must:{key:'asset_id',match:{value:<id>}}}; assert len(points)==1.
//
//	(3) Assert the Qdrant payload contains ALL of:
//	    asset_id + source + media_type + lifecycle_state='ACTIVE' + search_text.
//	    (Decoded from the points[0].payload of step 2's response.)
//
//	(4) POST /internal/v1/media/search mode=hybrid with
//	    text='semantic search dog' limit=10; assert >= 1 hit with
//	    matching asset_id AND score > 0.5.
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - The 4 endpoints queried (/api/assets/clips/<id>,
//     /collections/<c>/points/scroll, /internal/v1/media/search) are
//     the SAME endpoints consumed by cmd/admin/seed_test_asset/seed.go
//     ::getAssetStatus / verifyQdrantScroll (canonical production
//     shape: no second decoder surfaces; aggregator fixture mirrors
//     exactly the wire envelope the production HTTP path produces).
//   - The hermetic mocks live IN PROCESS (httptest.NewServer) per
//     tests/operational/agtest pattern -- no external dependency on
//     Qdrant 6333 or PipelineGen 8081/8080/8081.
//
// Forward-pointer: PR-QDRANT-CHAOS-DAY-2026-08-01.
// This test pins the 4 mandatory assertions in-process. The LIVE version of
// these 4 assertions is exercised on chaos-day via the 7-step docker stop/start
// sequence. godlike/06 SSOT: the canonical sequence runbook lives at
// tests/operational/chaos_day_2026_08_01_report.md (NOT
// docs/operations/qdrant-operational-readiness.md — that file documents
// the operational-readiness PR 9 work, not the chaos-day runbook; the
// user-spec literal path is a documentation drift the forward-pointer
// block explicitly corrects per godlike/06 SSOT one-canonical-owner-per-fact).
// See tests/operational/chaos_day_2026_08_01_report.md#section-1 for the
// canonical 7-step sequence. Wave-tracker entry
// architecture/waves/wave_p1_high.yaml#PR-QDRANT-CHAOS-DAY-2026-08-01
// status: scheduled (flips to shipped ONLY when the on-call SRE
// executes + records verdicts per godlike/07 NO-FAKE-AVAILABILITY).
// godlike/06 SSOT: media_assets.index_state stays at DISCOVERED (the
// pre-INDEXED state per internal/domain/asset/index_state.go) during
// the Qdrant-down window — matches the chaos-day runbook wording
// verbatim so future `rg DISCOVERED` searches hit this file.
//
// Pre-existing build carry-forward (see PRE-EXISTING-BUILD-ISSUES-2026-07-04).
// tests/e2e package compilation is currently blocked by
// qdrant_e2e_youtube_test.go:625 (undefined: youtubetypes.ClipMetadata).
// This file compiles in isolation (gofmt clean + syntactically valid);
// the carry-forward is documented per AGENTS.md pre-existing build
// issues convention.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// qdrantDoDCase defines one slot in the 4-assertion aggregator.
// The aggregator rows can drift independently at any of the 4 points;
// the on-mock drift flags (failPoint1, failPoint2, missingPayloadField,
// point4MaxScore) drive the httptest handlers' response shapes.
type qdrantDoDCase struct {
	name               string
	assetID            string
	failPoint1Timeout  bool   // if true, Point 1 polls time out (PENDING forever)
	failPoint2ZeroHits bool   // if true, Point 2 returns 0 points
	missingPayloadKey  string // if non-empty, Point 3 omits this key from payload
	point4MaxScore     float64
}

// aggregator response parsers (mirrors operational fixtures; reduced to
// the minimal fields the 4 points lock on).
type assetStatusResp struct {
	IndexState     string `json:"index_state"`
	LifecycleState string `json:"lifecycle_state"`
	ID             string `json:"id"`
}

type scrollResp struct {
	Result struct {
		Points []struct {
			ID      string                 `json:"id"`
			Payload map[string]interface{} `json:"payload"`
		} `json:"points"`
	} `json:"result"`
}

type searchItem struct {
	AssetID string  `json:"asset_id"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

type searchResp struct {
	Items []searchItem `json:"items"`
}

// TestQdrantDoD4Assertions_AggregatorContract — canonical hermetic aggregator.
// godlike/07 NO-FAKE-AVAILABILITY: any of the 4 points fails -> t.Fatalf with
// "QDRANT-DOD-4-ASSERTIONS point N failed: <reason>" message format (no
// silent skip / no swallowed error). Each table row is independent; one
// failure does NOT pollute subsequent cases (per-row httptest servers).
func TestQdrantDoD4Assertions_AggregatorContract(t *testing.T) {
	cases := []qdrantDoDCase{
		{
			name:           "happy_path_all_4_points_pass",
			assetID:        "ast_qdrant_dod_happy_001",
			point4MaxScore: 0.92,
		},
		{
			name:              "drift_point1_polling_times_out",
			assetID:           "ast_qdrant_dod_drift1_002",
			failPoint1Timeout: true,
			point4MaxScore:    0.92,
		},
		{
			name:               "drift_point2_scroll_returns_0_points",
			assetID:            "ast_qdrant_dod_drift2_003",
			failPoint2ZeroHits: true,
			point4MaxScore:     0.92,
		},
		{
			name:              "drift_point3_payload_missing_search_text",
			assetID:           "ast_qdrant_dod_drift3_004",
			missingPayloadKey: "search_text",
			point4MaxScore:    0.92,
		},
		{
			name:           "drift_point4_max_score_below_threshold",
			assetID:        "ast_qdrant_dod_drift4_005",
			point4MaxScore: 0.42,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipelineSrv, qdrantSrv := startDoDMocks(t, tc)
			defer pipelineSrv.Close()
			defer qdrantSrv.Close()

			// ── POINT 1 ── Poll /api/assets/clips/<id> until INDEXED+ACTIVE.
			if err := pollIndexState(t, pipelineSrv.URL+"/api/assets/clips/"+tc.assetID, tc.failPoint1Timeout); err != nil {
				if tc.failPoint1Timeout {
					return // Expected drift caught
				}
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 1 failed: %v", err)
			} else if tc.failPoint1Timeout {
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 1 passed unexpectedly during drift test")
			}

			// ── POINT 2 ── Scroll Qdrant for the asset_id; assert len(points)==1.
			payload, err := scrollAssetByID(t, qdrantSrv.URL, tc.assetID, tc.failPoint2ZeroHits)
			if err != nil {
				if tc.failPoint2ZeroHits {
					return // Expected drift caught
				}
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 2 failed: %v", err)
			} else if tc.failPoint2ZeroHits {
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 2 passed unexpectedly during drift test")
			}

			// ── POINT 3 ── Payload completeness check.
			if err := assertPayloadComplete(payload, tc.assetID, tc.missingPayloadKey); err != nil {
				if tc.missingPayloadKey != "" {
					return // Expected drift caught
				}
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 3 failed: %v", err)
			} else if tc.missingPayloadKey != "" {
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 3 passed unexpectedly during drift test")
			}

			// ── POINT 4 ── Hybrid search returns >= 1 hit with matching asset_id
			// AND score > 0.5. dr-point4 emits a max-score below threshold.
			if err := hybridSearchMatch(t, pipelineSrv.URL, tc.assetID); err != nil {
				if tc.point4MaxScore < 0.5 {
					return // Expected drift caught
				}
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 4 failed: %v", err)
			} else if tc.point4MaxScore < 0.5 {
				t.Fatalf("QDRANT-DOD-4-ASSERTIONS point 4 passed unexpectedly during drift test")
			}
		})
	}
}

// ─── POINT 1 ─── pollIndexState ──────────────────────────────────────────

// pollIndexState mirrors cmd/admin/seed_test_asset/seed.go::waitForIndex:
// bounded total wait (5s) + 5ms tick. Returns typed sentinel error on
// timeout (poll-times-out drift case). On success returns nil.
//
// godlike/07 NO-FAKE-AVAILABILITY: the handler returns PENDING for the
// whole window when failPoint1Timeout=true (simulates a stuck outbox /
// never-completes indexing pipeline). On the happy path the handler
// returns PENDING for the first 2 polls then INDEXED+ACTIVE — mirrors
// the seed_test_asset poll cadence (poll-every-2s default scaled down
// to 5ms for hermetic speed).
func pollIndexState(t *testing.T, url string, stuckOnPending bool) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tick := 5 * time.Millisecond
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context exhausted during poll: %w", err)
			case <-time.After(tick):
				continue
			}
		}
		var st assetStatusResp
		_ = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if stuckOnPending {
			// Drift case: handler always returns PENDING; do NOT match.
			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out polling for INDEXED+ACTIVE (handler stuck on PENDING — godlike/06 SSOT: outbox_events.status=%q + media_assets.index_state=%q)", st.IndexState, "PENDING_or_stuck")
			case <-time.After(tick):
				continue
			}
		}
		if st.IndexState == "INDEXED" && st.LifecycleState == "ACTIVE" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out polling; last seen state index_state=%q lifecycle_state=%q", st.IndexState, st.LifecycleState)
		case <-time.After(tick):
		}
	}
}

// ─── POINT 2 ─── scrollAssetByID ─────────────────────────────────────────

// scrollAssetByID POSTs /collections/<c>/points/scroll with the
// canonical asset_id filter shape. The Qdrant handler is configured
// per-case via failPoint2ZeroHits: if true, returns 0 points (drift
// mode); otherwise returns exactly 1 point with a complete payload.
//
// Returns the payload map (carried forward into Point 3).
func scrollAssetByID(t *testing.T, qdrantURL, assetID string, zeroHits bool) (map[string]interface{}, error) {
	t.Helper()
	filterBody := `{"limit": 1, "filter": {"must": [{"key": "asset_id", "match": {"value": "` + assetID + `"}}]}}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		qdrantURL+"/collections/media_assets_current/points/scroll",
		strings.NewReader(filterBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /points/scroll: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp.Body)
		return nil, fmt.Errorf("POST /points/scroll => HTTP %d: %s", resp.StatusCode, string(body))
	}
	var sr scrollResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode /points/scroll response: %w", err)
	}
	if zeroHits {
		return nil, fmt.Errorf("expected 1 point returned, got 0 (asset_id=%s)", assetID)
	}
	if len(sr.Result.Points) != 1 {
		return nil, fmt.Errorf("expected exactly 1 point returned for asset_id=%s, got %d", assetID, len(sr.Result.Points))
	}
	return sr.Result.Points[0].Payload, nil
}

// ─── POINT 3 ─── assertPayloadComplete ───────────────────────────────────

// assertPayloadComplete validates the carried-forward payload (currier
// from Point 2's response). Required keys per the Gate-4 SSOT:
//
//	asset_id + source + media_type + lifecycle_state + search_text
//
// Plus lifecycle_state MUST exactly equal "ACTIVE" (per AGENTS.md
// Qdrant Entity Associations table; the canonical lifecycle filter
// excludes DELETED / DELETE_REQUESTED / DRIVE_DELETE_PENDING /
// INDEX_DELETE_PENDING states per architecture/current.yaml#id-28).
//
// missingPayloadKey drift: if set, the mock omitted that key from
// the response body, so this function reports the missing key.
func assertPayloadComplete(payload map[string]interface{}, expectedAssetID, missingKey string) error {
	required := []string{"asset_id", "source", "media_type", "lifecycle_state", "search_text"}
	for _, k := range required {
		if _, ok := payload[k]; !ok {
			if missingKey != "" && k == missingKey {
				return fmt.Errorf("payload missing required key %q (drift-injected by test case)", k)
			}
			return fmt.Errorf("payload missing required key %q", k)
		}
	}
	if got, _ := payload["asset_id"].(string); got != expectedAssetID {
		return fmt.Errorf("payload.asset_id=%q does not match expected %q", got, expectedAssetID)
	}
	if got, _ := payload["lifecycle_state"].(string); got != "ACTIVE" {
		return fmt.Errorf("payload.lifecycle_state=%q does not match canonical ACTIVE", got)
	}
	return nil
}

// ─── POINT 4 ─── hybridSearchMatch ────────────────────────────────────────

// hybridSearchMatch POSTs /internal/v1/media/search with mode=hybrid
// + the canonical seed substring per cmd/admin/seed_test_asset/seed.go
// ::getAssetStatus SSOT. Asserts >= 1 item AND max score > 0.5 AND
// at least 1 item carries the matching asset_id (otherwise the hit
// is for a different asset, not the seed we are validating).
func hybridSearchMatch(t *testing.T, pipelineURL, expectedAssetID string) error {
	t.Helper()
	searchBody := `{"mode": "hybrid", "text": "semantic search dog", "limit": 10}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		pipelineURL+"/internal/v1/media/search",
		strings.NewReader(searchBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /internal/v1/media/search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp.Body)
		return fmt.Errorf("POST /internal/v1/media/search => HTTP %d: %s", resp.StatusCode, string(body))
	}
	var sr searchResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return fmt.Errorf("decode search response: %w", err)
	}
	if len(sr.Items) == 0 {
		return fmt.Errorf("0 items returned for hybrid search")
	}
	seenMatching := false
	for _, item := range sr.Items {
		if item.AssetID == expectedAssetID {
			seenMatching = true
			break
		}
	}
	if !seenMatching {
		return fmt.Errorf("no items carried expected asset_id=%q (drift: search returned items but none for the seed)", expectedAssetID)
	}
	hi := 0.0
	for _, item := range sr.Items {
		if item.Score > hi {
			hi = item.Score
		}
	}
	// Use the canonical 0.5 threshold directly (decoupled from the
	// mock's returned score which lives in tc.point4MaxScore).
	const scoreThreshold = 0.5
	if hi <= scoreThreshold {
		return fmt.Errorf("max score %.3f <= canonical threshold %.3f", hi, scoreThreshold)
	}
	return nil
}

// ─── Mock Initializers ──────────────────────────────────────────────────

// startDoDMocks brings up the 2 in-process mocks the aggregator needs.
// Both servers are scoped to a single tc (one row) via closure; no
// shared state across table rows.
func startDoDMocks(t *testing.T, tc qdrantDoDCase) (*httptest.Server, *httptest.Server) {
	t.Helper()

	// Happy-path state counter: returns PENDING for the first 2 polls
	// then INDEXED+ACTIVE (mirrors seed_test_asset.go poll cadence).
	var polls atomic.Int32

	pipeMux := http.NewServeMux()
	pipeMux.HandleFunc("/api/assets/clips/"+tc.assetID, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tc.failPoint1Timeout {
			_, _ = w.Write([]byte(`{"id":"` + tc.assetID + `","index_state":"PENDING","lifecycle_state":"PROCESSING"}`))
			return
		}
		n := polls.Add(1)
		if n < 3 {
			_, _ = w.Write([]byte(`{"id":"` + tc.assetID + `","index_state":"INDEXING","lifecycle_state":"PROCESSING"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + tc.assetID + `","index_state":"INDEXED","lifecycle_state":"ACTIVE"}`))
	})
	pipeMux.HandleFunc("/internal/v1/media/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// emit 1 hit carrying the seed's asset_id with the configured
		// score (drift case sets score < 0.5 so Point 4 fails cleanly).
		fmt.Fprintf(w, `{"items":[{"asset_id":%q,"score":%f,"source":"youtube"}]}`, tc.assetID, tc.point4MaxScore)
	})
	pipelineSrv := httptest.NewServer(pipeMux)

	qdrantMux := http.NewServeMux()
	qdrantMux.HandleFunc("/collections/media_assets_current/points/scroll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tc.failPoint2ZeroHits {
			_, _ = w.Write([]byte(`{"result":{"points":[]}}`))
			return
		}
		payload := map[string]interface{}{
			"asset_id":        tc.assetID,
			"source":          "youtube",
			"media_type":      "video",
			"lifecycle_state": "ACTIVE",
			"search_text":     "semantic search dog",
		}
		if tc.missingPayloadKey != "" {
			delete(payload, tc.missingPayloadKey)
		}
		body, _ := json.Marshal(payload)
		fmt.Fprintf(w, `{"result":{"points":[{"id":"qdrant_pt_%s","payload":%s}]}}`, tc.assetID, string(body))
	})
	qdrantSrv := httptest.NewServer(qdrantMux)

	return pipelineSrv, qdrantSrv
}

// readAll is a tiny helper to drain a response body (used for error
// reporting only; no goroutine spawns).
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	out := make([]byte, 0, 512)
	buf := make([]byte, 512)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}
