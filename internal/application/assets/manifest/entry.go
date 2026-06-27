// Package manifest — AssetManifestService. PR 6 / PR 7 cutover
// (codex/asset-manifest-cutover, June 2026).
//
// Every per-asset metadata write (clips upload, media processor,
// Artlist semantic enricher, others TBD) routes through this
// package. The pre-cutover writers (clips upload helper in
// app/clips + Processor private merge method +
// SemanticEnricher private merge method + package-level shared
// shared mutex + manual []map[string]any
// merge) are removed in the same PR.
//
// Design notes:
//
//   - Entry is the canonical asset-metadata shape. Each Entry
//     uniquely identifies an asset by AssetID (which is stable
//     across re-uploads of the SAME content — see clips upload
//     flow's content-hash-derived clipID).
//
//   - AssetToEntry is the SINGLE mapper used before AND after
//     enrichment (PR 7 spec line). The pre-enrichment variant
//     emits an Entry with empty/unset Metadata; the post-enrichment
//     variant emits the same shape with the enriched Tags /
//     SearchText / SearchTerms / Metadata fields filled in. The
//     manifest service does NOT distinguish — it merges by
//     AssetID and the deep merge of Metadata ensures enrichment
//     diffs land without losing pre-existing keys.
//
//   - Extras is the call-site-specific per-clip tail (clip_page_url,
//     duplicate_of, term, etc.) that the mapper doesn't know.
//     Merged at the top level of Metadata so the on-disk shape is
//     flat.
package manifest

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Entry is the canonical wire shape for one asset's metadata row
// inside a per-folder per-local-dir `metadata.json` file. JSON
// tags are stable: keys are snake_case to match the pre-PR6
// on-disk shape (backward-compatible with hand-rolled JSON readers),
// but the new code treats Entry as authoritative.
type Entry struct {
	AssetID     string         `json:"asset_id"`
	Source      string         `json:"source,omitempty"`
	Name        string         `json:"name,omitempty"`
	LocalPath   string         `json:"local_path,omitempty"`
	DriveFileID string         `json:"drive_file_id,omitempty"`
	DriveLink   string         `json:"drive_link,omitempty"`
	FileHash    string         `json:"file_hash,omitempty"`
	DurationSec float64        `json:"duration_sec,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	SearchTerms []string       `json:"search_terms,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// AssetToEntry is the canonical Asset→Entry mapper.
//
// Invoked from the upload flow BEFORE semantic enrichment (with
// empty Metadata) and from the semantic enrichment flow AFTER
// enrichment (with Metadata populated). Same mapper, same shape;
// the merge-by-AssetID invariant in service.go ensures the second
// invocation overwrites the first cleanly.
//
// params:
//   - a:       the asset struct (must be non-nil; nil maps to empty Entry).
//   - source:  the canonical source string ("youtube", "artlist",
//              "manual", "stock", ...). Required.
//   - term:    the search term for artlist-style discovery (optional;
//              empty string leaves the term key absent from Metadata).
//   - extras:  call-site-specific keys (clip_page_url, duplicate_of,
//              etc.). Merged at the top level of Metadata, NOT
//              promoted to first-class Entry fields.
func AssetToEntry(a *asset.Asset, source, term string, extras map[string]any) Entry {
	if a == nil {
		return Entry{UpdatedAt: time.Now().UTC()}
	}
	e := Entry{
		AssetID:     a.ID,
		Source:      source,
		Name:        a.Name,
		LocalPath:   a.LocalPath(),
		DriveFileID: a.DriveFileID(),
		DriveLink:   a.DriveLink(),
		FileHash:    a.FileHash(),
		DurationSec: a.Duration.Seconds(),
		Tags:        a.Tags,
		SearchTerms: a.SearchTerms,
		UpdatedAt:   time.Now().UTC(),
	}
	meta := make(map[string]any)
	if a.SearchText != "" {
		meta["search_text"] = a.SearchText
	}
	for k, v := range a.Metadata {
		if _, exists := meta[k]; !exists {
			meta[k] = v
		}
	}
	if term != "" {
		meta["term"] = term
	}
	for k, v := range extras {
		if _, exists := meta[k]; !exists {
			meta[k] = v
		}
	}
	if len(meta) > 0 {
		e.Metadata = meta
	}
	return e
}
