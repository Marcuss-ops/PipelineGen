package collections

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// GoldenQueryExecutor runs one query against a candidate collection and
// returns the ordered top-K point IDs. It is the seam for the golden
// certification step (item 14): the concrete adapter queries Qdrant against
// the candidate collection.
type GoldenQueryExecutor func(ctx context.Context, collection, query string, topK int) ([]string, error)

// V4RebuildResult summarizes a completed blue-green v4 rebuild.
type V4RebuildResult struct {
	// CollectionName is the signed physical name the generation was built
	// and promoted under (derived from the V4Signature, never hand-authored).
	CollectionName string

	// Signature is the signed identity used to name and verify the rebuild.
	Signature schema.V4Signature

	// GoldenQueriesCertified is true when the golden query set passed the
	// deterministic top-K certification (10 runs, top-10 identical).
	GoldenQueriesCertified bool

	// Activated is true when the alias switch completed successfully.
	Activated bool
}

// RebuildV4Projection drives the full blue-green lifecycle for a signed v4
// generation through the ProjectionManager port:
//
//	V4Signature.PhysicalName → BuildProjectionWith(populate) →
//	ValidateProjection → golden certification → ActivateProjection
//
// The signed physical name is derived via V4Signature.PhysicalName() and is
// therefore anchored to the committed embedding contract + semantic document
// version — two generations that disagree on the signature never collide.
// golden may be nil to skip the certification (schema-only rebuilds); a wired
// executor fails the rebuild on the first non-deterministic query.
func RebuildV4Projection(
	ctx context.Context,
	pm ProjectionManager,
	sig schema.V4Signature,
	projectionID string,
	registrySequence int64,
	expectedPoints int,
	populate ProjectionPopulateFunc,
	golden GoldenQueryExecutor,
) (V4RebuildResult, error) {
	result := V4RebuildResult{Signature: sig}
	name, err := sig.PhysicalName()
	if err != nil {
		return result, fmt.Errorf("rebuild v4: %w", err)
	}
	result.CollectionName = name

	if err := pm.BuildProjectionWith(ctx, projectionID, name, registrySequence, populate); err != nil {
		return result, fmt.Errorf("rebuild v4: build %q: %w", name, err)
	}
	if _, err := pm.ValidateProjection(ctx, projectionID, registrySequence, expectedPoints); err != nil {
		return result, fmt.Errorf("rebuild v4: validate %q: %w", name, err)
	}
	if golden != nil {
		if err := certifyV4Golden(ctx, name, golden); err != nil {
			return result, fmt.Errorf("rebuild v4: %w", err)
		}
		result.GoldenQueriesCertified = true
	}
	if err := pm.ActivateProjection(ctx, projectionID, registrySequence); err != nil {
		return result, fmt.Errorf("rebuild v4: activate %q: %w", name, err)
	}
	result.Activated = true
	return result, nil
}

// RebuildV4 wires the signed v4 rebuild onto this manager. It is a thin
// convenience over RebuildV4Projection so a concrete CollectionManager can be
// driven directly from a V4Signature.
func (cm *CollectionManager) RebuildV4(
	ctx context.Context,
	sig schema.V4Signature,
	projectionID string,
	registrySequence int64,
	expectedPoints int,
	populate ProjectionPopulateFunc,
	golden GoldenQueryExecutor,
) (V4RebuildResult, error) {
	return RebuildV4Projection(ctx, cm, sig, projectionID, registrySequence, expectedPoints, populate, golden)
}

// CertifyGoldenQueries runs the canonical golden query set against the given
// collection GoldenQueryRunCount times each and fails closed on the first
// non-deterministic ordered top-K. It is the single owner of the golden
// certification step (item 14), shared by RebuildV4Projection and the
// operator-facing reindex command.
func CertifyGoldenQueries(ctx context.Context, collection string, exec GoldenQueryExecutor) error {
	return certifyV4Golden(ctx, collection, exec)
}

// certifyV4Golden runs the canonical golden query set against the candidate
// collection GoldenQueryRunCount times each and asserts deterministic ordered
// top-K IDs (item 14).
func certifyV4Golden(ctx context.Context, collection string, exec GoldenQueryExecutor) error {
	queries := schema.CanonicalGoldenQueries()
	results := make([][][]string, len(queries))
	for qi, q := range queries {
		runs := make([][]string, schema.GoldenQueryRunCount)
		for ri := 0; ri < schema.GoldenQueryRunCount; ri++ {
			ids, err := exec(ctx, collection, q.Text, schema.GoldenQueryTopK)
			if err != nil {
				return fmt.Errorf("golden query %d run %d: %w", qi, ri, err)
			}
			runs[ri] = ids
		}
		results[qi] = runs
	}
	return schema.CertifyGoldenDeterminism(results)
}
