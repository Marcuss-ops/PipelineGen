// Package sceneir — validator.go is the fail-closed structural check for a
// compiled SceneIR. It is the boundary MediaCert and the QueryPlanner use
// before consuming a SceneIR: a SceneIR that fails Validate is rejected
// outright, exactly the way a count-only test would have declared success
// at a semantically broken pipeline.
//
// The validator checks:
//
//   - identity completeness (SegmentID, Position, SourceText, SourceTextHash
//     all non-empty/valid);
//   - source-text-hash consistency (the stamped SourceTextHash must match a
//     fresh hash of the current SourceText — catches post-compile tampering);
//   - profile completeness (Subject non-empty AND at least one VisualTerm —
//     catches the visual_profile=null bug);
//   - narration/source separation is NOT enforced as an error here, because
//     narration divergence is allowed; use IsNarrationDivergence to detect it.
package sceneir

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Validate is the fail-closed structural check for a compiled SceneIR. It
// returns the first structural violation found, or nil when the SceneIR is
// structurally sound. It does not check semantic relevance (that is
// MediaCert's job) — only that the immutable contract holds.
func (s SceneIR) Validate() error {
	if strings.TrimSpace(s.SegmentID) == "" {
		return fmt.Errorf("sceneir: segment_id is required")
	}
	if s.Position < 0 {
		return fmt.Errorf("sceneir: position must not be negative")
	}
	if strings.TrimSpace(s.SourceText) == "" {
		return fmt.Errorf("sceneir: source_text is required")
	}
	if strings.TrimSpace(s.SourceTextHash) == "" {
		return fmt.Errorf("sceneir: source_text_hash is required")
	}
	if s.RecomputeSourceTextHash() != s.SourceTextHash {
		return fmt.Errorf("sceneir: source_text_hash does not match a fresh hash of source_text (tampered source)")
	}
	visual := script.BuildSegmentVisualProfile(s.Profile)
	if strings.TrimSpace(visual.Subject) == "" {
		return fmt.Errorf("sceneir: profile.subject is required (visual_profile must not be null)")
	}
	if len(visual.Terms) == 0 {
		return fmt.Errorf("sceneir: profile.visual_terms must not be empty (visual_profile must not be null)")
	}
	return nil
}

// MissingProfileCount reports how many of the given SceneIRs have an empty
// SemanticProfile (the LIVE-test metric: semantic_profiles = X/5). It is
// the helper MediaCert calls to print:
//
//	SEMANTIC PROFILES  5/5
//
// A compiled SceneIR should always report 0 missing; the helper exists so
// a regression that re-introduces null profiles is caught explicitly.
func MissingProfileCount(irs []SceneIR) (missing, total int) {
	total = len(irs)
	for _, ir := range irs {
		visual := script.BuildSegmentVisualProfile(ir.Profile)
		if strings.TrimSpace(visual.Subject) == "" || len(visual.Terms) == 0 {
			missing++
		}
	}
	return missing, total
}
