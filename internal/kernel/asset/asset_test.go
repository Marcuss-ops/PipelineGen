package asset_test

// TestArtifactIsHardAliasFromArtifacts (PR-c-2 forward-looking guard, July 2026).
//
// Today: asset.Asset (rich media-catalog struct with 30+ Get/Set receiver
// methods) is structurally distinct from any canonical Artifact. A drop-in
// type alias `Asset = <canonical>.Artifact` would break 276 importers
// (callsites relying on Metadata / LifecycleState / MediaType / FolderID /
// DriveLink / the 30+ m.SetXxx methods). Not shippable as single-tornata.
//
// This test PASSES today (asserting non-equality) and will FAIL LOUDLY the
// moment someone types `type Asset = ...` against a canonical Artifact —
// at which point the alias-swap should be evaluated for cascade impact
// before merge (see docs/migrations/phase2-residual.md for the multi-phase
// unblock plan: receiver-method migration, then field harmonization, then
// the assertion flips from != to == here).

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestArtifactIsHardAliasFromArtifacts(t *testing.T) {
	assetType := reflect.TypeOf(asset.Asset{})

	canonicals := []struct {
		name string
		typ  reflect.Type
	}{
		{"artifact.Artifact", reflect.TypeOf(artifact.Artifact{})},
		{"artifacts.Artifact", reflect.TypeOf(artifacts.Artifact{})},
	}

	for _, c := range canonicals {
		if assetType == c.typ {
			t.Errorf(
				"asset.Asset is now a hard alias of %s (%v) — must check 276 importers + 30+ Get/Set methods before the alias lands. See docs/migrations/phase2-residual.md for the multi-phase unblock plan.",
				c.name, c.typ,
			)
		}
	}
}
