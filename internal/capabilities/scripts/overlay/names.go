// Package overlay owns overlay-specific policy used by the scripts capability.
//
// names.go owns the review-name derivation moved here from the root scripts
// package (2026-10-10): the root package is the compatibility facade and each
// overlay family migrates into this subpackage
// (architecture/package_hotspots.json::internal/capabilities/scripts targets).
// The entry point is exported because its caller still lives in the facade;
// the two helpers stay package-local — nothing outside this file owns them.
package overlay

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// NameItemsForReview makes the chosen motion, subject, and overlay
// category visible in every render artifact name. This lets operators spot a
// repeated or unexpected animation directly from a job's overlay list.
func NameItemsForReview(items []capabilityoverlay.OverlayItem) {
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
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(value))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
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
