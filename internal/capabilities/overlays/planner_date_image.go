package overlays

import capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

import "strings"

// MoveImagesAsideForOverlappingDateCards keeps a centered date readable when
// its certified window overlaps an entity or editorial image, across scenes.
// Both elements stay on the canvas centerline: the image uses the upper lane
// and the date uses the lower lane instead of splitting the composition left
// and right.
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
			// Keep the photo horizontally centered and place it above the date.
			// A side-by-side treatment made photos tiny and left most of the
			// white-paper canvas empty, especially in standalone overlay previews.
			canvasHeight := float64(canvasWidth) * 9.0 / 16.0
			maxWidth := float64(canvasWidth) * 0.82
			maxHeight := canvasHeight * 0.58
			if imageWidth > maxWidth {
				imageWidth = maxWidth
				items[i].Params["width"] = imageWidth
				items[i].Params["box_width"] = imageWidth
			}
			if imageHeight := overlayItemDimension(items[i].Params, "height", "box_height"); imageHeight > maxHeight {
				items[i].Params["height"] = maxHeight
				items[i].Params["box_height"] = maxHeight
			}
			items[i].Params["position_x"] = float64(0)
			items[i].Params["position_y"] = -canvasHeight * 0.10
			// Put the date in a lower centered lane. Keep its text centered
			// instead of anchoring it at 70% of the canvas width.
			if date.Params == nil {
				date.Params = make(map[string]any)
			}
			date.Params["font_size_px"] = float64(96)
			date.Params["position_x"] = float64(canvasWidth) / 2
			date.Params["position_y"] = canvasHeight * 0.86
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
