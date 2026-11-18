// Package script — editorial_bullet_style.go owns the per-bullet presentation
// pin of the script.generate payload.
//
// The overlay planner projects at most ONE editorial bullet per scene (admission
// drops any further bullet), so an entry keyed by scene addresses exactly one
// bullet. The payload therefore selects the font and the entrance motion of
// individual bullets without needing an item id: the scene is the identity.
//
// The field is presentation, not script text: like phrase_motions and
// motion_seed it is deliberately absent from the generation fingerprint, which
// pins the model output and the editorial decisions, not the look of an
// already-decided bullet.
package script

// EditorialBulletStyle pins the presentation of one projected editorial bullet.
//
// All fields are optional except SceneID. An empty FontFamily keeps the
// presentation default (the documentary runtime family), an empty MotionID
// keeps the automatic rotation, and a zero FontSizePX keeps the size the
// presentation pass derives from the bullet length.
type EditorialBulletStyle struct {
	// SceneID is the scene whose bullet this entry styles. It must name a
	// scene in the same request; the compiler rejects an unknown scene
	// instead of dropping the entry silently.
	SceneID string `json:"scene_id"`
	// FontFamily is one of SupportedFontFamilies, or empty for the preset.
	FontFamily string `json:"font_family,omitempty"`
	// FontSizePX overrides the presentation size for this bullet, in pixels.
	// Zero keeps the derived size. The accepted range is owned by the
	// capabilities/overlays validator (EditorialBulletFontSizeMin/Max).
	FontSizePX float64 `json:"font_size_px,omitempty"`
	// MotionID pins the bullet's entrance motion. It must be a certified,
	// renderer-supported phrase/bullet motion; an unknown or retired id fails
	// closed at the compile boundary rather than being silently replaced.
	MotionID string `json:"motion_id,omitempty"`
}
