package images

import "strings"

// extractStyleFromPath extracts the style segment from a relative image path.
// Paths follow images/downloaded/{source}/{style}/... or images/generated/{style}/....
func extractStyleFromPath(pathRel string) string {
	normalized := strings.ReplaceAll(pathRel, "\\", "/")
	parts := strings.Split(normalized, "/")
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "downloaded":
		if len(parts) >= 4 {
			return parts[3]
		}
	case "generated":
		return parts[2]
	}
	return ""
}
