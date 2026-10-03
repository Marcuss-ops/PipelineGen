package overlays

import "strings"

func numberOverlayItem(planID, sceneID string, number TimedAnnotation) OverlayItem {
	id := itemID(sceneID, "number", number.Text)
	templateID, motionID := "NUMBER", SelectTextMotion(planID, sceneID, id)
	if numberType := strings.TrimSpace(number.Type); numberType != "" {
		if presentationTemplate, presentationMotion := NumberPresentationForEntityType(planID, sceneID, id, numberType); presentationTemplate != "" {
			templateID, motionID = presentationTemplate, presentationMotion
		}
	}
	params := map[string]any{"position": "center", "style": "stat", "priority": number.Score}
	if templateID == "TIMELINE_DATE_CARD" {
		number = minimumDatePresentationDuration(number)
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	} else if templateID == "METRIC_STAT_CARD" {
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	}
	return OverlayItem{
		ID: id, SceneID: sceneID, PresetID: selectWordPreset(planID, sceneID, id),
		MotionID: motionID, Kind: "number", TemplateID: templateID, Text: number.Text,
		StartMs: number.StartMs, EndMs: number.EndMs, StartUS: number.StartUS, DurationUS: number.DurationUS,
		Params: params,
	}
}

// minimumDatePresentationDuration preserves the word-aligned start while
// keeping a Date card on screen for at least five seconds.
func minimumDatePresentationDuration(date TimedAnnotation) TimedAnnotation {
	const minimumDurationUS int64 = 5_000_000
	if date.DurationUS <= 0 {
		date.StartUS = date.StartMs * 1000
		date.DurationUS = (date.EndMs - date.StartMs) * 1000
	}
	if date.DurationUS < minimumDurationUS {
		date.DurationUS = minimumDurationUS
	}
	date.StartMs = date.StartUS / 1000
	date.EndMs = (date.StartUS + date.DurationUS + 999) / 1000
	return date
}
