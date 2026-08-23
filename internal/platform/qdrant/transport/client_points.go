// client_points.go — /collections/{n}/points* REST surface for the Qdrant client.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. CountPoints shares the GetCollection
// envelope (decode schema.CollectionInfo.PointTotal) for consistency with the
// readiness / collection-manager consumers. DeletePayloadKeys lives
// here because it targets /points/payload/delete — it's a point
// mutation, distinct from CreatePayloadIndex which targets
// /collections/{n}/index and lives in client_payload_indexes.go.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// UpsertPoints upserts a batch of points into a collection.
//
// Blocco 4e (July 2026): this method MUST only be called by IndexWriter —
// the single source of truth for Qdrant writes. No other type in the
// codebase may call this directly; all writes route through the canonical
// SSOT (IndexWriter.UpsertFromClip / UpsertFromClips / ReindexAll).
func (c *Client) UpsertPoints(ctx context.Context, collection string, points []schema.Point) error {
	if len(points) == 0 {
		return nil
	}

	body := map[string]any{
		"points": points,
	}
	url := fmt.Sprintf("%s/collections/%s/points?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("upsert points to %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// DeletePoints deletes points by ID from a collection.
//
// Blocco 4e (July 2026): this method MUST only be called by IndexWriter —
// the single source of truth for Qdrant writes. No other type in the
// codebase may call this directly; all deletes route through the canonical
// SSOT (IndexWriter.DeletePoints).
func (c *Client) DeletePoints(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	body := map[string]any{
		"points": ids,
	}
	url := fmt.Sprintf("%s/collections/%s/points/delete?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete points from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// CountPoints returns the number of points in a collection.
//
// PR1 — fix/qdrant-wire-contracts: the canonical Qdrant envelope for
// /collections/{name} returns `{"result": {"points_count": N, ...}}`.
// Pre-PR1 the decoder read `result` as a top-level map which silently
// returned 0 against real Qdrant (because `points_count` was one
// level too shallow). The fix mirrors the GetCollection envelope: we
// decode the full schema.CollectionInfo value so the points_count source
// path stays consistent with the readiness / collection-manager
// consumers, and we read the count off .PointTotal (which is mapped
// from `result.points_count`). See types.go::schema.CollectionInfo for the
// envelope contract.
func (c *Client) CountPoints(ctx context.Context, collection string) (int, error) {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, collection)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, &ErrCollectionNotFound{Name: collection}
	}
	if resp.StatusCode != http.StatusOK {
		return 0, c.parseErrorWith(opCountPoints, resp)
	}

	var info schema.CollectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, &APIError{
			Operation: opCountPoints,
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid collection response: %v", err),
			Retryable: false,
		}
	}
	return info.PointTotal, nil
}

// DeletePayloadKeys removes specific payload keys from points in a collection.
// pointIDs must be non-empty. This wraps the Qdrant POST /points/payload/delete
// endpoint, which is the canonical way to strip legacy keys (e.g. drive_link,
// local_path) without mutating vectors or other payload fields.
//
// QDRANT-005 (June 2026): used by LocatorCleaner to scrub legacy locator
// keys from historical points that were upserted before the QDRANT-001
// payload cleanup.
func (c *Client) DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error {
	if len(keys) == 0 || len(pointIDs) == 0 {
		return nil
	}
	body := map[string]any{
		"keys":   keys,
		"points": pointIDs,
	}
	url := fmt.Sprintf("%s/collections/%s/points/payload/delete?wait=true", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("delete payload keys from %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}
