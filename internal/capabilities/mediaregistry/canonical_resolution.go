// Package mediaregistry — canonical_resolution.go: the ONE implementation of
// the canonical identity RESOLUTION RULE, independent of the database that
// stores the facts.
//
// The rule used to exist twice, once per adapter
// (internal/platform/postgres/media/canonical_identity.go and
// internal/platform/sqlite/mediaregistry/canonical_identity.go), and the two
// copies differed only in SQL syntax. That is a single-source-of-truth
// violation in the worst place: "does this source resolve to one asset?" and
// "are these bytes multi-provenance?" are SEMANTIC decisions, and two copies
// drift in silence — the symptom would have been deduplication that behaves
// differently on the two stores, with no test able to see it because each test
// only exercised one copy.
//
// What stays adapter-side: the SELECT (dialect, nullable handling, ordering)
// and the scan errors. What lives HERE: input validation, the not-found case,
// the ambiguity fail-closed rule, and the "multi-provenance content leaves
// AssetID empty" rule.
package mediaregistry

import (
	"fmt"
	"strings"
)

// SourceObservation is one durable media_asset_sources row projected into the
// rule: the asset the source points at and the content hash recorded with it.
// The adapter reads rows in the canonical order (is_primary DESC, discovered_at
// ASC) and the rule preserves "last row wins", which is the historical behaviour
// of both implementations.
type SourceObservation struct {
	AssetID       string
	ContentSHA256 string
}

// NormalizeSourceQuery trims and validates a source lookup. An empty half is a
// caller error (ErrAssetSourceInvalid), never an empty result set: it means the
// caller does not know what it is asking for.
func NormalizeSourceQuery(sourceType, sourceRef string) (string, string, error) {
	sourceType = strings.TrimSpace(sourceType)
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceType == "" || sourceRef == "" {
		return "", "", fmt.Errorf("%w: source_type and source_ref are required", ErrAssetSourceInvalid)
	}
	return sourceType, sourceRef, nil
}

// ResolveSourceObservations classifies the durable rows for one
// (sourceType, sourceRef) lookup.
//
// Fail-closed contract: no row is ErrCanonicalIdentityNotFound, more than one
// DISTINCT asset is ErrCanonicalIdentityAmbiguous. Duplicate rows for the same
// asset (re-discoveries) are not ambiguity — they are one identity observed
// several times — and the last observation in adapter order wins.
func ResolveSourceObservations(sourceType, sourceRef string, observations []SourceObservation) (CanonicalIdentity, error) {
	if len(observations) == 0 {
		return CanonicalIdentity{}, fmt.Errorf("%w: source (%s, %s)", ErrCanonicalIdentityNotFound, sourceType, sourceRef)
	}
	assetIDs := make(map[string]struct{}, len(observations))
	var identity CanonicalIdentity
	for _, observation := range observations {
		assetIDs[observation.AssetID] = struct{}{}
		identity = CanonicalIdentity{
			AssetID:       observation.AssetID,
			SourceType:    sourceType,
			SourceRef:     sourceRef,
			ContentSHA256: observation.ContentSHA256,
		}
	}
	if len(assetIDs) > 1 {
		return CanonicalIdentity{}, fmt.Errorf("%w: source (%s, %s) resolves to %d assets", ErrCanonicalIdentityAmbiguous, sourceType, sourceRef, len(assetIDs))
	}
	return identity, nil
}

// NormalizeContentQuery trims and validates a content lookup.
func NormalizeContentQuery(contentSHA256 string) (string, error) {
	contentSHA256 = strings.TrimSpace(contentSHA256)
	if contentSHA256 == "" {
		return "", fmt.Errorf("%w: content_sha256 is required", ErrContentObjectInvalid)
	}
	return contentSHA256, nil
}

// ResolveContentObservations classifies the assets that reference one content
// hash. Exactly one asset means the bytes have a canonical owner; several mean
// the bytes are multi-provenance (one content object, many assets) and AssetID
// is deliberately left empty so the caller treats the identity as a content
// object instead of picking an arbitrary winner.
func ResolveContentObservations(contentSHA256 string, assetIDs []string) (CanonicalIdentity, error) {
	if len(assetIDs) == 0 {
		return CanonicalIdentity{}, fmt.Errorf("%w: content %s", ErrCanonicalIdentityNotFound, contentSHA256)
	}
	identity := CanonicalIdentity{ContentSHA256: contentSHA256}
	if len(assetIDs) == 1 {
		identity.AssetID = assetIDs[0]
	}
	return identity, nil
}
