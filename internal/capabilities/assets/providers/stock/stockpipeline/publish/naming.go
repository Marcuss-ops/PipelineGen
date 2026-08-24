// Package publish owns pure Drive naming rules for stock artifacts.
// It deliberately depends only on stock input DTOs and domain naming helpers;
// it does not depend on StepRunner, orchestration state, or infrastructure.
package publish

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	stocktypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/types"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// RootFolderName derives the human-readable Drive top-level folder name.
func RootFolderName(in *stocktypes.RunInput) string {
	if in == nil {
		return "stock"
	}
	if name := SanitizedRootName(in.FolderName); name != "" {
		return name
	}
	if name := SanitizedRootName(in.Subfolder); name != "" {
		return name
	}
	if name := domaindelivery.FirstSanitizedQuery(in.SearchQueries); name != "" {
		return name
	}
	if name := domaindelivery.FirstSanitizedURLBasename(in.DirectURLs); name != "" {
		return name
	}
	return "stock_" + time.Now().UTC().Format("2006-01-02")
}

// ResolvedFolderID returns a Drive folder ID only when it has been verified.
// FolderID is a workflow identifier and is never treated as a Drive folder.
func ResolvedFolderID(in *stocktypes.RunInput) string {
	if in == nil || !in.DriveFolderResolved {
		return ""
	}
	return strings.TrimSpace(in.DriveFolderID)
}

// SanitizedRootName trims and sanitizes an operator-provided folder name.
func SanitizedRootName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return pathutil.SafeFolderName(s)
}

// LegacyQuery returns the first non-empty sanitized search query.
func LegacyQuery(queries []string) string {
	for _, q := range queries {
		if name := SanitizedRootName(q); name != "" {
			return name
		}
	}
	return ""
}

// LegacyURLBasename returns the first non-empty sanitized URL basename.
func LegacyURLBasename(urls []string) string {
	for _, raw := range urls {
		if name := SanitizedURLBasename(raw); name != "" {
			return name
		}
	}
	return ""
}

// SanitizedURLBasename strips query/fragment and extension before sanitizing.
func SanitizedURLBasename(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if parsed, err := url.Parse(s); err == nil && parsed.Path != "" {
		s = parsed.Path
	}
	base := filepath.Base(s)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return SanitizedRootName(base)
}

// TimestampGroupName derives the shared Drive leaf for legacy runs.
func TimestampGroupName(in *stocktypes.RunInput) string {
	if in == nil {
		return "metadata"
	}
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if base := filepath.Base(filepath.Clean(sub)); base != "" && base != "." && base != string(filepath.Separator) {
			if name := SanitizedRootName(base); name != "" {
				return name
			}
		}
	}
	if name := SanitizedRootName(in.FolderName); name != "" {
		return name
	}
	return "metadata"
}

// ClipFolderName derives the Drive subfolder for an explicit clip.
func ClipFolderName(in *stocktypes.RunInput, plan stocktypes.ClipPlan, fallback string) string {
	if in != nil && strings.TrimSpace(in.Subfolder) != "" {
		return fallback
	}
	if plan.Round > 0 {
		return fmt.Sprintf("Round %d", plan.Round)
	}
	if title := SanitizedRootName(plan.Title); title != "" {
		return title
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return TimestampGroupName(in)
}

// TimestampParentGroupName derives the shared parent leaf for explicit clips.
func TimestampParentGroupName(in *stocktypes.RunInput) string {
	if in == nil {
		return TimestampGroupName(in)
	}
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if parent := filepath.Base(filepath.Dir(filepath.Clean(sub))); parent != "" && parent != "." && parent != string(filepath.Separator) {
			if name := SanitizedRootName(parent); name != "" {
				return name
			}
		}
	}
	return TimestampGroupName(in)
}

// PerClipLeafName derives the canonical explicit-clip leaf.
func PerClipLeafName(plan stocktypes.ClipPlan) string {
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := SanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		slugged := SlugifyTitle(title)
		if slugged != "" && slugged != "untitled" {
			return slugged
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60,
		int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60,
	)
}

// TimestampParentLeafName derives the leaf for an expanded timestamp block.
func TimestampParentLeafName(plan stocktypes.ClipPlan) string {
	if raw := strings.TrimSpace(plan.ParentSlug); raw != "" {
		if safe := SanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		slugged := SlugifyTitle(title)
		if slugged != "" && slugged != "untitled" {
			return slugged
		}
	}
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := SanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60,
		int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60,
	)
}

// SlugifyTitle delegates to the canonical title slug implementation.
func SlugifyTitle(title string) string { return slug.SlugifyTitle(title) }
