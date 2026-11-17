package script

import (
	"fmt"
	"math"
	"strings"
)

// OverlayImageAsset binds a preselected, locally available still to one scene
// for the overlay planner. It is intentionally separate from video stock
// bindings and provider media selection.
type OverlayImageAsset struct {
	SceneID   string `json:"scene_id"`
	AssetID   string `json:"asset_id"`
	LocalPath string `json:"local_path"`
	SHA256    string `json:"sha256"`
	MIMEType  string `json:"mime_type,omitempty"`
	// SoundEffect optionally pins one explicit cue to the image this pin
	// produces for its scene. It replaces the deterministic random cue for
	// that image while leaving every other image on random selection. The
	// asset_id is resolved by the same audio resolver audio.sound_effects
	// uses, so a payload never carries a filesystem path.
	SoundEffect *OverlayImageSoundEffect `json:"sound_effect,omitempty"`
	// Style optionally overrides the run-level overlay_style.image for the
	// item this pin produces. It is the same published image style block
	// (border, shadow, glow, effects, grid, backdrop), and every field the
	// pin leaves unset keeps the run-level value: a pin names only what is
	// different about its own photo.
	Style *OverlayImageStyleSpec `json:"style,omitempty"`
}

// OverlayImageSoundEffect is the per-image cue declaration. DurationMS is
// optional: omitted keeps the certified 250 ms editorial cue; a value longer
// than the image's visible window is clamped by the planner.
type OverlayImageSoundEffect struct {
	AssetID    string   `json:"asset_id"`
	GainDB     *float64 `json:"gain_db,omitempty"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
}

// Validate checks the cue before it reaches the audio asset resolver. The
// renderer repeats these bounds at its own trust boundary.
func (s *OverlayImageSoundEffect) Validate() error {
	if s == nil {
		return nil
	}
	if strings.TrimSpace(s.AssetID) == "" {
		return fmt.Errorf("sound_effect.asset_id is required")
	}
	if s.GainDB != nil {
		if math.IsNaN(*s.GainDB) || math.IsInf(*s.GainDB, 0) || *s.GainDB < -96 || *s.GainDB > 24 {
			return fmt.Errorf("sound_effect.gain_db must be between -96 and 24")
		}
	}
	if s.DurationMS != nil && (*s.DurationMS <= 0 || *s.DurationMS > 60_000) {
		return fmt.Errorf("sound_effect.duration_ms must be greater than 0 and at most 60000")
	}
	return nil
}
