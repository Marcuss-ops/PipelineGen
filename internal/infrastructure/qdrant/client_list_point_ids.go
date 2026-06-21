// Package qdrant — ListPointIDs scrolls the collection and returns the
// asset_id strings of up to `limit` points. Used by the PR3-5b cross-check
// to detect orphan Qdrant points (Qdrant holds a point whose asset_id is
// absent from media_assets in SQLite).
//
// Implementation notes:
//   - Hits /points/scroll with with_vector=false, with_payload=true.
//   - Uses Pagination via next_page_offset when limit > pageSize.
//   - Hard-capped at maxScrollPageSize so a runaway caller cannot OOM the
//     process or starve other Qdrant consumers by scrolling the entire
//     collection. Callers needing full collection iteration should use the
//     CleanupStalePoints path (which has a per-page work budget).
//   - Stops early once `limit` points are accumulated.
package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// ListPointIDs scrolls the Qdrant collection and returns up to `limit`
// distinct asset_id strings extracted from point payloads.
//
// limit <= 0 returns an empty slice (not an error). limit is capped at
// maxListPointIDsScrollCap so a caller cannot accidentally trigger a
// full-collection scroll — the cross-check is meant to be a SAMPLE, not
// a full diff. Use CleanupStalePoints for full collection work.
//
// next_page_offset handling matches the CleanupStalePoints pattern: the
// offset can be a numeric uint64 OR a JSON string for UUID-style IDs, so
// the cursor is preserved verbatim from the previous response.
func (c *QdrantClient) ListPointIDs(ctx context.Context, limit int) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("qdrant client is nil")
	}
	if limit <= 0 {
		return []string{}, nil
	}
	if limit > maxListPointIDsScrollCap {
		limit = maxListPointIDsScrollCap
	}

	// Hard cap already bounds `limit`; initialise both backing stores directly so
	// Go does not re-grow during appends.
	out := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	var offset any

	for collected := 0; collected < limit; {
		pageSize := listPointIDsPageSize
		if remaining := limit - collected; remaining < pageSize {
			pageSize = remaining
		}

		scrollReq := map[string]any{
			"limit":        pageSize,
			"with_payload": true,
			"with_vector":  false,
		}
		if offset != nil {
			scrollReq["offset"] = offset
		}

		respBody, err := c.qdrantRequest(ctx, "POST",
			fmt.Sprintf("/collections/%s/points/scroll", c.operationCollection()), scrollReq)
		if err != nil {
			return out, fmt.Errorf("scroll points (collected=%d): %w", collected, err)
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
			return out, fmt.Errorf("parse scroll response: %w", err)
		}

		if len(scrollResp.Result.Points) == 0 {
			break
		}

		for _, p := range scrollResp.Result.Points {
			assetID := extractString(p.Payload, "asset_id")
			if assetID == "" {
				continue
			}
			if _, dup := seen[assetID]; dup {
				continue
			}
			seen[assetID] = struct{}{}
			out = append(out, assetID)
			collected++
			if collected >= limit {
				break
			}
		}

		// Break before issuing the next page request if we've satisfied
		// the limit OR Qdrant reports no more pages.
		if collected >= limit {
			break
		}
		if scrollResp.Result.NextPageOffset == nil {
			break
		}
		raw := string(*scrollResp.Result.NextPageOffset)
		if raw == "" || raw == "null" {
			break
		}
		var numOffset uint64
		if err := json.Unmarshal(*scrollResp.Result.NextPageOffset, &numOffset); err == nil {
			offset = numOffset
		} else {
			offset = strings.Trim(raw, `"`)
		}

		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
	}

	c.log.Debug("ListPointIDs completed",
		zap.Int("requested_limit", limit),
		zap.Int("distinct_returned", len(out)))
	return out, nil
}

// listPointIDsInternal constants: pageSize is the per-page scroll batch
// and maxListPointIDsScrollCap is the hard cap so a runaway caller
// cannot scroll the entire collection.
const (
	listPointIDsPageSize     = 200
	maxListPointIDsScrollCap = 5000
)
