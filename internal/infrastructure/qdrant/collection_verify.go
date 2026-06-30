package qdrant

import (
	"context"
	"fmt"
)

// ── Verify ─────────────────────────────────────────────────────────────

// VerifyCandidate ensures the candidate's schema matches cm.schema
// via CompareSchema. On success, sets verifyLedger[candidate] = true
// so PromoteCandidate can run. Returns ErrSchemaIncompatible wrapped
// when the candidate's schema doesn't match.
func (cm *CollectionManager) VerifyCandidate(ctx context.Context, candidate string) error {
	info, err := cm.client.GetCollection(ctx, candidate)
	if err != nil {
		return fmt.Errorf("get collection %q: %w", candidate, err)
	}
	diff := CompareSchema(cm.schema, info)
	if !diff.Compatible {
		return fmt.Errorf("%w for %q: missing_vectors=%v dimension_mismatches=%d distance_mismatches=%v",
			NewErrSchemaIncompatible(diff), candidate,
			diff.MissingVectors, len(diff.DimensionMismatches), diff.DistanceMismatches)
	}
	cm.markVerified(candidate)
	return nil
}
