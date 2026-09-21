package script

// mergeSubtitlePreset fills an inline subtitle style from a named preset while
// preserving every field the caller explicitly supplied. The old replacement
// behaviour silently discarded inline stroke/shadow/position overrides when a
// payload also selected a preset.
func mergeSubtitlePreset(inline *VideoVisualStyleSpec, preset VideoVisualStyleSpec) VideoVisualStyleSpec {
	out := preset
	if inline == nil {
		return out
	}
	if inline.Font != "" {
		out.Font = inline.Font
	}
	if inline.Position != "" {
		out.Position = inline.Position
	}
	if inline.Size != 0 {
		out.Size = inline.Size
		if inline.FontSizePX == 0 {
			out.FontSizePX = 0
		}
	}
	if inline.Color != "" {
		out.Color = inline.Color
	}
	if inline.FontSizePX != 0 {
		out.FontSizePX = inline.FontSizePX
	}
	if inline.WidthPX != 0 {
		out.WidthPX = inline.WidthPX
	}
	if inline.HeightPX != 0 {
		out.HeightPX = inline.HeightPX
	}
	if inline.ScalePercent != 0 {
		out.ScalePercent = inline.ScalePercent
	}
	if inline.Stroke != nil {
		stroke := *inline.Stroke
		out.Stroke = &stroke
	}
	if inline.Shadow != nil {
		shadow := *inline.Shadow
		out.Shadow = &shadow
	}
	if inline.TransitionIn != nil {
		transition := *inline.TransitionIn
		out.TransitionIn = &transition
	}
	return out
}
