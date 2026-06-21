package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Search performs ANN search using named vectors.
func (c *QdrantClient) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if len(req.QueryVector) == 0 {
		return nil, fmt.Errorf("empty query vector")
	}

	if req.VectorName == "" {
		req.VectorName = c.cfg.TextVectorName
	}

	if req.Limit <= 0 {
		req.Limit = 10
	}

	minScore := req.MinScore
	if minScore <= 0 {
		minScore = c.cfg.MinInstantScore
	}

	// Build search request
	searchReq := map[string]any{
		"vector": map[string]any{
			"name":   req.VectorName,
			"vector": req.QueryVector,
		},
		"limit":           req.Limit * 2, // Fetch extra for filtering
		"with_payload":    true,
		"score_threshold": minScore,
	}

	// Add optional filters
	var mustConditions []map[string]any

	if req.Source != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key":   "source",
			"match": map[string]any{"value": req.Source},
		})
	}
	if req.Category != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key":   "category",
			"match": map[string]any{"value": req.Category},
		})
	}
	if req.MediaType != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key":   "media_type",
			"match": map[string]any{"value": req.MediaType},
		})
	}
	if req.Language != "" {
		mustConditions = append(mustConditions, map[string]any{
			"key":   "language",
			"match": map[string]any{"value": req.Language},
		})
	}

	if len(mustConditions) > 0 {
		searchReq["filter"] = map[string]any{
			"must": mustConditions,
		}
	}

	respBody, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/search", c.operationCollection()), searchReq)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return parseSearchResults(respBody, minScore, req.Limit)
}

// DeleteAsset removes an asset from Qdrant by asset ID.
func (c *QdrantClient) DeleteAsset(ctx context.Context, assetID string) error {
	// Convert to Qdrant-compatible point ID (same conversion as buildPoint)
	deleteReq := map[string]any{
		"points": []any{qdrantPointID(assetID)},
	}

	_, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/delete?wait=true", c.operationCollection()), deleteReq)
	if err != nil {
		return fmt.Errorf("delete point: %w", err)
	}

	return nil
}

// CleanupStalePoints scrolls all points in the collection and validates them
// via the provided validator function. Two-pass approach:
//
//  1. Points with tombstoned_at set and still stale → hard delete (grace period expired)
//  2. Points without tombstoned_at and newly stale → set tombstoned_at (soft标记)
//
// Points that fail validation (API errors) are skipped without marking stale.
// Returns count of hard-deleted points.
func (c *QdrantClient) CleanupStalePoints(ctx context.Context, validator func(assetID, driveFileID, driveLink string) (bool, error)) (int, error) {
	const pageSize = 200
	var deleted int
	var offset any

	for {
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
			return deleted, fmt.Errorf("scroll points: %w", err)
		}

		var scrollResp struct {
			Result struct {
				Points         []jsonPoint      `json:"points"`
				NextPageOffset *json.RawMessage `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.Unmarshal(respBody, &scrollResp); err != nil {
			return deleted, fmt.Errorf("parse scroll response: %w", err)
		}

		for _, p := range scrollResp.Result.Points {
			pointID := fmt.Sprint(p.ID)
			assetID := extractString(p.Payload, "asset_id")
			driveLink := extractString(p.Payload, "drive_link")
			driveFileID := extractString(p.Payload, "drive_file_id")
			tombstonedAt := extractString(p.Payload, "tombstoned_at")

			// Skip points with no Drive identifiers
			if driveLink == "" && driveFileID == "" {
				continue
			}

			// Use drive_file_id as primary key (more reliable than URL-based link)
			validateID := driveFileID
			validateLink := driveLink
			if validateID == "" {
				validateID = driveLink
			}

			valid, err := validator(assetID, validateID, validateLink)
			if err != nil {
				c.log.Warn("failed to validate drive file, skipping",
					zap.String("point_id", pointID),
					zap.String("asset_id", assetID),
					zap.Error(err))
				continue
			}

			if valid {
				// File is healthy — clear any tombstone if present
				if tombstonedAt != "" {
					c.clearTombstone(ctx, assetID, pointID)
				}
				continue
			}

			// Drive file is trashed/deleted
			if tombstonedAt == "" {
				// First detection: set tombstone (grace period starts now)
				c.setTombstone(ctx, assetID, pointID)
			} else {
				// Second detection: tombstone already set, hard delete
				if err := c.DeleteAsset(ctx, assetID); err != nil {
					c.log.Warn("failed to delete stale Qdrant point",
						zap.String("asset_id", assetID),
						zap.String("point_id", pointID),
						zap.Error(err))
					continue
				}
				deleted++
				c.log.Info("deleted stale Qdrant point after grace period",
					zap.String("asset_id", assetID),
					zap.String("tombstoned_at", tombstonedAt))
			}
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
			return deleted, ctx.Err()
		default:
		}
	}

	return deleted, nil
}

// setTombstone marks a point as tombstoned without deleting it.
// The next cleanup run will hard-delete it if still stale.
func (c *QdrantClient) setTombstone(ctx context.Context, assetID, pointID string) {
	now := time.Now().UTC().Format(time.RFC3339)
	payload := map[string]any{
		"key": "tombstoned_at",
		"set": now,
	}
	points := []any{qdrantPointID(assetID)}
	setPayloadReq := map[string]any{
		"points":  points,
		"payload": payload,
	}
	if _, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/payload?wait=true", c.operationCollection()), setPayloadReq); err != nil {
		c.log.Warn("failed to set tombstone on stale point",
			zap.String("asset_id", assetID),
			zap.String("point_id", pointID),
			zap.Error(err))
	} else {
		c.log.Info("tombstoned stale Qdrant point (grace period started)",
			zap.String("asset_id", assetID),
			zap.String("point_id", pointID))
	}
}

// clearTombstone removes the tombstone from a point that was previously
// marked stale but is now confirmed healthy.
func (c *QdrantClient) clearTombstone(ctx context.Context, assetID, pointID string) {
	delPayloadReq := map[string]any{
		"points": []any{qdrantPointID(assetID)},
		"keys":   []any{"tombstoned_at"},
	}
	if _, err := c.qdrantRequest(ctx, "POST",
		fmt.Sprintf("/collections/%s/points/payload/delete?wait=true", c.operationCollection()), delPayloadReq); err != nil {
		c.log.Warn("failed to clear tombstone on recovered point",
			zap.String("asset_id", assetID),
			zap.String("point_id", pointID),
			zap.Error(err))
	} else {
		c.log.Info("cleared tombstone on recovered Qdrant point",
			zap.String("asset_id", assetID))
	}
}

// jsonPoint represents a raw Qdrant point for scroll parsing.
type jsonPoint struct {
	ID      any            `json:"id"`
	Payload map[string]any `json:"payload"`
}
