package media

// SlotKind identifies the visual slot a layer may occupy in a scene.
// It is a closed set; new values require a code change.
type SlotKind string

const (
	SlotPrimaryVideo    SlotKind = "primary_video"
	SlotSecondaryImage  SlotKind = "secondary_image"
	SlotEvidenceOverlay SlotKind = "evidence_overlay"
	SlotMap             SlotKind = "map"
	SlotPortrait        SlotKind = "portrait"
	SlotDocument        SlotKind = "document"
	SlotBackground      SlotKind = "background"
)

// IsKnownSlotKind reports whether k is a supported slot kind.
func IsKnownSlotKind(k SlotKind) bool {
	switch k {
	case SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground:
		return true
	}
	return false
}

// AllowedMediaTypes returns the canonical media types that may
// occupy the given slot. The returned slice is owned by the
// function and must not be mutated by callers.
func AllowedMediaTypes(k SlotKind) []string {
	switch k {
	case SlotPrimaryVideo:
		return []string{"video"}
	case SlotSecondaryImage, SlotPortrait:
		return []string{"image"}
	case SlotMap:
		return []string{"image", "map"}
	case SlotDocument:
		return []string{"document", "image"}
	case SlotEvidenceOverlay, SlotBackground:
		return []string{"video", "image"}
	default:
		return nil
	}
}

// DefaultLayout returns the canonical default layout for a slot.
func DefaultLayout(k SlotKind) string {
	switch k {
	case SlotPrimaryVideo, SlotEvidenceOverlay:
		return "fullscreen"
	case SlotSecondaryImage, SlotPortrait:
		return "right_panel"
	case SlotMap:
		return "fullscreen_fade"
	case SlotDocument:
		return "overlay"
	case SlotBackground:
		return "background"
	default:
		return ""
	}
}

// IsMediaTypeAllowed reports whether the given media type is
// compatible with the slot. It is the canonical SSOT for the
// slot ↔ media type mapping used by the planner, ranker, and
// renderer.
func IsMediaTypeAllowed(k SlotKind, mediaType string) bool {
	if mediaType == "" {
		return false
	}
	for _, t := range AllowedMediaTypes(k) {
		if t == mediaType {
			return true
		}
	}
	return false
}

// MaxLayers returns the maximum number of layers supported for a
// single scene of the given slot kind. Most slots accept one layer.
func MaxLayers(k SlotKind) int {
	switch k {
	case SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground:
		return 1
	default:
		return 0
	}
}

// SupportsTimeRange reports whether a slot typically supports a
// temporal window (start_ms / end_ms).
func SupportsTimeRange(k SlotKind) bool {
	switch k {
	case SlotPrimaryVideo, SlotEvidenceOverlay, SlotBackground:
		return true
	case SlotSecondaryImage, SlotMap, SlotPortrait, SlotDocument:
		return false
	default:
		return false
	}
}
