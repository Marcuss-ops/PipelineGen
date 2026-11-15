package scriptgeneration

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func ownershipPlan(internet mediadomain.MediaToggle, mode, scope string) mediadomain.MediaPlanSpec {
	var plan mediadomain.MediaPlanSpec
	plan.ProviderPolicy.InternetImages = internet
	plan.Extraction.ImageSearchMode = mode
	plan.Extraction.EntityImages.Scope = scope
	return plan
}

func TestVidRushEntityImageOwnsDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan mediadomain.MediaPlanSpec
		want bool
	}{
		{
			name: "per-scene automatic lane owns entity discovery",
			plan: ownershipPlan(mediadomain.MediaToggleEnabled, mediadomain.ImageSearchModeAutomatic, "per_scene"),
			want: true,
		},
		{
			name: "per-scene explicit lane owns entity discovery",
			plan: ownershipPlan(mediadomain.MediaToggleEnabled, mediadomain.ImageSearchModeExplicitOnly, "per_scene"),
			want: true,
		},
		{
			name: "per-scene fallback lane owns entity discovery",
			plan: ownershipPlan(mediadomain.MediaToggleEnabled, mediadomain.ImageSearchModeExplicitThenFallback, "per_scene"),
			want: true,
		},
		{
			name: "disabled image search leaves no owner",
			plan: ownershipPlan(mediadomain.MediaToggleEnabled, mediadomain.ImageSearchModeDisabled, "per_scene"),
			want: false,
		},
		{
			name: "internet images disabled leaves the semantic fallback",
			plan: ownershipPlan(mediadomain.MediaToggleDisabled, mediadomain.ImageSearchModeAutomatic, "per_scene"),
			want: false,
		},
		{
			name: "overlay-only entity scope is not the VidRush lane",
			plan: ownershipPlan(mediadomain.MediaToggleEnabled, mediadomain.ImageSearchModeAutomatic, "entity"),
			want: false,
		},
	} {
		if got := vidRushEntityImageOwnsDiscovery(tc.plan); got != tc.want {
			t.Errorf("%s: owner=%v, want %v (plan=%+v)", tc.name, got, tc.want, tc.plan)
		}
	}
}
