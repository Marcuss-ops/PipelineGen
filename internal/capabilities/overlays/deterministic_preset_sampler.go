// Package overlays — deterministic_preset_sampler.go owns the SINGLE
// deterministic selection of a (preset, animation) pair for a semantic item.
//
// Pipeline position (see the semantic-index pipeline):
//
//	SemanticItem → Visual Intent Resolver → preset family
//	             → DeterministicPresetSampler → (preset, animation)
//
// Determinism contract: the selection is a pure function of the seed
//
//	job fingerprint + scene_id + semantic_id + preset_family
//
// so the same render job always produces the same preset and animation —
// render 1, render 2 and any replay of the same job are bit-identical. A new
// variant is requested by changing the seed (a new job fingerprint), never by
// wall-clock or random state.
//
// ADR-029 ownership split: this sampler owns ONLY the selection logic (the
// seeded index math). The candidate lists (Presets / Animations) are the
// Chronon-owned visual value space (VisualPresetRegistry) and arrive here as
// INPUT — the sampler never hardcodes a preset id or an animation id, so the
// Go side can never drift from Chronon's catalog.
package overlays

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// PresetSampleInput is the deterministic seed plus the candidate vocabulary
// for one semantic item. Presets and Animations are the candidate ids the
// sampler selects from; they are supplied by the caller (Chronon's
// VisualPresetRegistry catalog projected into the plan), never defined here.
type PresetSampleInput struct {
	// JobFingerprint is the stable content fingerprint of the render job. It
	// is the top-level variant knob: a new fingerprint yields a new selection.
	JobFingerprint string
	// SceneID is the id of the scene the item belongs to.
	SceneID string
	// SemanticID is the stable, scene-scoped semantic item id.
	SemanticID string
	// PresetFamily is the semantic_role / family key (e.g. "important_phrase",
	// "person_image"). It is part of the seed so the same item selects
	// independently within each family.
	PresetFamily string
	// Presets is the candidate preset id list (Chronon-owned value space).
	Presets []string
	// Animations is the candidate animation id list (Chronon-owned value
	// space). Empty means the item selects no animation.
	Animations []string
}

// PresetSample is the deterministic selection result. Empty Preset means the
// input carried no preset candidates; empty Animation means none was selected.
type PresetSample struct {
	Preset    string
	Animation string
}

// DeterministicPresetSampler selects a stable (preset, animation) pair. It is
// stateless and safe for concurrent use.
type DeterministicPresetSampler struct{}

// Sample returns the deterministic (preset, animation) pair for the given
// input. The selection is derived by hashing the 4-component seed
// (fingerprint, scene, semantic id, family) and mapping independent slices of
// the digest onto the candidate lists — the preset index and the animation
// index are decoupled, so the two never collapse to the same position.
//
// Empty candidate lists yield an empty selection (no error): the caller
// validates family existence upstream.
func (DeterministicPresetSampler) Sample(in PresetSampleInput) PresetSample {
	seed := sampleSeed{
		JobFingerprint: in.JobFingerprint,
		SceneID:        in.SceneID,
		SemanticID:     in.SemanticID,
		PresetFamily:   in.PresetFamily,
	}
	// sampleSeed contains only strings; marshal cannot fail in practice. Treat
	// a failure as a programming error rather than an unstable selection.
	b, err := json.Marshal(seed)
	if err != nil {
		panic(fmt.Sprintf("overlays: preset sampler seed marshal: %v", err))
	}
	sum := sha256.Sum256(b)

	var out PresetSample
	if n := len(in.Presets); n > 0 {
		out.Preset = in.Presets[int(binary.BigEndian.Uint64(sum[0:8])%uint64(n))]
	}
	if n := len(in.Animations); n > 0 {
		out.Animation = in.Animations[int(binary.BigEndian.Uint64(sum[8:16])%uint64(n))]
	}
	return out
}

// sampleSeed is the unambiguous serialization envelope for the sampler seed.
// JSON struct-field encoding (stable field order) prevents the ambiguity a
// bare concatenation would introduce (e.g. "a"+"bc" vs "ab"+"c").
type sampleSeed struct {
	JobFingerprint string `json:"job_fingerprint"`
	SceneID        string `json:"scene_id"`
	SemanticID     string `json:"semantic_id"`
	PresetFamily   string `json:"preset_family"`
}

// DefaultDeterministicPresetSampler is the process-wide sampler. Every call
// site samples through this single instance so the selection is uniform
// across the pipeline.
var DefaultDeterministicPresetSampler = DeterministicPresetSampler{}
