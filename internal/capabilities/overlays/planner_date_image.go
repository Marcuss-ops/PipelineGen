package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import "strings"

// MoveImagesAsideForOverlappingDateCards keeps a centered date readable when
// its certified window overlaps an entity or editorial image, across scenes.
func MoveImagesAsideForOverlappingDateCards(items []capmodel.OverlayItem, canvasWidth int) {
	if canvasWidth <= 0 {
		canvasWidth = 1920
	}
	for i := range items {
		kind := strings.ToLower(strings.TrimSpace(items[i].Kind))
		if kind != "image" && kind != "entity_image" {
			continue
		}
		imageStart, imageEnd := OverlayItemWindowUS(items[i])
		if imageEnd <= imageStart {
			continue
		}
		for dateIndex := range items {
			date := &items[dateIndex]
			if strings.TrimSpace(date.TemplateID) != "TIMELINE_DATE_CARD" {
				continue
			}
			dateStart, dateEnd := OverlayItemWindowUS(*date)
			if dateEnd <= dateStart || imageStart >= dateEnd || dateStart >= imageEnd {
				continue
			}
			if items[i].Params == nil {
				items[i].Params = make(map[string]any)
			}
			imageWidth := overlayItemDimension(items[i].Params, "width", "box_width")
			if imageWidth <= 0 {
				imageWidth = float64(canvasWidth) * 0.68
			}
			// Image coordinates are centre-relative. Reserve the centre/right
			// area for the overlapping date, then dock the image inside the left
			// safe area; -720px pushed most images beyond the canvas.
			safeMargin := float64(canvasWidth) * 0.04
			maxWidth := float64(canvasWidth) * 0.38
			if imageWidth > maxWidth {
				imageWidth = maxWidth
				items[i].Params["width"] = imageWidth
				items[i].Params["box_width"] = imageWidth
			}
			items[i].Params["position_x"] = -float64(canvasWidth)/2 + safeMargin + imageWidth/2
			items[i].Params["position_y"] = float64(0)
			// A full-width date such as "17 dicembre 2009" still collides
			// with a left-docked portrait at the default 140px type size.
			// Reduce only the overlapping date card so both remain readable.
			if date.Params == nil {
				date.Params = make(map[string]any)
			}
			date.Params["font_size_px"] = float64(96)
			// Text positions are canvas-absolute; give the date a right-side
			// anchor so the image and its text do not occupy the same pixels.
			date.Params["position_x"] = float64(canvasWidth) * 0.70
			if style, ok := date.Params["style"].(map[string]any); ok {
				style["font_size"] = float64(96)
			}
			break
		}
	}
}

func overlayItemDimension(params map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch value := params[key].(type) {
		case float64:
			if value > 0 {
				return value
			}
		case int:
			if value > 0 {
				return float64(value)
			}
		}
	}
	return 0
}
