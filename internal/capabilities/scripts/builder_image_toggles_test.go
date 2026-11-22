package scriptgeneration

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestNormalizeImageToggleCoherence(t *testing.T) {
	tests := []struct {
		name      string
		req       GenerateRequest
		wantLimit int
	}{
		{
			name: "both generic knobs off clear a stale budget",
			req: GenerateRequest{
				GenerateSceneImages: scriptpkg.ToggleDisabled,
				MaxImageOverlays:    25,
			},
			wantLimit: 0,
		},
		{
			name: "entity lane keeps the shared budget",
			req: GenerateRequest{
				GenerateSceneImages: scriptpkg.ToggleDisabled,
				MaxImageOverlays:    25,
				MediaPlan: mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
					EntityImages: mediadomain.EntityImagePolicy{Enabled: true},
				}},
			},
			wantLimit: 25,
		},
		{
			name: "images_per_scene still requests the generic lane",
			req: GenerateRequest{
				GenerateSceneImages: scriptpkg.ToggleDisabled,
				MaxImageOverlays:    25,
				ScriptParams:        scriptpkg.ScriptSpec{ImagesPerScene: 2},
			},
			wantLimit: 25,
		},
		{
			name: "enabled output keeps the budget even with zero images_per_scene",
			req: GenerateRequest{
				GenerateSceneImages: scriptpkg.ToggleEnabled,
				MaxImageOverlays:    25,
			},
			wantLimit: 25,
		},
		{
			name: "nothing to clear",
			req: GenerateRequest{
				GenerateSceneImages: scriptpkg.ToggleDisabled,
			},
			wantLimit: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			normalizeImageToggleCoherence(&req)
			if req.MaxImageOverlays != tc.wantLimit {
				t.Fatalf("MaxImageOverlays = %d, want %d", req.MaxImageOverlays, tc.wantLimit)
			}
		})
	}
}
