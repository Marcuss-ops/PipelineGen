// Package catalog — filters.go declares the read-only filter
// shapes used by catalog search.
//
// Per the July 2026 image-restructuring plan, catalog search
// accepts a filter that narrows the result set by territory
// (Origin × Provider), subject/slug match, style, etc. The
// filter is structural: callers can compose it like a builder,
// and the catalog engine translates it to the underlying repo
// query parameters.
package images

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ImageFilter is the canonical filter for catalog search.
// Composed by callers; can be partial (any field zero-value
// means "no constraint on that dimension").
type ImageFilter struct {
	SubjectID string
	Origins   []detail.ImageOrigin
	Tags      []string
	// Origines (territory) — empty = no origin constraint.
	// Multi-origin filters let callers ask for "retrieved OR
	// generated" in a single query.
	Origines []detail.ImageOrigin

	// Providers — empty = no provider constraint. Multi-provider
	// filters compose with Origines (AND).
	Providers []detail.ImageProvider

	// Slug is the EXACT subject slug to match. Empty = no slug
	// constraint. Pattern (Glob/Regex) search is NOT a catalog
	// surface — that's a retrieval-time activity.
	Slug string

	// SlugPrefix is a case-insensitive prefix match on subject
	// slug. Empty = no prefix constraint. Use for "all assets
	// whose subject starts with 'albert-ein'".
	SlugPrefix string

	// StyleID — empty = no style constraint. Multi-style filters
	// are OR'd (loose semantic) when StyleIDsAreAnd is false;
	// AND'd when true.
	StyleIDs       []string
	StyleIDsAreAnd bool

	// TagsContain — returns assets whose Tags include ALL of
	// the given strings (AND). Empty = no tag constraint.
	TagsContain []string

	// SearchText — optional fuzzy match over Description +
	// License + Author. Empty = no text-search constraint.
	SearchText string

	// LicenseRequired when true restricts to assets whose License
	// field is set (i.e. not Unknown). Used by legal-sensitive
	// surfaces (training-data licensing audits).
	LicenseRequired bool

	// Limit caps result size (0 = use default 50).
	Limit int

	// Offset for pagination. Compose with Cursor for stable
	// paginated UIs (Cursor outranks Offset when set).
	Offset int

	// Cursor is the opaque continuation token from a previous
	// CatalogSearchResult.NextCursor. When set, Offset is
	// ignored.
	Cursor string
}

// Matches returns a static analysis of whether this filter is
// "empty" (no constraints). Empty filters match all assets —
// callers should typically add at least one Origines or Slug
// constraint before listing the full catalog.
func (f ImageFilter) IsEmpty() bool {
	return len(f.Origines) == 0 &&
		len(f.Providers) == 0 &&
		f.Slug == "" &&
		f.SlugPrefix == "" &&
		len(f.StyleIDs) == 0 &&
		len(f.TagsContain) == 0 &&
		f.SearchText == "" &&
		!f.LicenseRequired
}

// DefaultLimit is the canonical filter result-set cap when the
// caller passed Limit=0.
const DefaultLimit = 50

// MaxLimit is the canonical upper bound. Callers asking for
// more are clamped by the catalog engine.
const MaxLimit = 500
const MaxListImagesLimit = MaxLimit

// EffectiveLimit returns the clamped limit: caller-supplied
// value when in range, DefaultLimit when 0, MaxLimit when above.
func (f ImageFilter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	if f.Limit > MaxLimit {
		return MaxLimit
	}
	return f.Limit
}

// Normalised is a defensive copy of the filter with normalised
// casing for Origines + Providers. The catalog engine consumes
// Normalised() so it doesn't have to repeat the case folding.
func (f ImageFilter) Normalised() ImageFilter {
	out := f
	if len(out.Origines) > 0 {
		out.Origines = make([]detail.ImageOrigin, len(out.Origines))
		for i, o := range out.Origines {
			out.Origines[i] = detail.ImageOrigin(strings.ToLower(string(o)))
		}
	}
	if len(out.Providers) > 0 {
		out.Providers = make([]detail.ImageProvider, len(out.Providers))
		for i, p := range out.Providers {
			out.Providers[i] = detail.ImageProvider(strings.ToLower(string(p)))
		}
	}
	if out.Slug != "" {
		out.Slug = strings.ToLower(strings.TrimSpace(out.Slug))
	}
	out.SlugPrefix = strings.ToLower(strings.TrimSpace(out.SlugPrefix))
	return out
}

// ── Builder-style constructors ──

// FilterByOrigin returns a filter that constrains exactly one
// territory. Optional providers list narrows further.
func FilterByOrigin(origin detail.ImageOrigin, providers ...detail.ImageProvider) ImageFilter {
	return ImageFilter{
		Origines:  []detail.ImageOrigin{origin},
		Providers: providers,
	}
}

// FilterBySlug returns a filter that constrains exactly one
// subject slug.
func FilterBySlug(slug string) ImageFilter {
	return ImageFilter{Slug: slug}
}

// FilterByStyle returns a filter that constrains StyleID.
func FilterByStyle(styleID string, providers ...detail.ImageProvider) ImageFilter {
	return ImageFilter{
		StyleIDs:  []string{styleID},
		Providers: providers,
	}
}

func ResolvedLimit(l int) int {
	if l <= 0 {
		return DefaultLimit
	}
	if l > MaxListImagesLimit {
		return MaxListImagesLimit
	}
	return l
}
