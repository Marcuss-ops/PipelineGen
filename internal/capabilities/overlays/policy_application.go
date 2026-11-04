// Package overlays — policy_application.go: canonical animation-directive
// validation and provenance stamping at the compile boundary.
//
// The legacy animation_policies contract (< v2) is retired: payloads that
// still carry it fail closed instead of being applied. Only the typed
// animation_directives contract is accepted.
package overlays

import (
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PolicyApplicationInput carries the canonical animation inputs the compile
// boundary resolved. AnimationPolicies is retained only as a retired-contract
// tombstone so a stale payload fails with a clear error instead of being
// silently ignored.
type PolicyApplicationInput struct {
	AnimationPolicies        []scriptpkg.AnimationPolicy
	AnimationDirectives      []scriptpkg.AnimationDirective
	AnimationContractVersion int
	AnimationCatalogDigest   string
}

// StampAnimationProvenance saves the selection provenance (catalog digest +
// source) on every item Params. It does not touch MotionID/timing, so
// historical plans without a pinned digest stay byte-identical.
func StampAnimationProvenance(items []capmodel.OverlayItem, catalogDigest, source string) {
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

// ApplyAnimationDirectives validates the canonical v2 directives and stamps
// provenance. Legacy animation_policies never reach item application: a
// non-empty list fails closed. A request with no explicit animation keeps the
// historical byte-identical plan unless a catalog digest is pinned.
func ApplyAnimationDirectives(items []capmodel.OverlayItem, in PolicyApplicationInput) error {
	if len(in.AnimationPolicies) > 0 {
		return fmt.Errorf("animation_policies is retired (contract < v%d); use animation_directives", scriptpkg.AnimationContractVersion)
	}
	if len(in.AnimationDirectives) > 0 && in.AnimationContractVersion < scriptpkg.AnimationContractVersion {
		return fmt.Errorf("animation_directives requires animation_contract_version >= %d (got %d)", scriptpkg.AnimationContractVersion, in.AnimationContractVersion)
	}
	for _, d := range in.AnimationDirectives {
		if err := ValidateAnimationDirective(d); err != nil {
			return fmt.Errorf("animation directive: %w", err)
		}
	}
	if len(in.AnimationDirectives) > 0 || in.AnimationCatalogDigest != "" {
		StampAnimationProvenance(items, in.AnimationCatalogDigest, "directive")
	}
	return nil
}
