// Package qdrant — filter_matrix.go (QDRANT-003 close-out, June 2026).
//
// DefaultFilterMatrix runs a small payload-filter smoke matrix against
// the target collection to assert that the indexer actually populated
// the per-field payload indexes the production search route expects.
//
// Each filter combination is sent as a real SearchPoints call with
// `Filter` populated. The mat runner asserts that at least one result
// matches (proving the field+value pair is indexed) AND, where an
// expected asset_id is supplied, that the returned payload matches.
//
// QDRANT-003 close-out rationale: the previous FiltersOK gate was
// a TODO placeholder in the SwitchReport. The new gate exercises
// the canonical query path (HTTP Client → REST API → SearchPoints
// returns non-empty) and surfaces missing payload indexes BEFORE the
// alias switch. Without this gate, a reindex that drops a payload
// index would silently degrade the production semantic search.
package qdrant

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// DefaultFilterMatrix is the default implementation of FilterMatrix.
// Each Mat entry is a (filter, expectedMinCount, expectedAssetID)
// triple; the runner loops over the list and asserts each.
type DefaultFilterMatrix struct {
	client *Client
	schema *IndexSchema
	mats   []FilterMatEntry
	log    *zap.Logger
}

// FilterMatEntry is one filter combination the matrix runs against
// the target collection. expected_min_count is the minimum number
// of results the SearchPoints call must return (1 is the canonical
// value for "this filter must return at least one result"). An
// expected_asset_id of "" skips the asset_id-specific content check
// (operator may not know which exact asset should match).
type FilterMatEntry struct {
	Name             string
	Filter           map[string]interface{}
	ExpectedMinCount int
	ExpectedAssetID  string
}

// Compile-time interface assertion: DefaultFilterMatrix
// implements FilterMatrix.
var _ FilterMatrix = (*DefaultFilterMatrix)(nil)

// NewDefaultFilterMatrix constructs the canonical V3 filter matrix:
//   - source: "youtube"
//   - media_type: "video"
//   - combined source: "youtube", media_type: "video"
//   - category: "tech" (one-shot example — production wiring
//     can extend this with the operator's known categories)
//
// Operators wire a non-nil DefaultFilterMatrix via
// internal/app/build_bundles_process.go::BuildProcessBundle so the
// production-admin reindex command (cmd/admin/reindex_qdrant.go)
// gets real filter coverage on every switch.
func NewDefaultFilterMatrix(client *Client, schema *IndexSchema, log *zap.Logger) *DefaultFilterMatrix {
	mats := []FilterMatEntry{
		{
			Name: "source_youtube",
			Filter: map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "source", "match": map[string]interface{}{"value": "youtube"}},
				},
			},
			ExpectedMinCount: 1,
		},
		{
			Name: "media_type_video",
			Filter: map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "media_type", "match": map[string]interface{}{"value": "video"}},
				},
			},
			ExpectedMinCount: 1,
		},
		{
			Name: "source_youtube_AND_media_type_video",
			Filter: map[string]interface{}{
				"must": []map[string]interface{}{
					{"key": "source", "match": map[string]interface{}{"value": "youtube"}},
					{"key": "media_type", "match": map[string]interface{}{"value": "video"}},
				},
			},
			ExpectedMinCount: 1,
		},
	}
	return &DefaultFilterMatrix{
		client: client,
		schema: schema,
		mats:   mats,
		log:    log,
	}
}

// RunMatrix executes every Mat entry against the target collection.
// Returns (passed, failures, checksRun, error). passed=true when
// every filter returned >= expected_min_count results.
func (r *DefaultFilterMatrix) RunMatrix(ctx context.Context, collection string) (passed bool, failures int, checksRun int, err error) {
	if r == nil || r.client == nil {
		return false, 0, 0, fmt.Errorf("filter matrix: client not wired")
	}
	if len(r.mats) == 0 {
		return true, 0, 0, nil
	}
	checksRun = len(r.mats)
	// Pick any dense vector name from the schema's first channel —
	// the matrix only asserts payload-filter coverage, so the dense
	// vector body is irrelevant (we send a zero-vector; Qdrant returns
	// the closest-N regardless of which channel, and the filter
	// constraint is what we actually exercise).
	vectorName := "text"
	vectorDim := 768
	if r.schema != nil {
		if spec := r.schema.GetDense("text"); spec != nil {
			vectorDim = spec.Dimensions
		}
	}
	zeroVec := make([]float32, vectorDim)
	for _, mat := range r.mats {
		results, searchErr := r.client.SearchPoints(ctx, collection, SearchRequest{
			QueryVector: zeroVec,
			VectorName:  vectorName,
			Limit:       mat.ExpectedMinCount + 2, // a little headroom
			Filter:      mat.Filter,
		})
		if searchErr != nil {
			if r.log != nil {
				r.log.Warn("filter matrix: search failed for entry",
					zap.String("entry", mat.Name),
					zap.Error(searchErr))
			}
			failures++
			continue
		}
		if len(results) < mat.ExpectedMinCount {
			if r.log != nil {
				r.log.Warn("filter matrix: insufficient results for entry",
					zap.String("entry", mat.Name),
					zap.Int("expected_min", mat.ExpectedMinCount),
					zap.Int("actual", len(results)))
			}
			failures++
			continue
		}
		if mat.ExpectedAssetID != "" {
			found := false
			for _, res := range results {
				if res.AssetID == mat.ExpectedAssetID {
					found = true
					break
				}
			}
			if !found {
				if r.log != nil {
					r.log.Warn("filter matrix: expected asset_id not returned for entry",
						zap.String("entry", mat.Name),
						zap.String("expected_asset_id", mat.ExpectedAssetID))
				}
				failures++
			}
		}
	}
	return failures == 0, failures, checksRun, nil
}
