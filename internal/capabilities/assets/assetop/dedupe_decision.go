package assetop

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// DedupeDecision is the ONE answer a duplicate check may act on.
//
// It deliberately separates three facts that were previously collapsed into a
// single "duplicate" flag:
//
//	existence  — this logical asset (ID) already has a row
//	certification — its content address equals the bytes we are about to store
//	delivery   — that row's Drive publication actually completed
//
// A row that exists with the same bytes is NOT evidence that the bytes reached
// Drive: a failed upload leaves exactly such a row behind (PUBLISH_PENDING or
// PUBLISH_FAILED). Only SkipID authorizes reporting a completed publish, and it
// is granted only when the required delivery is certified on the matched row.
type DedupeDecision struct {
	Kind        DuplicateKind
	Record      *AssetRecord
	SameContent bool
	// SkipID is the idempotent-retry case: the same logical asset with the same
	// bytes, the policy allows skipping, and (when delivery is required) the
	// matched row carries a PUBLISHED delivery identity.
	SkipID bool
	// ReuseStorage is the shared-physical-content case: a DIFFERENT logical
	// asset owns these bytes. The new record must still be created.
	ReuseStorage bool
	// DeliveryCertified records whether the matched row proves a completed
	// publish. It is meaningful only when SkipID was considered.
	DeliveryCertified bool
}

// DecideDedupe is the single decision function consumed by both the read-only
// preview (CheckDuplicate) and the side-effecting path (ProcessAsset), so the
// two can never disagree about what a match means.
//
// deliveryRequired says whether this call must deliver the bytes to an external
// publisher. When it is true, a match is only skippable if the existing record
// is PUBLISHED and carries a Drive identity; otherwise the asset must be
// processed (and published) again.
func DecideDedupe(match *DuplicateMatch, policy DuplicatePolicy, deliveryRequired bool) DedupeDecision {
	decision := DedupeDecision{}
	if match == nil {
		return decision
	}
	decision.Kind = match.Kind
	decision.Record = match.Record
	decision.SameContent = match.SameContent
	if match.Record == nil {
		return decision
	}

	switch match.Kind {
	case DuplicateSameAsset:
		if !match.SameContent || !policy.SkipIfExists {
			return decision
		}
		decision.DeliveryCertified = DeliveryCertified(match.Record)
		decision.SkipID = !deliveryRequired || decision.DeliveryCertified
	case DuplicateSameContent:
		decision.ReuseStorage = true
	}
	return decision
}

// DeliveryCertified reports whether a stored record proves that its bytes were
// actually delivered: an external file identity AND a publish state that does
// not deny delivery.
//
// The identity is the primary evidence — only the uploader produces a Drive
// file id — while the state is a veto: PUBLISH_PENDING and PUBLISH_FAILED rows
// describe an upload that did not complete, so they can never certify one. A
// PUBLISHED row without an identity proves nothing either. The empty state is
// accepted for rows written before the column existed when they already carry
// an identity; rejecting them would re-upload every pre-existing asset.
func DeliveryCertified(record *AssetRecord) bool {
	if record == nil {
		return false
	}
	status := strings.TrimSpace(record.PublishStatus)
	if status != "" && !strings.EqualFold(status, string(asset.AssetPublishPublished)) {
		return false
	}
	return strings.TrimSpace(record.DriveFileID) != "" || strings.TrimSpace(record.DriveLink) != ""
}
