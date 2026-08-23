package script

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// CanonicalizeSourceText returns the byte-deterministic planning source.
func CanonicalizeSourceText(s string) string {
	n := strings.ReplaceAll(s, "\r\n", "\n")
	return norm.NFC.String(n)
}

// ComputeSourceHash hashes the canonicalized source text.
func ComputeSourceHash(s string) string {
	sum := digest.SHA256Bytes([]byte(CanonicalizeSourceText(s)))
	return sum
}

// SourceAnchor is an immutable byte-range reference into canonical source text.
type SourceAnchor struct {
	SourceHash  string `json:"source_hash"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	Excerpt     string `json:"excerpt,omitempty"`
}

func (a *SourceAnchor) Validate(parentPlanSourceHash string) error {
	if a == nil {
		return fmt.Errorf("source anchor: nil")
	}
	var details []string
	if a.SourceHash != parentPlanSourceHash {
		details = append(details, fmt.Sprintf(
			"source_hash mismatch (anchor=%q, plan=%q)",
			a.SourceHash, parentPlanSourceHash))
	}
	if a.StartOffset < 0 {
		details = append(details, fmt.Sprintf(
			"start_offset must be >= 0, got %d", a.StartOffset))
	}
	if a.EndOffset < a.StartOffset {
		details = append(details, fmt.Sprintf(
			"end_offset must be >= start_offset (start=%d end=%d)",
			a.StartOffset, a.EndOffset))
	}
	if len(details) > 0 {
		return fmt.Errorf("source anchor: %s", strings.Join(details, "; "))
	}
	return nil
}

// ClipSearchSlot is one visual requirement emitted by the pre-planner.
type ClipSearchSlot struct {
	Ref              string        `json:"ref"`
	Topic            string        `json:"topic,omitempty"`
	SourceAnchor     *SourceAnchor `json:"source_anchor,omitempty"`
	SearchQuery      string        `json:"search_query,omitempty"`
	VisualIntent     string        `json:"visual_intent,omitempty"`
	TargetDurationMs int64         `json:"target_duration_ms,omitempty"`
	Required         bool          `json:"required"`
}

func (s *ClipSearchSlot) Validate(parentPlanSourceHash string) error {
	if s == nil {
		return fmt.Errorf("clip search slot: nil")
	}
	ref := strings.TrimSpace(s.Ref)
	if ref == "" {
		return fmt.Errorf("clip search slot: ref is required")
	}
	if !strings.HasPrefix(ref, "slot-") {
		return fmt.Errorf("clip search slot: ref %q must start with \"slot-\"", ref)
	}
	if s.SourceAnchor == nil {
		return fmt.Errorf("clip search slot: source_anchor is required")
	}
	return s.SourceAnchor.Validate(parentPlanSourceHash)
}

// ClipPrePlan is the deterministic, provenance-attached pre-planner output.
type ClipPrePlan struct {
	Version     int              `json:"version"`
	Fingerprint string           `json:"fingerprint,omitempty"`
	SourceHash  string           `json:"source_hash"`
	Title       string           `json:"title"`
	Slots       []ClipSearchSlot `json:"slots"`
}

func (p *ClipPrePlan) Validate() error {
	if p == nil {
		return fmt.Errorf("clip pre plan: nil")
	}
	var details []string
	if p.Version != 1 {
		details = append(details, fmt.Sprintf(
			"unsupported version %d (expected 1)", p.Version))
	}
	if p.SourceHash == "" {
		details = append(details, "source_hash is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		details = append(details, "title is required")
	}
	seenRefs := make(map[string]struct{})
	for i, slot := range p.Slots {
		if err := slot.Validate(p.SourceHash); err != nil {
			details = append(details, fmt.Sprintf("slots[%d]: %s", i, err.Error()))
		}
		ref := strings.TrimSpace(slot.Ref)
		expected := fmt.Sprintf("slot-%d", i+1)
		if ref != expected {
			details = append(details, fmt.Sprintf(
				"slots[%d]: ref %q does not match expected %q (no gaps, in order)",
				i, ref, expected))
		}
		if _, dup := seenRefs[ref]; dup {
			details = append(details, fmt.Sprintf(
				"slots[%d]: duplicate ref %q (slot refs must be unique)",
				i, ref))
		} else {
			seenRefs[ref] = struct{}{}
		}
	}
	if len(details) > 0 {
		return fmt.Errorf("clip pre plan: %s", strings.Join(details, "; "))
	}
	return nil
}

// ClipCandidate is one search match emitted for a slot.
type ClipCandidate struct {
	SlotRef               string             `json:"slot_ref"`
	AssetRef              string             `json:"asset_ref"`
	SemanticScore         float64            `json:"semantic_score"`
	VisualScore           float64            `json:"visual_score,omitempty"`
	QualityScore          float64            `json:"quality_score,omitempty"`
	DurationMs            int64              `json:"duration_ms,omitempty"`
	TranscriptSnippet     string             `json:"transcript_snippet,omitempty"`
	Language              string             `json:"language,omitempty"`
	DriveLinkEmpty        bool               `json:"drive_link_empty,omitempty"`
	WitnessedAtMs         int64              `json:"witnessed_at_ms,omitempty"`
	PerSlotScoreBreakdown map[string]float64 `json:"per_slot_score_breakdown,omitempty"`
}

// SlotClipBinding is the backend binding produced by the clip sampler.
type SlotClipBinding struct {
	SlotRef      string        `json:"slot_ref"`
	ClipID       string        `json:"clip_id"`
	ClipTitle    string        `json:"clip_title,omitempty"`
	DriveLink    string        `json:"drive_link,omitempty"`
	StartMs      int64         `json:"start_ms,omitempty"`
	EndMs        int64         `json:"end_ms,omitempty"`
	SourceAnchor *SourceAnchor `json:"source_anchor,omitempty"`
	Embedding    []float32     `json:"embedding,omitempty"`
}

// ResolvedClipSlot ties a selected candidate back to its plan slot.
type ResolvedClipSlot struct {
	Ref            string             `json:"ref"`
	Topic          string             `json:"topic,omitempty"`
	SourceAnchor   *SourceAnchor      `json:"source_anchor,omitempty"`
	ChosenAssetRef string             `json:"chosen_asset_ref"`
	SemanticScore  float64            `json:"semantic_score"`
	VisualScore    float64            `json:"visual_score,omitempty"`
	Narrative      *NarrativeClipView `json:"narrative,omitempty"`
	Binding        *SlotClipBinding   `json:"binding,omitempty"`
}

func (s *ResolvedClipSlot) Validate(parentPlanSourceHash string) error {
	if s == nil {
		return fmt.Errorf("resolved clip slot: nil")
	}
	ref := strings.TrimSpace(s.Ref)
	if ref == "" {
		return fmt.Errorf("resolved clip slot: ref is required")
	}
	if !strings.HasPrefix(ref, "slot-") {
		return fmt.Errorf("resolved clip slot: ref %q must start with \"slot-\"", ref)
	}
	if strings.TrimSpace(s.ChosenAssetRef) == "" {
		return fmt.Errorf("resolved clip slot: chosen_asset_ref is required")
	}
	if s.SourceAnchor != nil {
		if err := s.SourceAnchor.Validate(parentPlanSourceHash); err != nil {
			return fmt.Errorf("resolved clip slot: source_anchor: %s", err.Error())
		}
	}
	return nil
}
