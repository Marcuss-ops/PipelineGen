package overlays

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestValidateEditorialBulletStylesAcceptsSupportedPins pins the accepted
// shapes: an omitted block, a scene-only pin that keeps every default, a full
// pin with a bundled font and a certified bullet motion, and the runtime's
// default family spelled explicitly.
func TestValidateEditorialBulletStylesAcceptsSupportedPins(t *testing.T) {
	cases := [][]scriptpkg.EditorialBulletStyle{
		nil,
		{{SceneID: "scene-1"}},
		{{SceneID: "scene-1", FontFamily: "oswald", FontSizePX: 96, MotionID: "air_rise_type_on"}},
		{{SceneID: "scene-1", FontFamily: "playfair_display_italic"}},
		{
			{SceneID: "scene-1", FontFamily: "inter"},
			{SceneID: "scene-2", FontSizePX: EditorialBulletFontSizeMin},
			{SceneID: "scene-3", FontSizePX: EditorialBulletFontSizeMax},
		},
	}
	for _, styles := range cases {
		if err := ValidateEditorialBulletStyles(styles); err != nil {
			t.Fatalf("styles %+v should validate, got %v", styles, err)
		}
	}
}

// TestValidateEditorialBulletStylesFailsClosed pins every rejection the payload
// can produce: a missing/duplicated identity, a font the runtime does not
// bundle, a size outside the readable range, a motion the registry does not
// certify for bullets, and an oversized block.
func TestValidateEditorialBulletStylesFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		styles []scriptpkg.EditorialBulletStyle
		want   string
	}{
		{
			"missing scene",
			[]scriptpkg.EditorialBulletStyle{{FontFamily: "inter"}},
			"scene_id is required",
		},
		{
			"blank scene",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "   "}},
			"scene_id is required",
		},
		{
			"duplicate scene",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "scene-1"}, {SceneID: " scene-1 "}},
			"duplicated",
		},
		{
			"unknown font",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "scene-1", FontFamily: "comic_sans"}},
			"font_family \"comic_sans\" is unsupported",
		},
		{
			"size below range",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "scene-1", FontSizePX: 8}},
			"font_size_px must be in",
		},
		{
			"size above range",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "scene-1", FontSizePX: 400}},
			"font_size_px must be in",
		},
		{
			"unknown motion",
			[]scriptpkg.EditorialBulletStyle{{SceneID: "scene-1", MotionID: "no_such_motion"}},
			"motion_id \"no_such_motion\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEditorialBulletStyles(tc.styles)
			if err == nil {
				t.Fatalf("%s should fail closed", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: error = %q, want it to contain %q", tc.name, err, tc.want)
			}
		})
	}
	many := make([]scriptpkg.EditorialBulletStyle, MaxEditorialBulletStyles+1)
	for i := range many {
		many[i] = scriptpkg.EditorialBulletStyle{SceneID: "scene-" + strings.Repeat("x", i+1)}
	}
	if err := ValidateEditorialBulletStyles(many); err == nil || !strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("oversized block: got %v", err)
	}
}
