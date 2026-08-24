package filesystem

import (
	"strings"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// SafeFolderName replaces filesystem-unsafe characters with spaces.
// Note: the behavior has been unified with textutil.SafeName which now
// also converts hyphens, underscores, and dots to spaces.
func SafeFolderName(name string) string {
	return textutil.SafeName(name)
}

// ExtractStyleFromPath extracts the style segment from a relative image path.
// Paths follow the pattern: images/downloaded/{source}/{style}/{subStyle}/{genID}/{hash}.ext
// or: images/generated/{style}/{subStyle}/{genID}/{hash}.ext
func ExtractStyleFromPath(pathRel string) string {
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
		if len(parts) >= 3 {
			return parts[2]
		}
	}
	return ""
}
