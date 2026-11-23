package publish

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// ─── Legacy naming entry points ──────────────────────────────────────────────

func namingInput(in *core.RunInput) NamingInput {
	if in == nil {
		return NamingInput{}
	}
	return NamingInput{FolderName: in.FolderName, Subfolder: in.Subfolder, SearchQueries: in.SearchQueries, DirectURLs: in.DirectURLs, DriveFolderID: in.DriveFolderID, DriveFolderResolved: in.DriveFolderResolved}
}

func RunInputRootFolderName(in *core.RunInput) string {
	if in == nil {
		return "stock"
	}
	// SanitizedRootName maps empty/whitespace input to pathutil's
	// "untitled" sentinel, so it must not short-circuit the fallback
	// cascade here. Only a genuinely non-empty FolderName (after trim)
	// that sanitizes to a real name wins; otherwise we fall through to
	// Subfolder, the legacy query/URL chain, and finally the UTC date
	// fallback owned by the publish capability package.
	if name := SanitizedRootName(in.FolderName); name != "" && name != "untitled" {
		return name
	}
	if name := strings.TrimSpace(in.Subfolder); name != "" {
		return name
	}
	if name := firstLegacyQuery(in.SearchQueries); name != "" {
		return name
	}
	if name := firstLegacyURL(in.DirectURLs); name != "" {
		return name
	}
	return RootFolderName(namingInput(in))
}
func RunInputResolvedFolderID(in *core.RunInput) string {
	return ResolvedFolderID(namingInput(in))
}
func SanitizedRootName(s string) string { return pathutil.SafeFolderName(strings.TrimSpace(s)) }
func SanitizedURLBasename(raw string) string {
	s := strings.TrimSpace(raw)
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
	return sanitizeURLSegment(strings.TrimSuffix(base, filepath.Ext(base)))
}

func sanitizeURLSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
func RunInputTimestampGroupName(in *core.RunInput) string {
	return TimestampGroupName(namingInput(in))
}
func RunInputClipFolderName(in *core.RunInput, plan core.ClipPlan, fallback string) string {
	return ClipFolderName(namingInput(in), publishClipNamingInput(plan), fallback)
}
func RunInputTimestampParentGroupName(in *core.RunInput) string {
	return TimestampParentGroupName(namingInput(in))
}
func PerClipLeafName(plan core.ClipPlan) string {
	if raw := strings.TrimSpace(plan.Slug); raw != "" {
		if safe := SanitizedRootName(raw); safe != "" && safe != "untitled" && domaindelivery.ContainsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		if slugged := SlugifyTitle(title); slugged != "" && slugged != "untitled" {
			return slugged
		}
	}
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d", int(plan.StartSec)/3600, (int(plan.StartSec)%3600)/60, int(plan.StartSec)%60, int(plan.EndSec)/3600, (int(plan.EndSec)%3600)/60, int(plan.EndSec)%60)
}
func firstLegacyQuery(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return pathutil.SafeFolderName(strings.TrimSpace(value))
		}
	}
	return ""
}
func firstLegacyURL(values []string) string {
	for _, value := range values {
		if name := SanitizedURLBasename(value); name != "" && name != "untitled" {
			return name
		}
	}
	return ""
}

// ─── publish bridge ──────────────────────────────────────────────────────────

func publishClipNamingInput(plan core.ClipPlan) ClipNamingInput {
	return ClipNamingInput{
		Round: plan.Round, Title: plan.Title, Slug: plan.Slug, ParentSlug: plan.ParentSlug,
		StartSec: plan.StartSec, EndSec: plan.EndSec,
	}
}
