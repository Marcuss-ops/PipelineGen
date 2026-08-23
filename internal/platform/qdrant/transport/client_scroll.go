// client_scroll.go — /collections/{n}/points/scroll REST surface.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. ScrollPoints is the consumer of
// QDRANT-003 (VerifyReindex missing/orphan detection) and the
// QDRANT-005 filter smoke runner; the envelope handler here is the
// canonical decoder for both.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ScrollPoints iterates over all points in a collection using the Qdrant
// scroll API. Returns the batch of points and the next offset (empty string
// when iteration is complete).
//
// filter is an optional Qdrant filter (nil = no filter). When non-nil, only
// points matching the filter are returned. This is used by the QDRANT-005
// filter smoke runner to validate that payload indexes work correctly.
//
// QDRANT-003 (June 2026): used by VerifyReindex to compare Qdrant point
// IDs against SQLite assets for missing/orphan detection.
func (c *Client) ScrollPoints(ctx context.Context, collection string, offset string, limit int, filter map[string]any) (*schema.ScrollResult, error) {
	return c.scroll(ctx, collection, offset, limit, filter, false)
}

// ScrollPointsWithVector is like ScrollPoints but returns the vector(s)
// of each point alongside the payload. Used by diagnostics and the
// operator console to verify the dimensionality of stored embeddings.
func (c *Client) ScrollPointsWithVector(ctx context.Context, collection string, offset string, limit int, filter map[string]any) (*schema.ScrollResult, error) {
	return c.scroll(ctx, collection, offset, limit, filter, true)
}

func (c *Client) scroll(ctx context.Context, collection string, offset string, limit int, filter map[string]any, withVector bool) (*schema.ScrollResult, error) {
	body := map[string]any{
		"limit":        limit,
		"with_payload": true,
		"with_vector":  withVector,
	}
	if offset != "" {
		body["offset"] = offset
	}
	if filter != nil {
		body["filter"] = filter
	}

	url := fmt.Sprintf("%s/collections/%s/points/scroll", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("scroll %q: %w", collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &ErrCollectionNotFound{Name: collection}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	type scrollPoint struct {
		ID      string         `json:"id"`
		Payload map[string]any `json:"payload,omitempty"`
		Vector  map[string]any `json:"vector,omitempty"`
	}
	var result struct {
		Result struct {
			Points         []scrollPoint `json:"points"`
			NextPageOffset *string       `json:"next_page_offset"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode scroll result: %w", err)
	}

	points := make([]schema.ScrollPoint, len(result.Result.Points))
	for i, p := range result.Result.Points {
		points[i] = schema.ScrollPoint{
			ID:      p.ID,
			Payload: p.Payload,
			Vector:  p.Vector,
		}
	}

	nextOffset := ""
	if result.Result.NextPageOffset != nil {
		nextOffset = *result.Result.NextPageOffset
	}

	return &schema.ScrollResult{
		Points:     points,
		NextOffset: nextOffset,
	}, nil
}
