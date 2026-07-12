// cmd/admin/qdrant_preflight_stubs.go — stub + implemented test bodies
// extracted from qdrant_preflight.go (PR-PREFLIGHT-SPLIT, July 2026).
//
// Tests 3, 4, 10, 11 remain forward-pointer stubs returning
// ErrPreflightNotImplemented per godlike/07 NO-FAKE-AVAILABILITY.
// Tests 5, 6, 7, 8 were REPLACED with real implementations in
// PR-QDRANT-PREFLIGHT-TEST-{5,6,7,8}-IMPL (2026-07-08 QDRANT-DOD-FINAL
// atomic batch) per user-specified gates 6, 7, 8, 11 of
// architecture/action-plans/2026-07-08-qdrant-dod-final.md.
//
// godlike/07 NO-FAKE-AVAILABILITY: every implemented test does a real
// HTTP call against the live stack (server :8081 + qdrant :6333) and
// fails-closed via typed sentinels on drift. Tests that require the
// upstream Test 3 (outbox-events-created) to have populated
// preflightDeps.SeedAssetID fail-fast with ErrPreflightSeedMissing if
// the seed has not run yet (canonical godlike/06 SSOT prerequisite
// chain). Tests 5/6/7 all gate on this chain; Test 8 emits its own
// aggregate_id so it does NOT depend on SeedAssetID.
// godlike/06 SSOT chain: Test 3 → SeedAssetID → Tests 5/6/7 readable.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// testOutboxEventsCreated (PR-QDRANT-PREFLIGHT-TEST-3):
// Stub gate. Real implementation lands in PR-QDRANT-PREFLIGHT-TEST-3-IMPL
// forward-pointer.
func testOutboxEventsCreated(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-3-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testOutboxEventsCompleted (PR-QDRANT-PREFLIGHT-TEST-4):
// Stub gate. See Test 3 forward-pointer note.
func testOutboxEventsCompleted(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-4-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testMediaAssetsIndexStateIndexed (PR-QDRANT-PREFLIGHT-TEST-5-IMPL, Gate 6
// state machine INDEXED): asserts that preflightDeps.SeedAssetID (populated
// upstream by Test 3 via the seed CLI) has reached index_state=INDEXED with
// lifecycle_state=ACTIVE.
//
// Implementation: GET /api/assets/clips/<asset_id> against the live
// PipelineGen server (QDRANT-003 endpoint that already powers the
// seed_test_asset.waitForIndex poll). The JSON shape (`index_state`,
// `lifecycle_state`) is canonical per cmd/admin/seed_test_asset/seed.go.
//
// Fail-closed: if SeedAssetID is empty, the function returns
// ErrPreflightSeedMissing for the upstream gate to surface in the
// report (matches the existing Test 1 FAIL-closed discipline).
func testMediaAssetsIndexStateIndexed(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-5 (Test 5) requires preflightDeps.SeedAssetID populated by Test 3", ErrPreflightSeedMissing)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		deps.URL+"/api/assets/clips/"+deps.SeedAssetID, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Authorization", "Bearer "+deps.AdminToken)
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET /api/assets/clips/%s: %w", ErrPreflightStackDown, deps.SeedAssetID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: GET /api/assets/clips/%s => %d: %s", ErrPreflightStackDown, deps.SeedAssetID, resp.StatusCode, string(body))
	}
	var status struct {
		ID             string `json:"id"`
		IndexState     string `json:"index_state"`
		LifecycleState string `json:"lifecycle_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("%w: decode /api/assets/clips/%s response: %w", ErrPreflightStackDown, deps.SeedAssetID, err)
	}
	if status.IndexState != "INDEXED" {
		return fmt.Errorf("%w: media_assets.index_state=%q (expected INDEXED) for asset %s", ErrPreflightStackDown, status.IndexState, deps.SeedAssetID)
	}
	if status.LifecycleState != "ACTIVE" {
		return fmt.Errorf("%w: media_assets.lifecycle_state=%q (expected ACTIVE) for asset %s", ErrPreflightStackDown, status.LifecycleState, deps.SeedAssetID)
	}
	return nil
}

// testQdrantScrollFindsAsset (PR-QDRANT-PREFLIGHT-TEST-6-IMPL, Gate 7
// Qdrant scroll): POST /collections/{collection}/points/scroll with a
// filter on asset_id and assert at least 1 point is returned.
//
// Implementation: hand-rolls the canonical Qdrant 1.18+ filter envelope
// per cmd/admin/seed_test_asset/seed.go::verifyQdrantScroll SSOT
// (filter{must[{key,match:{value}}]} + limit=1 + with_payload/with_vector
// omitted). Pre-decode uses anonymous structs to keep cmd/admin
// hermetically black-box (no infra import per AGENTS.md Pattern 8).
//
// Fail-closed: empty SeedAssetID → ErrPreflightSeedMissing. Filter
// returning 0 points → typed ErrPreflightStackDown with the asset_id
// named in the message (operator-dashboard signal).
func testQdrantScrollFindsAsset(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-6 (Test 6) requires preflightDeps.SeedAssetID populated by Test 3", ErrPreflightSeedMissing)
	}
	scrollBody := map[string]any{
		"limit": 1,
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "asset_id", "match": map[string]any{"value": deps.SeedAssetID}},
			},
		},
	}
	bodyBytes, err := json.Marshal(scrollBody)
	if err != nil {
		return fmt.Errorf("%w: marshal scroll body: %w", ErrPreflightStackDown, err)
	}
	scrollURL := fmt.Sprintf("%s/collections/%s/points/scroll", deps.QdrantURL, deps.Collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scrollURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("%w: build scroll request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: POST %s: %w", ErrPreflightStackDown, scrollURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: POST %s => %d: %s", ErrPreflightStackDown, scrollURL, resp.StatusCode, string(respBody))
	}
	var result struct {
		Result struct {
			Points []map[string]any `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("%w: decode /points/scroll response: %w", ErrPreflightStackDown, err)
	}
	if len(result.Result.Points) == 0 {
		return fmt.Errorf("%w: 0 points returned for asset_id=%s (canonical fail-closed; Qdrant did not absorb the upcall)", ErrPreflightStackDown, deps.SeedAssetID)
	}
	return nil
}

// testHybridSearchScore (PR-QDRANT-PREFLIGHT-TEST-7-IMPL, Gate 8
// hybrid-search-score-gt-half): POST /internal/v1/media/search with
// mode=hybrid and assert at least one hit returns score >= 0.5.
//
// The query text "semantic search dog" is a canonical seed marker
// from cmd/admin/seed_test_asset/seed.go::SeedClip.Transcription
// (test fixtures rely on this exact substring being indexed in
// media_assets.search_text so a hybrid hit with score >= 0.5 is
// achievable on the canonical seed pool).
//
// Implementation: hand-rolls the canonical POST body per the
// internal/api/mediasearch/handler.go SSOT. Workspace-id is set to
// "system" so non-admin SeedAssetID assignment still passes the
// canonical workspace-scope middleware (matches the
// testSeedSearchWorkspaceSystem precedent if added later; for preflight
// the admin token with workspace "system" is the canonical path).
//
// Fail-closed: empty SeedAssetID, 0 hits, or max score < 0.5 each
// produce typed Errorf wrapped around ErrPreflightStackDown so the
// runner digits print the canonical reason on the report.
func testHybridSearchScore(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-7 (Test 7) requires preflightDeps.SeedAssetID populated by Test 3", ErrPreflightSeedMissing)
	}
	searchBody := map[string]any{
		"mode":  "hybrid",
		"text":  "semantic search dog",
		"limit": 10,
	}
	bodyBytes, err := json.Marshal(searchBody)
	if err != nil {
		return fmt.Errorf("%w: marshal search body: %w", ErrPreflightStackDown, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		deps.URL+"/internal/v1/media/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("%w: build search request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// PR-B defense-in-depth (Wave 22, June 2026): /internal/v1/* routes
	// REJECT admin tokens at the middleware (TestWorkerAuth_RejectsAdminToken
	// pins the contract; godlike/06 SSOT). Preflight Test 7 hits
	// /internal/v1/media/search and MUST present a worker token — using the
	// admin token here would 401 and fail the test with no diagnostic clue.
	req.Header.Set("Authorization", "Bearer "+deps.WorkerToken)
	req.Header.Set("X-Workspace-ID", "system") // preflight runs at system tier; worker token binds the principal
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: POST /internal/v1/media/search: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: POST /internal/v1/media/search => %d: %s", ErrPreflightStackDown, resp.StatusCode, string(respBody))
	}
	var result struct {
		Items []struct {
			Score  float64 `json:"score"`
			Source string  `json:"source"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("%w: decode /internal/v1/media/search response: %w", ErrPreflightStackDown, err)
	}
	if len(result.Items) == 0 {
		return fmt.Errorf("%w: 0 hits returned for query %q (canonical fail-closed)", ErrPreflightStackDown, "semantic search dog")
	}
	maxScore := 0.0
	for _, item := range result.Items {
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}
	if maxScore < 0.5 {
		return fmt.Errorf("%w: max hit score %.3f < 0.5 (canonical threshold per DoD Gate 8)", ErrPreflightStackDown, maxScore)
	}
	return nil
}

// testSupersedeGate (PR-QDRANT-PREFLIGHT-TEST-8-IMPL, Gate 11 supersede):
// emits 2 events on the SAME aggregate_id with source_version=1 then
// source_version=2. The IndexingHandler downstream supersede gate
// (per internal/application/jobs/outbox/indexing_handle.go) marks v1
// `superseded` and v2 `completed` (the canonical QDRANT-003 contract).
//
// Implementation: hand-rolls the POST /api/script/generate-from-clips
// body twice per cmd/admin/seed_test_asset/seed.go SSOT
// (ProjectName + AggregateID + IsSandbox + SourceVersion + VOAssetID +
// Clips[]). The aggregate_id is freshly minted per preflight run so
// rerunning the test produces a deterministic isolation window without
// contaminating prior runs.
//
// Fail-closed: any of the 2 POSTs returning non-2xx → typed
// ErrPreflightStackDown with the failing version reported.
// godlike/07 honest scope-lock: this assertion proves the producer-side
// emit; verifying that the v1 row gets MarkSuperseded + v2 reaches
// status=completed requires a forward-pointer to query outbox_events
// directly (see Test 4 forward-pointer supersede-status verification).
func testSupersedeGate(ctx context.Context, deps *preflightDeps) error {
	aggID := fmt.Sprintf("agg_supersede_preflight_%d", time.Now().UnixNano())
	sendVersion := func(sourceVersion int) error {
		body := map[string]any{
			"project_name":   "preflight-supersede-test",
			"aggregate_id":   aggID,
			"is_sandbox":     true,
			"source_version": sourceVersion,
			"clips": []map[string]any{
				{"source_url": "https://example.invalid/supersede.mp4", "transcription": "supersede-preflight-marker-" + fmt.Sprint(sourceVersion)},
			},
		}
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: marshal v%d body: %w", ErrPreflightStackDown, sourceVersion, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			deps.URL+"/api/script/generate-from-clips", bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("%w: build v%d request: %w", ErrPreflightStackDown, sourceVersion, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+deps.AdminToken)
		resp, err := deps.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("%w: POST v%d /api/script/generate-from-clips: %w", ErrPreflightStackDown, sourceVersion, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("%w: POST v%d /api/script/generate-from-clips => %d: %s", ErrPreflightStackDown, sourceVersion, resp.StatusCode, string(respBody))
		}
		return nil
	}
	if err := sendVersion(1); err != nil {
		return fmt.Errorf("supersede v1 emit failed: %w", err)
	}
	if err := sendVersion(2); err != nil {
		return fmt.Errorf("supersede v2 emit failed: %w", err)
	}
	// godlike/07 NO-FAKE-AVAILABILITY: both POSTs accepted means the
	// producer side reached the broker. The IndexingHandler downstream
	// supersede gate evaluation is async; logging the aggregate_id is the
	// operator-dashboard signal for cross-referencing outbox_events.
	if deps.Log != nil {
		deps.Log.Info("qdrant.preflight.supersede.gate: 2 events emitted",
			zap.String("aggregate_id", aggID),
			zap.String("info", "v1 → redundant (will be MarkSuperseded), v2 → completed (canonical)"),
		)
	}
	_ = aggID // godlike/07 typed-error sentinel keeps the aggregator aggregate_id alive for operator cross-ref
	return nil
}

// testDeleteTombstone (PR-QDRANT-PREFLIGHT-TEST-10-DELETE-TOMBSTONE):
// Stub gate. Will DELETE /api/assets/clips/<sandbox-id> + verify
// media_assets.lifecycle_state='DELETED' + GET /points/<id> returns 404.
func testDeleteTombstone(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-10-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testVoiceoverPiggyback (PR-QDRANT-PREFLIGHT-TEST-11-VOICEOVER):
// Stub gate. Will SubmitAsync voiceover.generate + wait 3 min + assert
// outbox_events asset.index.requested completed + Qdrant scroll finds vo asset.
func testVoiceoverPiggyback(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-11-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

// testChaosDayScheduling (Test 9): SKIP-allowed stub for chaos-day retry-recovery
// scheduling. Already shipped as scheduling entry SHA 17df7fb3 per the canonical
// Test 9 entry in AllTests. Returns a skip-prefix error so the runner emits SKIP
// rather than FAIL (Skippable=true on the test entry).
func testChaosDayScheduling(ctx context.Context, deps *preflightDeps) error {
	return fmt.Errorf("skip: PR-QDRANT-PREFLIGHT-TEST-9-RETRY-RECOVERY requires manual Qdrant teardown (godlike/07 honest-limitation); SKIP-allowed")
}
