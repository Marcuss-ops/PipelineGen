// Package publish owns neutral contracts for stock artifact publication.
package publish

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// Chunk is the neutral publish projection for a video chunk.
type Chunk struct {
	Index                                               int
	ArtifactID, Filename, LocalPath                     string
	SizeBytes                                           int64
	SHA256, Description, RootFolder, FolderID, PathLeaf string
	FolderResolved                                      bool
}

// Metadata is the neutral publish projection for run metadata.
type Metadata struct {
	ArtifactID, Filename, LocalPath        string
	SizeBytes                              int64
	SHA256, RootFolder, FolderID, PathLeaf string
	FolderResolved                         bool
}

// NamingInput contains only values needed by the naming policy.
type NamingInput struct {
	FolderName, Subfolder     string
	SearchQueries, DirectURLs []string
	DriveFolderID             string
	DriveFolderResolved       bool
}

// ClipNamingInput contains explicit clip naming fields.
type ClipNamingInput struct {
	Round                   int
	Title, Slug, ParentSlug string
	StartSec, EndSec        float64
}

// RootFolderName applies the stock fallback chain.
func RootFolderName(in NamingInput) string {
	if name := sanitized(in.FolderName); name != "" {
		return name
	}
	if name := strings.TrimSpace(in.Subfolder); name != "" {
		return name
	}
	for _, query := range in.SearchQueries {
		if name := strings.TrimSpace(query); name != "" {
			return name
		}
	}
	for _, raw := range in.DirectURLs {
		if name := URLBasename(raw); name != "" {
			return name
		}
	}
	return "stock_" + time.Now().UTC().Format("2006-01-02")
}

// ResolvedFolderID returns only a verified Drive folder ID.
func ResolvedFolderID(in NamingInput) string {
	if !in.DriveFolderResolved {
		return ""
	}
	return strings.TrimSpace(in.DriveFolderID)
}

// TimestampGroupName derives the shared timestamp leaf.
func TimestampGroupName(in NamingInput) string {
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if name := sanitized(filepath.Base(filepath.Clean(sub))); name != "" && name != "." {
			return name
		}
	}
	if name := sanitized(in.FolderName); name != "" {
		return name
	}
	return "metadata"
}

// TimestampParentGroupName derives the parent of an explicit timestamp subfolder.
func TimestampParentGroupName(in NamingInput) string {
	if sub := strings.TrimSpace(in.Subfolder); sub != "" {
		if name := sanitized(filepath.Base(filepath.Dir(filepath.Clean(sub)))); name != "" && name != "." {
			return name
		}
	}
	return TimestampGroupName(in)
}

// ClipFolderName derives the shared folder for an explicit clip.
func ClipFolderName(in NamingInput, clip ClipNamingInput, fallback string) string {
	if strings.TrimSpace(in.Subfolder) != "" {
		return fallback
	}
	if clip.Round > 0 {
		return fmt.Sprintf("Round %d", clip.Round)
	}
	if name := sanitized(clip.Title); name != "" {
		return name
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return TimestampGroupName(in)
}

// PerClipLeafName derives a stable explicit-clip leaf. Operator slugs use
// filesystem-safe names; titles use the canonical title slug.
func PerClipLeafName(clip ClipNamingInput) string {
	if raw := strings.TrimSpace(clip.Slug); raw != "" {
		if safe := pathutil.SafeFolderName(raw); safe != "" && safe != "untitled" && containsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(clip.Title); title != "" {
		if safe := SlugifyTitle(title); safe != "" && safe != "untitled" {
			return safe
		}
	}
	return timestampLeaf(clip.StartSec, clip.EndSec)
}

// TimestampParentLeafName derives the parent leaf for expanded timestamp clips.
func TimestampParentLeafName(clip ClipNamingInput) string {
	if raw := strings.TrimSpace(clip.ParentSlug); raw != "" {
		if safe := pathutil.SafeFolderName(raw); safe != "" && safe != "untitled" && containsAlphanumeric(safe) {
			return safe
		}
	}
	if title := strings.TrimSpace(clip.Title); title != "" {
		if safe := SlugifyTitle(title); safe != "" && safe != "untitled" {
			return safe
		}
	}
	if raw := strings.TrimSpace(clip.Slug); raw != "" {
		if safe := pathutil.SafeFolderName(raw); safe != "" && safe != "untitled" && containsAlphanumeric(safe) {
			return safe
		}
	}
	return timestampLeaf(clip.StartSec, clip.EndSec)
}

// SlugifyTitle delegates to the repository-wide canonical title slug.
func SlugifyTitle(title string) string { return slug.SlugifyTitle(title) }

// SafeName is the neutral trim-only naming primitive; empty stays empty.
func SafeName(value string) string { return strings.TrimSpace(value) }

// URLBasename strips URL query/fragment and extension, then sanitizes it.
func URLBasename(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		raw = parsed.Path
	}
	base := filepath.Base(raw)
	return sanitized(strings.TrimSuffix(base, filepath.Ext(base)))
}

func sanitized(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return pathutil.SafeFolderName(value)
}
func containsAlphanumeric(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
func timestampLeaf(start, end float64) string {
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d", int(start)/3600, (int(start)%3600)/60, int(start)%60, int(end)/3600, (int(end)%3600)/60, int(end)%60)
}
