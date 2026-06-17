// Package vectorstore — GhostSweeper support methods.
//
// ScrollAssetIDsPage: full-collection iterator (no hard cap like
// ListPointIDs which is sampled). Yields distinct asset_id batches to fn
// until Qdrant reports no more pages or fn returns an error.
//
// DeletePoints: batch delete filter by `should`-list on payload asset_id,
// client-side chunking at ghostSweepDeleteBatch so each Qdrant payload
// stays under the documented ~1000-item filter limit.
//
// These two methods back the daily sweepers.go::startQdrantGhostSweeper.
package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

const (
	ghostSweepScrollBatchSize = 500  // per-page scroll request to Qdrant
	ghostSweepMaxBatchSize    = 1000 // hard ceiling so a runaway caller can't OOM
	ghostSweepDeleteBatch     = 100  // must be < Qdrant's 1000 filter limit
)

// ScrollAssetIDsPage scrolls the entire collection and invokes fn per
// non-empty batch of asset_id strings extracted from point payloads.
// Iteration stops when:
//   - Qdrant returns an empty page (no more points), or
//   - Qdrant returns no next_page_offset, or
//   - fn returns a non-nil error (propagated to the caller), or
//   - ctx is cancelled.
//
// Unlike ListPointIDs there is NO hard upper bound on collection size —
// the GhostSweeper is meant to be a SAMPLE-free, ground-truth diff, not
// a quick cross-check. Memory stays bounded because fn is responsible
// for processing (typically accumulating) the batch.
//
// batchSize <= 0 falls back to ghostSweepScrollBatchSize. batchSize is
// capped at ghostSweepMaxBatchSize so a single Qdrant request can't
// starve the network round trip.
func (c *QdrantClient) ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error {
	if c == nil {
		return fmt.Errorf("qdrant client is nil")
	}
	if batchSize <= 0 {
		batchSize = ghostSweepScrollBatchSize
	}
	if batchSize > ghostSweepMaxBatchSize {
		batchSize = ghostSweepMaxBatchSize
	}

	var offset any
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		scrollReq := map[string]any{
			"limit":        batchSize,
			"with_payload": true,
			"with_vector":  false,
		}
		if offset != nil {
			scrollReq["offset"] = offset
		}

		respBody, err := c.qdrantRequest(ctx, "POST",
			fmt.Sprintf("/collections/%s/points/scroll", c.operationCollection()), scrollReq)
		if err != nil {
			return fmt.Errorf("scroll (offset=%v): %w", offset, err)
		}

		var scrollResp struct {
			Result struct {
				Points []struct {
					ID      any            `json:"id"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset *json.RawMessage `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.Unmarshal(respBody, &scrollResp); err != nil {
			return fmt.Errorf("parse scroll response: %w", err)
		}

		if len(scrollResp.Result.Points) == 0 {
			return nil
		}

		batch := make([]string, 0, len(scrollResp.Result.Points))
		for _, p := range scrollResp.Result.Points {
			assetID := extractString(p.Payload, "asset_id")
			if assetID != "" {
				batch = append(batch, assetID)
			}
		}
		if err := fn(batch); err != nil {
			return fmt.Errorf("callback (collected=%d): %w", -1, err)
		}

		if scrollResp.Result.NextPageOffset == nil {
			return nil
		}
		raw := string(*scrollResp.Result.NextPageOffset)
		if raw == "" || raw == "null" {
			return nil
		}
		var numOffset uint64
		if err := json.Unmarshal(*scrollResp.Result.NextPageOffset, &numOffset); err == nil {
			offset = numOffset
		} else {
			offset = strings.Trim(raw, `"`)
		}
	}
}

// DeletePoints batch-deletes Qdrant points whose payload.asset_id matches
// any of the given assetIDs. Internally chunks the input at
// ghostSweepDeleteBatch (Qdrant rejects filter payloads above ~1000 items).
//
// Uses POST /points/delete with a `should` filter on `payload.asset_id`.
// Should multi-match is supported by Qdrant ≥1.0 and avoids having to
// resolve Qdrant point IDs (UUIDs) from asset_id strings.
//
// Idempotent: deleting an already-missing asset_id is a no-op.
//
// Returns the first chunk error encountered (if any). Subsequent chunks
// are NOT processed on partial failure — the operator will see the next
// sweep pick up where the previous one left off.
func (c *QdrantClient) DeletePoints(ctx context.Context, assetIDs []string) error {
	if c == nil {
		return fmt.Errorf("qdrant client is nil")
	}
	if len(assetIDs) == 0 {
		return nil
	}

	for i := 0; i < len(assetIDs); i += ghostSweepDeleteBatch {
		end := i + ghostSweepDeleteBatch
		if end > len(assetIDs) {
			end = len(assetIDs)
		}
		chunk := assetIDs[i:end]

		shouldList := make([]any, 0, len(chunk))
		for _, id := range chunk {
			shouldList = append(shouldList, map[string]any{
				"key": "asset_id",
				"match": map[string]any{
					"value": id,
				},
			})
		}
		filter := map[string]any{
			"should": shouldList,
		}
		body := map[string]any{"filter": filter}

		respBody, err := c.qdrantRequest(ctx, "POST",
			fmt.Sprintf("/collections/%s/points/delete", c.operationCollection()), body)
		if err != nil {
			return fmt.Errorf("delete chunk %d-%d (size=%d): %w", i, end, len(chunk), err)
		}

		var resp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return fmt.Errorf("parse delete response (chunk %d-%d): %w", i, end, err)
		}
		c.log.Debug("Qdrant delete chunk completed",
			zap.Int("from", i), zap.Int("to", end),
			zap.Int("count", len(chunk)),
			zap.String("qdrant_status", resp.Status))
	}
	return nil
}
