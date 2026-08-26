// Package catalog — search.go declares the application-layer
// read-only search surface.
//
// Per the July 2026 image-restructuring plan, catalog.* is
// read-only: a thin layer over the SQLite image-assets table
// that returns CatalogSearchResult values. It does NOT perform
// ingest, generation, or retrieval — those are delegated to the
// retrieved/ and generated/ subpackages.
//
// This file declares:
//
//   - CatalogSearch — the search interface application-layer
//     consumers depend on.
//   - InMemoryCatalogSearch — a small testing-friendly impl that
//     accepts a pre-built slice of assets (no SQLite).
//   - Compile-time interface assertion pinning the in-memory
//     impl as a CatalogSearch.
//
// Step 9 (July 2026): full SQLite-backed implementation lives
// in the parent images package (ImageStorageService is the
// canonical home for the production backend); the in-memory
// variant here covers tests + read-only unit-test pipelines.
package images

import (
	"context"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// CatalogSearch is the read-only search surface. Handlers
// depend on this interface; composition roots inject an
// implementation (InMemoryCatalogSearch for tests, or a
// SQLite-backed impl for production).
type CatalogSearch interface {
	// Search returns the assets matching `filter`. Pagination
	// metadata (TotalCount, NextCursor) is in the result.
	Search(ctx context.Context, filter ImageFilter) (CatalogSearchResult, error)

	// CountByOrigin returns how many registered assets belong
	// to each territory. Used by the /api/system/doctor and
	// admin dashboards.
	CountByOrigin(ctx context.Context) (map[detail.ImageOrigin]int, error)
}

// Compile-time assertion: *InMemoryCatalogSearch satisfies the
// CatalogSearch interface. Drift between the interface and the
// impl surfaces at build time.
var _ CatalogSearch = (*InMemoryCatalogSearch)(nil)

// InMemoryCatalogSearch is the minimal in-memory implementation
// of CatalogSearch. Suitable for tests + read-only unit-test
// pipelines. NOT production — production uses the
// SQLite-backed surface wired in service.go's NewService
// composition root.
type InMemoryCatalogSearch struct {
	assets []detail.ImageAsset
}

// NewInMemoryCatalogSearch constructs an InMemoryCatalogSearch
// from a fixed asset slice.
func NewInMemoryCatalogSearch(assets []detail.ImageAsset) *InMemoryCatalogSearch {
	return &InMemoryCatalogSearch{assets: assets}
}

// Search applies ImageFilter against the in-memory slice and
// returns CatalogSearchResult. Pagination is naive slice
// slicing; cursor encoding is "offset:N" for stability.
func (i *InMemoryCatalogSearch) Search(_ context.Context, filter ImageFilter) (CatalogSearchResult, error) {
	if i == nil {
		return CatalogSearchResult{}, nil
	}
	filtered := make([]detail.ImageAsset, 0, len(i.assets))
	for _, a := range i.assets {
		if matchesFilter(a, filter) {
			filtered = append(filtered, a)
		}
	}
	total := len(filtered)
	limit := filter.EffectiveLimit()
	offset := filter.Offset
	if filter.Cursor != "" {
		offset = parseCursor(filter.Cursor)
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if offset >= total {
		return CatalogSearchResult{
			TotalCount: total,
			FilterEcho: filter.Normalised(),
			NextCursor: "",
		}, nil
	}
	page := filtered[offset:end]
	nextCursor := ""
	if end < total {
		nextCursor = formatCursor(end)
	}
	return CatalogSearchResult{
		Assets:     page,
		TotalCount: total,
		NextCursor: nextCursor,
		FilterEcho: filter.Normalised(),
	}, nil
}

// CountByOrigin returns the per-territory counts.
func (i *InMemoryCatalogSearch) CountByOrigin(_ context.Context) (map[detail.ImageOrigin]int, error) {
	out := make(map[detail.ImageOrigin]int)
	if i == nil {
		return out, nil
	}
	for _, a := range i.assets {
		out[a.Origin]++
	}
	return out, nil
}

// matchesFilter is the in-memory predicate for Search.
//
// Step 9 contract: this predicate consumes fields that exist on
// detail.ImageAsset today. Style/LicenseRequired filtering can
// come back in a follow-up step once MetadataJSON parsing is
// added at a higher layer (catalog does NOT parse JSON — that's
// the parent's responsibility).
func matchesFilter(a detail.ImageAsset, f ImageFilter) bool {
	if len(f.Origines) > 0 {
		found := false
		for _, o := range f.Origines {
			if a.Origin == o {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.Providers) > 0 {
		found := false
		for _, p := range f.Providers {
			if a.Provider == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	// SlugID is the canonical identity for asset subjects (SubjectID
	// is its same-row equivalent in some callers). Both fields
	// exist on detail.ImageAsset today — match either.
	if f.Slug != "" && a.SlugID != f.Slug && a.SubjectID != f.Slug {
		return false
	}
	if f.SlugPrefix != "" && !hasPrefixCI(a.SlugID, f.SlugPrefix) && !hasPrefixCI(a.SubjectID, f.SlugPrefix) {
		return false
	}
	if len(f.TagsContain) > 0 {
		tagSet := make(map[string]bool, len(a.Tags))
		for _, t := range a.Tags {
			tagSet[strings.ToLower(t)] = true
		}
		for _, need := range f.TagsContain {
			if !tagSet[strings.ToLower(need)] {
				return false
			}
		}
	}
	if f.SearchText != "" {
		text := strings.ToLower(f.SearchText)
		if !strings.Contains(strings.ToLower(a.Description), text) &&
			!strings.Contains(strings.ToLower(a.License), text) {
			return false
		}
	}
	if f.LicenseRequired && a.License == "" {
		return false
	}
	return true
}

// ── Cursor helpers (package-private) ──

func formatCursor(offset int) string {
	return "offset:" + strconv.Itoa(offset)
}

func parseCursor(c string) int {
	if len(c) < 8 || c[:7] != "offset:" {
		return 0
	}
	n, err := strconv.Atoi(c[7:])
	if err != nil {
		return 0
	}
	return n
}

func hasPrefixCI(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a, b := s[i], prefix[i]
		if a >= 'A' && a <= 'Z' {
			a += 32
		}
		if b >= 'A' && b <= 'Z' {
			b += 32
		}
		if a != b {
			return false
		}
	}
	return true
}
