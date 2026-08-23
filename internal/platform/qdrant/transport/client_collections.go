// client_collections.go — /collections/* REST surface for the Qdrant client.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. Three of the four methods decode the
// canonical Qdrant envelope `{"result": {...}}` (see types.go's
// schema.CollectionInfo.UnmarshalJSON for the discriminator heuristic) —
// the pre-PR1 decoder treated result as a top-level shape, silently
// returning empty/zero against real Qdrant 1.10+.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// GetCollection fetches collection info from Qdrant.
//
// PR1 — fix/qdrant-wire-contracts: the wire envelope is the canonical
// Qdrant `{"result": {...}}` shape. schema.CollectionInfo.UnmarshalJSON knows
// how to decode that nested envelope AND the legacy flat shape used
// in pre-PR1 test mocks; see types.go::schema.CollectionInfo.UnmarshalJSON
// godoc for the discriminator heuristic. The decoder failure surfaces
// as a typed *APIError carrying the failing operation name so callers
// (CollectionManager.CompareActiveCollection, schema.CompareSchema) can route
// diagnostics without parsing the error string.
func (c *Client) GetCollection(ctx context.Context, name string) (*schema.CollectionInfo, error) {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &ErrCollectionNotFound{Name: name}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseErrorWith(opGetCollection, resp)
	}

	var info schema.CollectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, &APIError{
			Operation: opGetCollection,
			Status:    http.StatusOK,
			Message:   fmt.Sprintf("invalid collection response: %v", err),
			Retryable: false,
		}
	}
	return &info, nil
}

// CreateCollection creates a new collection with the given vector parameters.
func (c *Client) CreateCollection(ctx context.Context, name string, vectors map[string]any, sparseVectors map[string]any) error {
	body := map[string]any{
		"vectors": vectors,
	}
	if len(sparseVectors) > 0 {
		body["sparse_vectors"] = sparseVectors
	}

	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doJSON(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("create collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		// Idempotent: collection already exists (e.g. from a previous
		// failed startup attempt). Not an error — metadata was written
		// but startup aborted before alias promotion.
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// DeleteCollection deletes a collection by name.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, name)
	resp, err := c.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("delete collection %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode != http.StatusOK {
		return c.parseError(resp)
	}
	return nil
}

// ListCollections returns all collection names.
func (c *Client) ListCollections(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/collections", c.baseURL)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var result struct {
		Result struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode collections list: %w", err)
	}

	names := make([]string, len(result.Result.Collections))
	for i, col := range result.Result.Collections {
		names[i] = col.Name
	}
	return names, nil
}

// ── Payload index ────────────────────────────────────────────────────
// Relocated from client_payload_indexes.go (Phase 5 consolidation, June 2026).
// CreatePayloadIndex registers a payload field as indexable.
// Distinct from DeletePayloadKeys (client_points.go) which targets
// /points/payload/delete and is a point-mutation endpoint.

// CreatePayloadIndex creates a payload field index.
func (c *Client) CreatePayloadIndex(ctx context.Context, collection, field, fieldType string) error {
	body := map[string]any{
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
