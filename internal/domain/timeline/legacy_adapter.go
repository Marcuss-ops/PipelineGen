// Package timeline — legacy_adapter.go (FASE C, July 2026).
//
// FASE C — Adapter legacy: maps old-style layers with explicit
// temporal bounds (from/duration) into the new SequenceNode-based
// model. This prevents breaking existing content during migration.
//
// The conversion rule (per the plan §3):
//
//	LegacyLayer{Name, From, Duration, Node}
//	  → SequenceNode{
//	      Name: Name + "_legacy",
//	      Spec: SequenceSpec{From: From, Duration: Duration},
//	      Children: []TimelineNode{Node},
//	    }
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of the LegacyLayer type and WrapLegacyLayer
// family of functions. After FASE F (elimina legacy), this entire
// file will be git-rm'd — the deprecation record lives in
// architecture/deprecations.yaml.
//
// godlike/07: LegacyLayer is intentionally a SEPARATE type from
// LayerNode. Mixing temporal bounds into LayerNode would violate
// the godlike/07 contract "LayerNode has NO from/duration fields."
// The adapter is the BRIDGE, not a backdoor.
package timeline

// ── LegacyLayer ────────────────────────────────────────────────────

// LegacyLayer represents a pre-FASE-C layer with explicit temporal
// bounds (from + optional duration). It exists ONLY as a migration
// bridge — all new content MUST use SequenceNode + LayerNode/MediaNode.
//
// Deprecated: use SequenceNode with child LayerNode/MediaNode instead.
// This type will be removed in FASE F.
type LegacyLayer struct {
	// Name is the layer identifier (e.g. "logo", "title").
	// The generated sequence will be named Name + "_legacy".
	Name string `json:"name"`

	// From is the frame at which the layer becomes active in
	// its parent's time space. Maps directly to SequenceSpec.From.
	From Frame `json:"from"`

	// Duration is the length of the layer in frames.
	// nil means "infinite" (layer stays active forever after From).
	// Maps directly to SequenceSpec.Duration.
	Duration *Frame `json:"duration,omitempty"`

	// Node is the actual content (LayerNode or MediaNode).
	// Becomes the sequence's only child.
	Node TimelineNode `json:"node"`
}

// ── WrapLegacyLayer ────────────────────────────────────────────────

// WrapLegacyLayer converts a single legacy layer into a SequenceNode.
// The generated sequence inherits the layer's temporal bounds, and
// the layer's content becomes the sequence's only child.
//
// The sequence name is the layer name + "_legacy" suffix, per the
// plan's naming convention. This distinguishes auto-generated
// sequences from user-defined ones and makes it easy to grep for
// legacy sites during FASE F cleanup.
func WrapLegacyLayer(legacy LegacyLayer) SequenceNode {
	return SequenceNode{
		Name: legacy.Name + "_legacy",
		Spec: SequenceSpec{
			From:     legacy.From,
			Duration: legacy.Duration,
		},
		Children: []TimelineNode{legacy.Node},
	}
}

// WrapLegacyLayers converts a slice of legacy layers into a slice of
// SequenceNodes. Each legacy layer becomes its own sequence wrapping
// its content. Order is preserved.
//
// Returns nil if the input slice is nil or empty.
func WrapLegacyLayers(legacyLayers []LegacyLayer) []SequenceNode {
	if len(legacyLayers) == 0 {
		return nil
	}
	seqs := make([]SequenceNode, len(legacyLayers))
	for i, l := range legacyLayers {
		seqs[i] = WrapLegacyLayer(l)
	}
	return seqs
}

// ── Composition helpers ────────────────────────────────────────────

// AddLegacyLayer is a convenience method that wraps a legacy layer
// into a SequenceNode and adds it to the root sequence's children.
// Equivalent to:
//
//	comp.AddToRoot(WrapLegacyLayer(legacy))
//
// This keeps migration code concise — one call instead of two.
func (c *Composition) AddLegacyLayer(legacy LegacyLayer) {
	c.AddToRoot(WrapLegacyLayer(legacy))
}

// AddLegacyLayers is a batch convenience method that wraps multiple
// legacy layers and adds them all to the root sequence's children.
// Order is preserved.
func (c *Composition) AddLegacyLayers(legacyLayers []LegacyLayer) {
	for _, l := range legacyLayers {
		c.AddLegacyLayer(l)
	}
}
