// cmd/admin/qdrant_preflight_stubs.go contains the Qdrant preflight tests
// that are either implemented against canonical asset/search surfaces or are
// explicit forward-pointer stubs.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func testOutboxEventsCreated(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-3-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

func testOutboxEventsCompleted(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-4-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

func testMediaAssetsIndexStateIndexed(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-5 requires SeedAssetID", ErrPreflightSeedMissing)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deps.URL+"/api/assets/clips/"+deps.SeedAssetID, nil)
	if err != nil {
		return fmt.Errorf("%w: build asset request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Authorization", "Bearer "+deps.AdminToken)
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: GET asset: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: GET asset => %d: %s", ErrPreflightStackDown, resp.StatusCode, string(body))
	}
	var status struct {
		IndexState     string `json:"index_state"`
		LifecycleState string `json:"lifecycle_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("%w: decode asset response: %w", ErrPreflightStackDown, err)
	}
	if status.IndexState != "INDEXED" {
		return fmt.Errorf("%w: media_assets.index_state=%q (expected INDEXED) for asset %s", ErrPreflightStackDown, status.IndexState, deps.SeedAssetID)
	}
	if status.LifecycleState != "ACTIVE" {
		return fmt.Errorf("%w: media_assets.lifecycle_state=%q (expected ACTIVE) for asset %s", ErrPreflightStackDown, status.LifecycleState, deps.SeedAssetID)
	}
	return nil
}

func testQdrantScrollFindsAsset(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-6 requires SeedAssetID", ErrPreflightSeedMissing)
	}
	payload := map[string]any{
		"limit": 1,
		"filter": map[string]any{
			"must": []map[string]any{{"key": "asset_id", "match": map[string]any{"value": deps.SeedAssetID}}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: marshal scroll request: %w", ErrPreflightStackDown, err)
	}
	url := fmt.Sprintf("%s/collections/%s/points/scroll", deps.QdrantURL, deps.Collection)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build scroll request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: POST scroll: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: POST scroll => %d: %s", ErrPreflightStackDown, resp.StatusCode, string(responseBody))
	}
	var decoded struct {
		Result struct {
			Points []map[string]any `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("%w: decode scroll response: %w", ErrPreflightStackDown, err)
	}
	if len(decoded.Result.Points) == 0 {
		return fmt.Errorf("%w: 0 points returned for asset_id=%s", ErrPreflightStackDown, deps.SeedAssetID)
	}
	return nil
}

func testHybridSearchScore(ctx context.Context, deps *preflightDeps) error {
	if deps.SeedAssetID == "" {
		return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-7 requires SeedAssetID", ErrPreflightSeedMissing)
	}
	body, err := json.Marshal(map[string]any{"mode": "hybrid", "text": "semantic search dog", "limit": 10})
	if err != nil {
		return fmt.Errorf("%w: marshal search request: %w", ErrPreflightStackDown, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deps.URL+"/internal/v1/media/search", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build search request: %w", ErrPreflightStackDown, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+deps.WorkerToken)
	req.Header.Set("X-Workspace-ID", "system")
	resp, err := deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: POST media search: %w", ErrPreflightStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: POST media search => %d: %s", ErrPreflightStackDown, resp.StatusCode, string(responseBody))
	}
	var decoded struct {
		Items []struct {
			Score float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("%w: decode media search response: %w", ErrPreflightStackDown, err)
	}
	if len(decoded.Items) == 0 {
		return fmt.Errorf("%w: 0 hits returned for query %q", ErrPreflightStackDown, "semantic search dog")
	}
	maxScore := 0.0
	for _, item := range decoded.Items {
		if item.Score > maxScore {
			maxScore = item.Score
		}
	}
	if maxScore < 0.5 {
		return fmt.Errorf("%w: max hit score %.3f < 0.5", ErrPreflightStackDown, maxScore)
	}
	return nil
}

// The old supersede probe depended on the removed generate-from-clips DTO
// (aggregate_id/source_version plus inline clip bodies). GenerationEnvelopeV2
// intentionally has no such fields, so pretending to migrate that probe would
// test a contract that does not exist. Keep the gap explicit until supersede is
// tested through the canonical media/outbox producer.
func testSupersedeGate(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("skip: supersede probe removed with generate-from-clips; re-home it on the canonical media/outbox producer")
}

func testDeleteTombstone(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-10-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

func testVoiceoverPiggyback(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("%w: PR-QDRANT-PREFLIGHT-TEST-11-IMPL (forward-pointer)", ErrPreflightNotImplemented)
}

func testChaosDayScheduling(_ context.Context, _ *preflightDeps) error {
	return fmt.Errorf("skip: PR-QDRANT-PREFLIGHT-TEST-9 requires manual Qdrant teardown")
}
