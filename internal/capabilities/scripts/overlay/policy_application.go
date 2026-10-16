// Package overlay — policy_application.go: single home for the post-compose
// animation-policy projection (moved from overlay_plan_compile.go to respect
// max_lines_per_file_strict=600; same behavior).
//
// P2-5: i piani canonici v2 (direttive risolte + provenance) non ricampionano:
// il renderer valida ID/compatibilità senza resample. Legacy (contract 0/1)
// mantiene dual-read con doppio sampling. Solo tipi kernel/overlays qui
// dentro: nessun import cycle verso il package scriptgeneration.
package overlay

import (
	"fmt"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PolicyApplicationInput carries the run-level animation inputs the compile
// boundary already resolved. Only kernel/overlay types cross this boundary.
type PolicyApplicationInput struct {
	PlanID, VideoID           string
	MotionSeed, CoherenceSeed string
	PhraseMotions             []string
	PhraseMotionFamily        string
	EntityImageMotions        []string
	EntityCaptionMotions      []string
	AnimationPolicies         []scriptpkg.AnimationPolicy
	AnimationDirectives       []scriptpkg.AnimationDirective
	AnimationContractVersion  int
	AnimationCatalogDigest    string
	AnimationCounts           map[string]int
}

// StampAnimationProvenance salva su ogni item Params la provenance della
// selezione (digest catalogo + source). Risponde a: «Perché il sistema ha
// ignorato l'animazione che avevo scelto?». Non tocca MotionID/timing,
// quindi i piani storici senza digest restano byte-identici (lo stamp legacy
// scatta solo con digest pinnato).
func StampAnimationProvenance(items []capabilityoverlay.OverlayItem, catalogDigest, source string) {
	if catalogDigest == "" {
		catalogDigest = "unknown"
	}
	if source == "" {
		source = "legacy"
	}
	for i := range items {
		if items[i].Params == nil {
			items[i].Params = map[string]any{}
		}
		items[i].Params["animation_catalog_digest"] = catalogDigest
		items[i].Params["animation_resolution_source"] = source
		items[i].Params["animation_resolution_status"] = "accepted"
	}
}

// ApplyPoliciesOrDirectives runs the legacy policy projection or validates
// the canonical v2 directives (no resampling on the v2 path), then stamps
// provenance. It runs after the distinct guards with policy-touched items
// pinned on the legacy path: explicit choices stand, automatic collisions
// rotate away.
func ApplyPoliciesOrDirectives(items []capabilityoverlay.OverlayItem, in PolicyApplicationInput) error {
	isCanonicalV2 := in.AnimationContractVersion >= 2 && len(in.AnimationDirectives) > 0
	if isCanonicalV2 {
		for _, d := range in.AnimationDirectives {
			if err := ValidateAnimationDirective(d); err != nil {
				return fmt.Errorf("animation directive: %w", err)
			}
		}
		StampAnimationProvenance(items, in.AnimationCatalogDigest, "directive")
		return nil
	}
	prePolicyMotions := make(map[int]string, len(items))
	for index := range items {
		prePolicyMotions[index] = items[index].MotionID
	}
	if err := ApplyAnimationPolicies(items, in.AnimationPolicies, in.PlanID, in.VideoID, in.EntityImageMotions, in.EntityCaptionMotions); err != nil {
		return fmt.Errorf("animation policies: %w", err)
	}
	if len(in.AnimationPolicies) > 0 {
		pinned := make(map[int]bool, len(items))
		for index := range items {
			if items[index].MotionID != prePolicyMotions[index] {
				pinned[index] = true
			}
		}
		policyInput := capabilityoverlay.PlanInput{PlanID: in.PlanID, VideoID: in.VideoID, MotionSeed: in.MotionSeed, CoherenceSeed: in.CoherenceSeed, AnimationCounts: in.AnimationCounts}
		capabilityoverlay.EnsureDistinctPhraseMotionsPinned(items, policyInput, pinned)
		capabilityoverlay.EnsureDistinctNumberMotionsPinned(items, in.AnimationCounts, pinned)
	}
	// Legacy: provenance solo se il chiamante pinna il digest (P0-3);
	// così i piani storici senza digest restano byte-identici.
	if in.AnimationCatalogDigest != "" {
		StampAnimationProvenance(items, in.AnimationCatalogDigest, "legacy")
	}
	return nil
}
