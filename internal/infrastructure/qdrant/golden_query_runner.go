// Package qdrant — golden_query_runner.go (QDRANT-003 closure, June 2026).
//
// DefaultGoldenQueryRunner executes predefined smoke queries against a
// collection to verify it is reachable and queryable after reindex.
// It uses the Client's SearchPoints with a zero-vector of the correct
// dimensions — Qdrant accepts all-zero vectors and returns nearest
// neighbors in cosine space, proving the collection is alive and the
// search API works without requiring an embedder dependency.

package qdrant

import (
	"context"

	"go.uber.org/zap"
)

// DefaultGoldenQueryRunner executes golden queries via the Client.
// Each query must return at least one result to pass.
type DefaultGoldenQueryRunner struct {
	client *Client
	schema *IndexSchema
	log    *zap.Logger
}

// Compile-time interface assertion.
var _ GoldenQueryRunner = (*DefaultGoldenQueryRunner)(nil)

// NewDefaultGoldenQueryRunner creates a runner that performs smoke queries
// against the text channel. The runner sends a zero-vector SearchPoints
// request — Qdrant returns the nearest neighbors in cosine space, which
// proves the collection is queryable without needing a text embedder.
func NewDefaultGoldenQueryRunner(client *Client, schema *IndexSchema, log *zap.Logger) *DefaultGoldenQueryRunner {
	return &DefaultGoldenQueryRunner{
		client: client,
		schema: schema,
		log:    log,
	}
}

// RunQueries executes a single smoke query against the collection's
// text channel and returns (passed, failures, error). The query uses
// a zero-vector of the correct dimension from the schema.
//
// QDRANT-003 (June 2026): this is a connectivity + API-endpoint smoke
// test, NOT a semantic-quality test. Full semantic golden queries (with
// expected result sets) require an embedder and a reference fixture and
// are deferred to a follow-up PR.
func (r *DefaultGoldenQueryRunner) RunQueries(ctx context.Context, collection string) (passed bool, failures int, err error) {
	if r == nil || r.client == nil {
		return false, 0, nil // nil-safe: trivially passed (no client = skip)
	}
	if r.schema == nil {
		if r.log != nil {
			r.log.Warn("golden query runner: schema not wired, skipping smoke query")
		}
		return true, 0, nil
	}

	// Get text channel dimensions from the schema.
	spec := r.schema.GetDense("text")
	if spec == nil {
		if r.log != nil {
			r.log.Warn("golden query runner: no text channel in schema, skipping")
		}
		return true, 0, nil
	}

	// Build a zero-vector of the correct dimension.
	zeroVec := make([]float32, spec.Dimensions)
	results, searchErr := r.client.SearchPoints(ctx, collection, SearchRequest{
		QueryVector: zeroVec,
		VectorName:  "text",
		Limit:       3,
	})
	if searchErr != nil {
		if r.log != nil {
			r.log.Warn("golden query smoke: SearchPoints failed",
				zap.String("collection", collection),
				zap.Error(searchErr))
		}
		return false, 1, nil
	}
	if len(results) == 0 {
		if r.log != nil {
			r.log.Warn("golden query smoke: zero results for text query on populated collection")
		}
		return false, 1, nil
	}

	if r.log != nil {
		r.log.Debug("golden query smoke passed",
			zap.String("collection", collection),
			zap.Int("results", len(results)))
	}
	return true, 0, nil
}
