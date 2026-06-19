package files

import (
	"fmt"
	"strings"
	"time"

	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// SafeFolderName replaces filesystem-unsafe characters with spaces.
// Note: the behavior has been unified with textutil.SafeName which now
// also converts hyphens, underscores, and dots to spaces.
func SafeFolderName(name string) string {
	return textutil.SafeName(name)
}

// BuildTimestampedSlug creates a timestamped slug from a name and time.
func BuildTimestampedSlug(name string, t time.Time) string {
	slug := textutil.Slugify(name)
	if slug == "" {
		slug = "generated-script"
	}
	return fmt.Sprintf("%s_%s", t.Format("20060102_150405"), slug)
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
