package scriptgeneration

import (
	"fmt"
	"strings"
	"unicode"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// nameOverlayItemsForReview makes the chosen motion, subject, and overlay
// category visible in every render artifact name. This lets operators spot a
// repeated or unexpected animation directly from a job's overlay list.
func nameOverlayItemsForReview(items []capabilityoverlay.OverlayItem) {
	seen := make(map[string]int, len(items))
	for i := range items {
		item := &items[i]
		motion := strings.TrimSpace(item.MotionID)
		if item.Map != nil && strings.TrimSpace(item.Map.MotionID) != "" {
			motion = strings.TrimSpace(item.Map.MotionID)
		}
		if motion == "" {
			motion = "static"
		}
		entity := ""
		if item.EntityRef != nil {
			entity = item.EntityRef.Name
		}
		if entity == "" && item.Map != nil && len(item.Map.Pins) > 0 {
			entity = item.Map.Pins[0].Label
		}
		if entity == "" {
			entity = item.Text
		}
		if entity == "" {
			entity = item.EntityCaption
		}
		if entity == "" {
			entity = item.ID
		}
		category := overlayNameCategory(item.Kind, motion)
		base := strings.Join([]string{overlayNameSlug(motion), overlayNameSlug(entity), category}, "__")
		seen[base]++
		item.ID = fmt.Sprintf("%s__%02d", base, seen[base])
	}
}

func overlayNameCategory(kind, motion string) string {
	switch kind {
	case "map":
		return "map"
	case "text_phrase":
		if strings.HasPrefix(motion, "short_phrase_") {
			return "short_phrase"
		}
		return "phrase"
	case "number":
		return "date_number"
	case "entity_image", "entity_card":
		return "entity_image"
	case "image":
		return "image"
	case "quote":
		return "quote"
	case "brand_text":
		return "brand_text"
	default:
		return overlayNameSlug(kind)
	}
}

func overlayNameSlug(value string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			separator = false
		} else {
			separator = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 48 {
		result = strings.TrimRight(result[:48], "-")
	}
	if result == "" {
		return "unnamed"
	}
	return result
}
