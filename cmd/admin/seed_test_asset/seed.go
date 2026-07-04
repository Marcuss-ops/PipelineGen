// seed.go — core logic for the seed_test_asset CLI.
//
// Implements the canonical seed flow that unblocks Qdrant preflight
// Tests 3-8 + 10:
//   1. POST /api/script/generate-from-clips to create the asset (outbox
//      event emitted: asset.index.requested).
//   2. Poll GET /api/assets/clips/{asset_id} until index_state=INDEXED
//      AND lifecycle_state=ACTIVE.
//   3. Verify the asset is reachable in Qdrant via /points/scroll.
//   4. Emit the seed-result.json to stdout for the preflight binary
//      to consume (SeedAssetID / SeedJobID / SeedVOAssetID).
//
// godlike/06 SSOT: this file is the canonical owner of the seed flow.
// Per-test follow-up PRs (Tests 3-8 + 10) wire the preflightDeps fields
// from the SeedResult output; godlike/06 3-surface lockstep with
// architecture/current.yaml#PR-QDRANT-PREFLIGHT-DATA-SEED + CHANGELOG.
//
// godlike/07 NO-FAKE-AVAILABILITY: each step uses real HTTP calls; failures
// are returned via typed sentinel errors (ErrSeed*) so the CLI exits
// non-zero and surfaces the error verbatim on stderr.
//
// godlike/07 typed-error contract: 4 sentinels for stack-down / http-failed
// / index-timeout / qdrant-not-synced. errors.Is traversal enabled.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Typed sentinel errors (godlike/07 NO-FAKE-AVAILABILITY).
var (
	ErrSeedStackDown       = errors.New("seed: pipelinegen server or qdrant unreachable")
	ErrSeedHTTPFailed      = errors.New("seed: POST /api/script/generate-from-clips rejected")
	ErrSeedIndexTimeout    = errors.New("seed: timeout waiting for index_state=INDEXED")
	ErrSeedQdrantNotSynced = errors.New("seed: asset indexed in DB but missing from Qdrant scroll")
)

// SeedConfig carries the resolved CLI flags.
type SeedConfig struct {
	URL           string        // PipelineGen server URL (e.g. http://127.0.0.1:8081)
	QdrantURL     string        // Qdrant base URL (e.g. http://127.0.0.1:16333)
	Collection    string        // Qdrant canonical collection alias (e.g. media_assets_current)
	AdminToken    string        // VELOX_ADMIN_TOKEN value
	Timeout       time.Duration // max wait for index_state=INDEXED
	PollEvery     time.Duration // poll interval (default 2s)
	AssetName     string        // human-readable identifier
	VOAssetID     string        // optional vo-asset-id for Test 11 piggyback
	AggregateID   string        // optional override for the deterministic aggregate_id (Test 8 supersede-gate)
	SourceVersion int           // source version (Test 8 supersede-gate; defaults to 1, second call passes 2)
}

// SeedDeps is the testable surface — production wires real *http.Client;
// tests wire httptest.Server-backed clients.
type SeedDeps struct {
	HTTPClient *http.Client
	Config     SeedConfig
}

// buildAggregateID returns the configured override if set, else the deterministic
// fallback (agg_<asset_name>_<unix_nano>). The override path is required for
// Test 8 (supersede-gate-2-source-versions): the preflight binary calls the seed
// CLI twice with the same --aggregate-id but --source-version=1 + =2.
func buildAggregateID(cfg SeedConfig) string {
	if cfg.AggregateID != "" {
		return cfg.AggregateID
	}
	return fmt.Sprintf("agg_%s_%d", cfg.AssetName, time.Now().UnixNano())
}

// buildSourceVersion returns the configured source version (default 1).
// Test 8 calls the seed CLI twice: first with --source-version=1, then with
// --source-version=2 (same --aggregate-id both times) so the supersede gate
// can verify exactly 2 source_versions per aggregate_id.
//
// godlike/07 explicit-zero-value: the `== 0` check is the canonical
// "unset" sentinel for int. SourceVersion=0 is not a valid semver for
// PipelineGen assets (we start at 1) so defaulting 0 to 1 is safe and
// matches the CLI flag's default value at main.go declaration site.
func buildSourceVersion(cfg SeedConfig) int {
	if cfg.SourceVersion == 0 {
		return 1
	}
	return cfg.SourceVersion
}

// Run executes the canonical seed flow. Returns the SeedResult on success
// or a typed sentinel error on failure. The CLI prints the SeedResult as
// JSON to stdout; this function returns the typed value.
func Run(ctx context.Context, deps SeedDeps) (*SeedResult, error) {
	if deps.Config.URL == "" {
		return nil, fmt.Errorf("%w: empty server URL", ErrSeedStackDown)
	}
	if deps.Config.AdminToken == "" {
		return nil, fmt.Errorf("%w: empty admin token", ErrSeedHTTPFailed)
	}

	// Step 1: POST /api/script/generate-from-clips to create the asset.
	seedReq := SeedRequest{
		ProjectName:   deps.Config.AssetName,
		AggregateID:   buildAggregateID(deps.Config),
		IsSandbox:     true,
		SourceVersion: buildSourceVersion(deps.Config),
		VOAssetID:     deps.Config.VOAssetID,
		Clips: []SeedClip{
			{
				SourceURL:     "https://dummy-test-clip.com/video.mp4",
				Transcription: "The quick brown fox jumps over the semantic search dog.",
				VOAssetID:     deps.Config.VOAssetID,
			},
		},
	}
	seedResp, err := postSeedRequest(ctx, deps, seedReq)
	if err != nil {
		return nil, err
	}

	// Step 2: poll GET /api/assets/clips/{asset_id} until index_state=INDEXED.
	if err := waitForIndex(ctx, deps, seedResp.AssetID); err != nil {
		return nil, err
	}

	// Step 3: verify the asset is reachable in Qdrant via /points/scroll.
	if err := verifyQdrantScroll(ctx, deps, seedResp.AssetID); err != nil {
		return nil, err
	}

	// Step 4: emit the SeedResult.
	return &SeedResult{
		AssetID:     seedResp.AssetID,
		JobID:       seedResp.JobID,
		VOAssetID:   seedResp.VOAssetID,
		Status:      "INDEXED",
		AggregateID: seedReq.AggregateID,
	}, nil
}

// postSeedRequest sends the seed request and returns the parsed response.
func postSeedRequest(ctx context.Context, deps SeedDeps, req SeedRequest) (*SeedResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrSeedHTTPFailed, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		deps.Config.URL+"/api/script/generate-from-clips", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: new request: %w", ErrSeedStackDown, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+deps.Config.AdminToken)
	resp, err := deps.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: POST /api/script/generate-from-clips: %v", ErrSeedStackDown, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: status %d: %s", ErrSeedHTTPFailed, resp.StatusCode, string(respBody))
	}
	var seedResp SeedResponse
	if err := json.NewDecoder(resp.Body).Decode(&seedResp); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrSeedHTTPFailed, err)
	}
	if seedResp.AssetID == "" {
		return nil, fmt.Errorf("%w: empty asset_id in response", ErrSeedHTTPFailed)
	}
	return &seedResp, nil
}

// waitForIndex polls the server until the asset reaches index_state=INDEXED
// AND lifecycle_state=ACTIVE OR the timeout fires.
// Returns ErrSeedIndexTimeout on timeout.
func waitForIndex(ctx context.Context, deps SeedDeps, assetID string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, deps.Config.Timeout)
	defer cancel()
	pollEvery := deps.Config.PollEvery
	if pollEvery == 0 {
		pollEvery = 2 * time.Second
	}
	for {
		// godlike/07 typed-error contract: if the parent context is cancelled
		// (timeout OR signal), translate to ErrSeedIndexTimeout rather than
		// surfacing the underlying connection error from getAssetStatus.
		if err := timeoutCtx.Err(); err != nil {
			return fmt.Errorf("%w: asset %s after %v (ctx: %v)",
				ErrSeedIndexTimeout, assetID, deps.Config.Timeout, err)
		}
		status, err := getAssetStatus(timeoutCtx, deps, assetID)
		if err != nil {
			// If the error is a context-cancellation (timeout or signal),
			// return ErrSeedIndexTimeout so the typed-error contract holds.
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return fmt.Errorf("%w: asset %s after %v: %v",
					ErrSeedIndexTimeout, assetID, deps.Config.Timeout, err)
			}
			return err
		}
		if status.IndexState == "INDEXED" && status.LifecycleState == "ACTIVE" {
			return nil
		}
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("%w: asset %s reached index_state=%s lifecycle_state=%s after %v",
				ErrSeedIndexTimeout, assetID, status.IndexState, status.LifecycleState, deps.Config.Timeout)
		case <-time.After(pollEvery):
		}
	}
}

// getAssetStatus fetches the current asset status.
func getAssetStatus(ctx context.Context, deps SeedDeps, assetID string) (*AssetStatus, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		deps.Config.URL+"/api/assets/clips/"+assetID, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: new request: %w", ErrSeedStackDown, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+deps.Config.AdminToken)
	resp, err := deps.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: GET /api/assets/clips/%s: %w", ErrSeedStackDown, assetID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: GET asset status => %d: %s", ErrSeedStackDown, resp.StatusCode, string(respBody))
	}
	var status AssetStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("%w: decode asset status: %w", ErrSeedStackDown, err)
	}
	return &status, nil
}

// verifyQdrantScroll confirms the asset is reachable in Qdrant via /points/scroll.
// Returns ErrSeedQdrantNotSynced if the asset is not present in the collection.
func verifyQdrantScroll(ctx context.Context, deps SeedDeps, assetID string) error {
	scrollReq := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "asset_id", "match": map[string]any{"value": assetID}},
			},
		},
		"limit": 1,
	}
	body, err := json.Marshal(scrollReq)
	if err != nil {
		return fmt.Errorf("%w: marshal: %w", ErrSeedQdrantNotSynced, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		deps.Config.QdrantURL+"/collections/"+deps.Config.Collection+"/points/scroll",
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: new request: %w", ErrSeedQdrantNotSynced, err)
	}
	resp, err := deps.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: POST scroll: %w", ErrSeedQdrantNotSynced, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: scroll status %d: %s", ErrSeedQdrantNotSynced, resp.StatusCode, string(respBody))
	}
	var scrollResp struct {
		Result struct {
			Points []map[string]any `json:"points"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&scrollResp); err != nil {
		return fmt.Errorf("%w: decode scroll: %w", ErrSeedQdrantNotSynced, err)
	}
	if len(scrollResp.Result.Points) == 0 {
		return fmt.Errorf("%w: no points for asset_id=%s", ErrSeedQdrantNotSynced, assetID)
	}
	return nil
}
