// Package mediaregistry — identity_audit.go: SQLite implementation of the
// source-identity half of the identity audit (item 12).
//
// The source half answers one question: how many (source_type, source_ref)
// tuples resolve to more than one canonical asset. It reads only the durable
// media_asset_sources table and never inspects the AssetID prefix. The Qdrant
// point-identity half (DuplicateQdrantPoints) is owned by the projection
// auditor (CountDuplicateAssetPoints) and is left 0 here; the orchestrator
// merges the two halves into one capregistry.IdentityAuditReport.
package mediaregistry

import (
	"context"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// AuditIdentity audits the source-identity invariant: the number of
// (source_type, source_ref) tuples that resolve to more than one canonical
// asset. DuplicateQdrantPoints is owned by the Qdrant projection auditor and
// stays 0 in this report.
func (r *CanonicalIdentityResolver) AuditIdentity(ctx context.Context) (capregistry.IdentityAuditReport, error) {
	report := capregistry.IdentityAuditReport{}
	if r == nil || r.db == nil {
		return report, ErrNotWired
	}
	n, err := countDuplicateSourceIdentity(ctx, r.db)
	if err != nil {
		return report, fmt.Errorf("identity audit: duplicate source identity: %w", err)
	}
	report.DuplicateSourceIdentity = n
	return report, nil
}
