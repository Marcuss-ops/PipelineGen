package overlays

import "strings"

// MoveImagesAsideForOverlappingDateCards keeps a centered date readable when
// its certified window overlaps an entity or editorial image, across scenes.
func MoveImagesAsideForOverlappingDateCards(items []OverlayItem) {
	for i := range items {
		kind := strings.ToLower(strings.TrimSpace(items[i].Kind))
		if kind != "image" && kind != "entity_image" {
			continue
		}
		imageStart, imageEnd := overlayItemWindowUS(items[i])
		if imageEnd <= imageStart {
			continue
		}
		for _, date := range items {
			if strings.TrimSpace(date.TemplateID) != "TIMELINE_DATE_CARD" {
				continue
			}
			dateStart, dateEnd := overlayItemWindowUS(date)
			if dateEnd <= dateStart || imageStart >= dateEnd || dateStart >= imageEnd {
				continue
			}
			if items[i].Params == nil {
				items[i].Params = make(map[string]any)
			}
			items[i].Params["position_x"] = float64(-720)
			items[i].Params["position_y"] = float64(0)
			break
		}
	}
}
