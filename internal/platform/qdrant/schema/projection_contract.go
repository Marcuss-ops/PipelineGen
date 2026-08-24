// Package schema — projection_contract.go is the SSOT that separates
// the three Qdrant projections into dedicated, non-overlapping contracts.
//
// item 8 (Qdrant projection separation, August 2026): a frame is not an
// asset and a concept is not an asset. They must never share a point ID,
// a runtime alias, or a retention scope with media_assets. This file
// declares ONE canonical contract per projection:
//
//	media_assets    → DefaultV3Schema()     (asset point IDs = bare UUID v8)
//	media_frames    → FrameIndexSchema()    (point IDs = "frame-" + UUID v8)
//	media_concepts  → ConceptIndexSchema()  (point IDs = "concept-" + ID)
//
// Each contract binds its manifest (IndexSchema), runtime alias, retention
// prefix and point-ID namespace into a single self-describing value, so a
// future writer/manager can be constructed per-projection without
// re-declaring any of these facts inline. All writes still flow through the
// generic ProjectionWriter capability (indexing.ProjectionWriter); never
// through transport.Client.UpsertPoints/DeletePoints directly.
package schema

import (
	"fmt"
	"strings"

)

// ProjectionKind is the logical name of a Qdrant projection. A projection is
// a derived, rebuildable view of canonical SQLite state. The three canonical
// projections are closed over by this package.
type ProjectionKind string

const (
	// ProjectionMediaAssets is the canonical asset projection
	// (1 canonical asset = 1 point, point ID = canonical asset ID).
	ProjectionMediaAssets ProjectionKind = "media_assets"

	// ProjectionMediaFrames is the keyframe projection. A keyframe is a
	// (video_id, ts_ms) timestamped visual — NOT an asset.
	ProjectionMediaFrames ProjectionKind = "media_frames"

	// ProjectionMediaConcepts is the phrase-derived entity projection. A
	// concept is a phrase → approved-binding entity — NOT an asset.
	ProjectionMediaConcepts ProjectionKind = "media_concepts"
)

// ConceptPointIDPrefix is the canonical Qdrant point-id prefix for
// media_concepts. It is declared here (next to the contract) so the
// concept projection's point-ID namespace has one owner. The legacy
// unexported copy in qdrantmm.conceptPointIDPrefix must collapse into
// this constant.
const ConceptPointIDPrefix = "concept-"

// AssetPointIDPrefix is intentionally empty: canonical asset points are
// bare UUID v8 (see AssetIDToQdrantPointID), with no textual prefix. The
// empty value is still a distinct namespace from the frame/concept
// prefixes, which is what ValidateProjectionSeparation enforces.
const AssetPointIDPrefix = ""

// ProjectionContract binds one Qdrant projection to its canonical identity:
// the manifest (IndexSchema), the runtime alias, the retention prefix, and
// the point-ID namespace.
type ProjectionContract struct {
	// Kind is the logical projection name (media_assets / media_frames /
	// media_concepts). It is the closed-set discriminator used by
	// ValidateProjectionSeparation.
	Kind ProjectionKind

	// Schema is the canonical manifest: PhysicalName + RuntimeAlias +
	// vector channels + payload indexes for this projection.
	Schema *IndexSchema

	// RetentionPrefix is the stable physical-name prefix the retention
	// sweep uses to identify this projection's generations. It MUST be a
	// prefix of Schema.PhysicalName so a retention sweep can never cross
	// into another projection's collections.
	RetentionPrefix string

	// PointIDPrefix is the canonical point-ID namespace emitted by this
	// projection's writer. It MUST be distinct across projections so a
	// reindex can never collide two projections into the same point.
	PointIDPrefix string
}

// Alias returns the projection's runtime alias (fail-closed on a nil schema).
func (c ProjectionContract) Alias() string {
	if c.Schema == nil {
		return ""
	}
	return c.Schema.RuntimeAlias
}

// PhysicalName returns the projection's physical collection name
// (fail-closed on a nil schema).
func (c ProjectionContract) PhysicalName() string {
	if c.Schema == nil {
		return ""
	}
	return c.Schema.PhysicalName
}

// MediaAssetsProjection returns the dedicated contract for the canonical
// asset projection.
func MediaAssetsProjection() ProjectionContract {
	s := DefaultV3Schema()
	return ProjectionContract{
		Kind:            ProjectionMediaAssets,
		Schema:          s,
		RetentionPrefix: s.PhysicalName,
		PointIDPrefix:   AssetPointIDPrefix,
	}
}

// MediaFramesProjection returns the dedicated contract for the keyframe
// projection.
func MediaFramesProjection() ProjectionContract {
	s := FrameIndexSchema()
	return ProjectionContract{
		Kind:            ProjectionMediaFrames,
		Schema:          s,
		RetentionPrefix: s.PhysicalName,
		PointIDPrefix:   FramePointIDPrefix,
	}
}

// MediaConceptsProjection returns the dedicated contract for the concept
// projection.
func MediaConceptsProjection() ProjectionContract {
	s := ConceptIndexSchema()
	return ProjectionContract{
		Kind:            ProjectionMediaConcepts,
		Schema:          s,
		RetentionPrefix: s.PhysicalName,
		PointIDPrefix:   ConceptPointIDPrefix,
	}
}

// AllProjections returns the three canonical projection contracts in a
// stable order (assets, frames, concepts).
func AllProjections() []ProjectionContract {
	return []ProjectionContract{
		MediaAssetsProjection(),
		MediaFramesProjection(),
		MediaConceptsProjection(),
	}
}

// IsCanonicalKind reports whether k is one of the three closed-set kinds.
func IsCanonicalKind(k ProjectionKind) bool {
	switch k {
	case ProjectionMediaAssets, ProjectionMediaFrames, ProjectionMediaConcepts:
		return true
	default:
		return false
	}
}

// Validate performs the per-contract fail-closed checks. It does NOT require
// Schema.Validate() to pass: the frame/concept schemas legitimately use
// name==alias today (no separate runtime alias), which IndexSchema.Validate
// rejects. Projection separation is enforced by ValidateProjectionSeparation.
func (c ProjectionContract) Validate() error {
	if !IsCanonicalKind(c.Kind) {
		return fmt.Errorf("projection contract: unknown kind %q", c.Kind)
	}
	if c.Schema == nil {
		return fmt.Errorf("projection contract %q: nil schema", c.Kind)
	}
	if strings.TrimSpace(c.Schema.PhysicalName) == "" {
		return fmt.Errorf("projection contract %q: empty physical name", c.Kind)
	}
	if strings.TrimSpace(c.Schema.RuntimeAlias) == "" {
		return fmt.Errorf("projection contract %q: empty runtime alias", c.Kind)
	}
	if strings.TrimSpace(c.RetentionPrefix) == "" {
		return fmt.Errorf("projection contract %q: empty retention prefix", c.Kind)
	}
	if !strings.HasPrefix(c.Schema.PhysicalName, c.RetentionPrefix) {
		return fmt.Errorf("projection contract %q: retention prefix %q is not a prefix of physical name %q", c.Kind, c.RetentionPrefix, c.Schema.PhysicalName)
	}
	return nil
}

// ValidateProjectionSeparation enforces the 3-way non-overlap invariant: no
// two projections may share a kind, physical name, runtime alias, retention
// prefix, or point-ID namespace. It fails closed on the first violation.
func ValidateProjectionSeparation(contracts []ProjectionContract) error {
	kinds := make(map[ProjectionKind]ProjectionContract, len(contracts))
	physical := make(map[string]ProjectionContract, len(contracts))
	aliases := make(map[string]ProjectionContract, len(contracts))
	retention := make(map[string]ProjectionContract, len(contracts))
	pointPrefixes := make(map[string]ProjectionContract, len(contracts))

	for _, c := range contracts {
		if err := c.Validate(); err != nil {
			return err
		}
		if prev, ok := kinds[c.Kind]; ok {
			return fmt.Errorf("projection separation: kind %q declared twice (%q)", c.Kind, prev.PhysicalName())
		}
		kinds[c.Kind] = c
		if prev, ok := physical[c.Schema.PhysicalName]; ok {
			return fmt.Errorf("projection separation: physical name %q shared by %q and %q", c.Schema.PhysicalName, prev.Kind, c.Kind)
		}
		physical[c.Schema.PhysicalName] = c
		if prev, ok := aliases[c.Schema.RuntimeAlias]; ok {
			return fmt.Errorf("projection separation: runtime alias %q shared by %q and %q", c.Schema.RuntimeAlias, prev.Kind, c.Kind)
		}
		aliases[c.Schema.RuntimeAlias] = c
		if prev, ok := retention[c.RetentionPrefix]; ok {
			return fmt.Errorf("projection separation: retention prefix %q shared by %q and %q", c.RetentionPrefix, prev.Kind, c.Kind)
		}
		retention[c.RetentionPrefix] = c
		if prev, ok := pointPrefixes[c.PointIDPrefix]; ok {
			return fmt.Errorf("projection separation: point-id prefix %q shared by %q and %q", c.PointIDPrefix, prev.Kind, c.Kind)
		}
		pointPrefixes[c.PointIDPrefix] = c
	}
	return nil
}
