// Package legacyaudit — single-source-of-truth for legacy Qdrant
// point classification and canonical cleanup contract.
//
// Issue 12 (June 2026): the qdrant-maintenance command consolidates
// clean-qdrant-locators + cleanup-qdrant-legacy under one surface
// with 3 modes: audit (classify all 8 categories, no mutations),
// repair-locators (strip drive_link/local_path keys), and
// delete-invalid (outbox-delete non-locator assets). Per the user
// spec, locator payload keys are repairable — points whose only
// finding is LegacyLocatorPayload are excluded from delete-invalid.
// The 8 categories are the canonical list:
//
//  1. non-media rows           — payload.source is missing or not
//     in the allowed media-type set
//     (video|image|audio). Caused by
//     legacy ingest paths emitting rows
//     without a source discriminator.
//  2. metadata.json            — payload.metadata_json (legacy
//     fingerprint block on payload, NOT
//     on media_assets.metadata_json
//     column). Bulk-import era residue;
//     the canonical asset fingerprint
//     is now indexed_version_<channel>
//     on the per-channel payload key,
//     see IndexSchema.ManifestQ5().
//  3. hidden/temp files        — payload.name (or local-path
//     surrogate) starts with '.' OR
//     ends with '.tmp'/'.bak'/'.swp'.
//     These are Drive upload-residue
//     rows that the sync pipeline did
//     not delete.
//  4. invalid vectors          — at least one dense channel has
//     a NaN/Inf/zero-row vector. Maps
//     directly to ErrNaNOrInf in the
//     canonical PayloadMapper.
//  5. wrong dimensions         — vector channel dim != schema
//     IndexSchema spec dim. The
//     canonical mapping is
//     AssetIDToQdrantPointID +
//     EmbeddingSpec.Dimensions check.
//  6. legacy lifecycle         — payload has both the legacy
//     "status" key AND the canonical
//     "lifecycle_state" key, OR the
//     legacy status is non-empty with
//     lifecycle_state empty. The
//     canonical SSOT is lifecycle_state
//     (QDRANT-004 PR2, see
//     qdrant.DefaultV3Schema.PayloadIndexes).
//  7. legacy locator payload   — payload has drive_link or
//     local_path (the QDRANT-005
//     closure removed both keys from
//     BuildPayload, but legacy upserts
//     still carry them). See
//     qdrant.LocatorCleaner for the
//     single-purpose path; this is
//     the unified-classification path.
//  8. non-canonical point ID   — point.ID is NOT a canonical
//     UUID v5 string (because
//     AssetIDToQdrantPointID always
//     produces UUID v5 hashes). Asset
//     IDs written via raw asset.ID
//     literal are an anti-pattern
//     (every point whose ID is raw
//     has been inserted by a legacy
//     path that bypassed the
//     canonical helper).
//
// Classification is READ-ONLY: pointing at a point never mutates
// its payload or media_assets row. The apply step (Apply via the
// canonical outbox.Dispatcher.EnqueueAndDelete) is a separate
// concern, gated on the dry-run output.
//
// Cross-reference:
//   - Internal/platform/qdrant/locator_cleaner.go cleanup
//     contract for category 7.
//   - Internal/platform/qdrant/pointid.go canonical UUID v5
//     boundary for category 8.
//   - architecture/ownership.yaml ::target_readiness for the
//     downstream consumer.
//   - godlike/06_DATA §"One owner per fact" (this package owns
//     the canonical 8-category list; downstream consumers must
//     import this package, not duplicate it).
//
// # File organisation (PR-SPLIT-LEGACYAUDIT-V2, July 2026)
//
// The 27+ canonical symbols (interfaces + structs + functions +
// per-category helpers + internal helpers) are organised across
// 4 sister files by capability concern (godlike/06 SSOT
// one-canonical-owner-per-fact):
//
//   - audit_collection.go (collection-snapshot walker + read-side
//     port + walker output envelope): QdrantScanner, ScrollPoint,
//     NextOffsetExtractor, Categories, PointAudit, Report, Classify.
//
//   - audit_payload.go (per-point payload classifiers, pure
//     functions): classifyPoint, ClassifierForTesting, nonMediaHit,
//     metadataJSONHit, hiddenTempHit, IsHiddenOrTemp, isHiddenOrTemp,
//     vectorShapeHit, stringFromPayload, floatsFromAny.
//
//   - audit_reconciler.go (drift detection + apply step + canonical
//     point-ID helpers): legacyLifecycleHit, hasKeyNonEmpty,
//     legacyLocatorHit, observeNonCanonicalPointID, CanonicalPointID,
//     IsCanonicalPointID, ApplyRequest, MarshalAudit, ValidateAssetIDs.
//
//   - legacyaudit.go (this file — slim orchestrator): just the
//     package-doc + the cross-capability StringifyReport CLI
//     formatter.
//
// Wave-tracker origin: architecture/current.yaml#LONG-FILES-DECOMPOSITION-V2-2026-07-06
// (PR-SPLIT-LEGACYAUDIT-V2, deadline 2026-07-15; pure code-motion,
// no new symbols, no signature changes, no dependency changes).
package audit

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"fmt"
	"strings"
)

// StringifyReport renders a human-readable summary suitable for the
// CLI default (non-JSON) output. The function is exported so the admin
// command can avoid re-implementing the format.
func StringifyReport(r *Report) string {
	if r == nil {
		return "(no report)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Collection:        %s\n", r.Collection)
	fmt.Fprintf(&b, "Points scrolled:   %d\n", r.TotalPoints)
	fmt.Fprintf(&b, "Complete scan:     %t\n", r.CompleteScan)
	fmt.Fprintf(&b, "Non-media rows:    %d\n", r.Audit.NonMediaRow)
	fmt.Fprintf(&b, "metadata.json:     %d\n", r.Audit.MetadataJSON)
	fmt.Fprintf(&b, "Hidden/temp:       %d\n", r.Audit.HiddenTempFiles)
	fmt.Fprintf(&b, "Invalid vectors:   %d\n", r.Audit.InvalidVectors)
	fmt.Fprintf(&b, "Wrong dimensions:  %d\n", r.Audit.WrongDimensions)
	fmt.Fprintf(&b, "Legacy lifecycle:  %d\n", r.Audit.LegacyLifecycle)
	fmt.Fprintf(&b, "Legacy locator:    %d\n", r.Audit.LegacyLocatorPayload)
	fmt.Fprintf(&b, "Non-canonical ID:  %d\n", r.Audit.NonCanonicalPointID)
	if len(r.Errors) > 0 {
		fmt.Fprintf(&b, "Errors:            %d\n", len(r.Errors))
		for i, e := range r.Errors {
			fmt.Fprintf(&b, "  [%d] %s\n", i, e)
		}
	}
	return b.String()
}
