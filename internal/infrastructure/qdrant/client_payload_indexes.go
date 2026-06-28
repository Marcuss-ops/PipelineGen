// client_payload_indexes.go — /collections/{n}/index payload-field-index surface.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. Distinct from client_points.go's
// DeletePayloadKeys (which targets /points/payload/delete and is a
// point-mutation endpoint) — CreatePayloadIndex lands at
// /collections/{n}/index and registers a payload field as indexable
// for Qdrant's internal filter acceleration.
package qdrant

import (
	"context"
	"fmt"
	"net/http"
)

// CreatePayloadIndex creates a payload field index.
func (c *Client) CreatePayloadIndex(ctx context.Context, collection, field, fieldType string) error {
	body := map[string]interface{}{
		"field_name": field,
		"field_type": fieldType,
	}
	url := fmt.Sprintf("%s/collections/%s/index", c.baseURL, collection)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("create index %q on %q: %w", field, collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}
