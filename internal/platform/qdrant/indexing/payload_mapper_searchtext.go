// Package indexing — payload_mapper_searchtext.go: search text helpers.
//
// Extracted from payload_mapper.go (July 2026, PR-PAYLOAD-MAPPER-SPLIT).
// Owns: parseMetadataJSON, buildSearchTextInput, resolveSearchText.
package indexing

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"go.uber.org/zap"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// parseMetadataJSON lazily parses metadata JSON on first access.
func parseMetadataJSON(asset *AssetData) {
	if asset.Metadata != nil || asset.MetadataJSON == "" {
		return
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(asset.MetadataJSON), &m); err == nil {
		asset.Metadata = m
	}
}

// buildSearchTextInput maps an AssetData row to the canonical
// SearchTextInput that the per-source strategies consume. The mapping
// is intentionally permissive: every field on SearchTextInput is a
// zero-or-more-value contract; strategies only read the subset they
// document (per `internal/infrastructure/indexing/searchtext/strategies.go`).
//
// AssetData field priorities:
//   - Title         ← asset.Name (canonical human-readable label)
//   - Tags          ← asset.Tags (already-typed []string)
//   - Category      ← asset.Category
//   - Channel       ← asset.ChannelID (YouTube channel name)
//   - Language      ← asset.Language (BCP-47)
//   - Description   ← metadata_json.$.description (when parsed)
//   - Transcript    ← metadata_json.$.transcript ONLY (no asset.SearchText
//     fallback — search_text is the BM25 OUTPUT, never the
//     raw transcript; falling back would feed the assembled
//     search text back into the YouTube strategy's
//     transcript slot and double-count it).
//   - Caption       ← metadata_json.$.caption
//   - Prompt        ← metadata_json.$.prompt
//   - DetectedEntities ← metadata_json.$.detected_entities
//   - OriginProvider   ← metadata_json.$.origin_provider
//
// The function returns a SearchTextInput that ALWAYS carries the
// canonical identifiers (AssetID, Source, MediaType) so the registry
// can dispatch on Source even when the freetext bag is empty.
func buildSearchTextInput(asset *AssetData) appsearchtext.SearchTextInput {
	if asset == nil {
		return appsearchtext.SearchTextInput{}
	}
	parseMetadataJSON(asset)

	// Populate Additional for YouTube clips from metadata_json fields
	// that don't have a dedicated SearchTextInput slot. The youtubeStrategy
	// reads these keys from Additional to produce the canonical search_text.
	// godlike/06 SSOT: the key names here MUST match what youtubeStrategy reads.
	//
	// source_url convergence: AssetData.SourceURL (hydrated from the url
	// column) is the canonical owner; the metadata_json key is a provenance
	// mirror for legacy rows. Prefer the typed field so search text and the
	// IndexDocument airlock use the same precedence.
	sourceURL := firstNonEmpty(asset.SourceURL, assetpkg.MetadataString(asset.Metadata, "source_url"))
	var additional map[string]string
	switch asset.Source {
	case "youtube":
		additional = map[string]string{
			"hook":             assetpkg.MetadataString(asset.Metadata, "hook"),
			"source_url":       sourceURL,
			"speakers":         flattenMetadataSlice(asset.Metadata, "speakers"),
			"mentioned_people": flattenMetadataSlice(asset.Metadata, "mentioned_people"),
		}
	case "stock":
		// stockChunkStrategy reads: event, round, subject, action,
		// source_url, start_sec, end_sec (per stockChunkStrategyAdditionalKeys).
		// godlike/07 NO-FAKE-AVAILABILITY: "event" is left empty because
		// the boxing event name lives at the RUN level (RunInput.FolderName),
		// not in per-chunk metadata_json. The stock strategy handles empty
		// event gracefully (falls through to round-only or prefix-only).
		additional = map[string]string{
			"event":      "",
			"round":      fmtIntMetadata(asset.Metadata, "round"),
			"subject":    assetpkg.MetadataString(asset.Metadata, "title"),
			"action":     assetpkg.MetadataString(asset.Metadata, "description"),
			"source_url": sourceURL,
			"start_sec":  fmtFloatMetadata(asset.Metadata, "start_sec"),
			"end_sec":    fmtFloatMetadata(asset.Metadata, "end_sec"),
			// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent folder
			// metadata for search-text enrichment so Qdrant BM25 can
			// surface "open in Drive folder" navigation from search.
			"timestamp_drive_folder_link": assetpkg.MetadataString(asset.Metadata, "timestamp_drive_folder_link"),
			"timestamp_folder_id":         assetpkg.MetadataString(asset.Metadata, "timestamp_folder_id"),
		}
	}

	return appsearchtext.SearchTextInput{
		AssetID:          asset.ID,
		Source:           asset.Source,
		MediaType:        asset.MediaType,
		Title:            asset.Name,
		Description:      assetpkg.MetadataString(asset.Metadata, "description"),
		Transcript:       assetpkg.MetadataString(asset.Metadata, "transcript"),
		Prompt:           assetpkg.MetadataString(asset.Metadata, "prompt"),
		Caption:          assetpkg.MetadataString(asset.Metadata, "caption"),
		Tags:             asset.Tags,
		Category:         asset.Category,
		Language:         asset.Language,
		Channel:          asset.ChannelID,
		DetectedEntities: assetpkg.MetadataStringSlice(asset.Metadata, "detected_entities"),
		OriginProvider:   assetpkg.MetadataString(asset.Metadata, "origin_provider"),
		Additional:       additional,
	}
}

// fmtIntMetadata reads a numeric metadata_json key and returns its
// decimal string representation. Returns "" when the key is absent or
// zero (omitempty contract: Round=0 is not written to metadata_json).
func fmtIntMetadata(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case float64:
		if val == 0 {
			return ""
		}
		return strconv.FormatFloat(val, 'f', 0, 64)
	case int:
		if val == 0 {
			return ""
		}
		return strconv.Itoa(val)
	default:
		return ""
	}
}

// fmtFloatMetadata reads a float64-typed metadata_json key and returns its
// string representation. Returns "" when the key is absent or zero.
func fmtFloatMetadata(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case float64:
		if val == 0 {
			return ""
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	default:
		return ""
	}
}

// flattenMetadataSlice reads a metadata_json key that may be either a
// []any or a plain string, and returns a space-joined string.
// Used by buildSearchTextInput to convert speakers/mentioned_people arrays
// into a format the searchtext strategies can consume from Additional.
func flattenMetadataSlice(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

// resolveSearchText is the canonical call site for search-text
// construction at indexing time. It owns the precedence order:
//
//  1. SearchTextBuilder (when wired via SetSearchTextBuilder) — returns
//     the per-source canonical text. A nil error is treated as "ok"
//     even when Build returns empty (legitimate empty strategies).
//     A non-nil error is logged at DEBUG and falls through — we
//     never block the IndexDocument on a search-text builder
//     failure (godlike/07 no-fake-availability contract).
//  2. asset.SearchText — the DB-stored pre-built value. Always the
//     graceful fallback when the builder is absent OR returns empty.
//
// Always returns a non-nil string (possibly "") so callers can write
// doc.SearchText directly without nil-checks.
//
// IMPORTANT: returns asset.SearchText UNCHANGED when the builder is nil
// — this is the byte-for-byte contract the pre-existing
// TestAssetToPoint_SparseVector_HasServerSideShape and other tests
// (which call NewPayloadMapper(store, log) WITHOUT SetSearchTextBuilder)
// pin. Production code paths (NewRuntime) wire the registry; admin
// CLI / tests / fixtures that bypass NewRuntime see the legacy
// pass-through behavior.
func (m *PayloadMapper) resolveSearchText(ctx context.Context, asset *AssetData) string {
	if asset == nil {
		return ""
	}
	if m.searchTextBuild != nil {
		input := buildSearchTextInput(asset)

		// Inject index_languages from config so youtubeStrategy can
		// filter TextTracks by configured languages.
		if m.indexLanguages != "" {
			if input.Additional == nil {
				input.Additional = make(map[string]string)
			}
			if _, exists := input.Additional["index_languages"]; !exists {
				input.Additional["index_languages"] = m.indexLanguages
			}
		}

		// Populate TextTracks from the DB so youtubeStrategy can
		// concatenate multilingual transcripts for BM25 search text.
		if m.textTrackQuerier != nil && asset.ID != "" {
			if tracks, err := m.textTrackQuerier.ListByAsset(ctx, asset.ID); err == nil && len(tracks) > 0 {
				entries := make([]appsearchtext.TextTrackEntry, 0, len(tracks))
				for _, t := range tracks {
					if t.TextContent != "" && t.Status == assetpkg.TextTrackReady {
						entries = append(entries, appsearchtext.TextTrackEntry{
							LanguageCode: t.LanguageCode,
							Text:         t.TextContent,
							TextKind:     string(t.TextKind),
						})
					}
				}
				if len(entries) > 0 {
					input.TextTracks = entries
				}
			}
		}

		text, err := m.searchTextBuild.Build(ctx, input)
		if err != nil {
			if m.log != nil {
				// godlike/07 no-fake-availability: surface strategy
				// failures at Warn so operator dashboards catch a
				// panicking or misconfigured strategy. The mapper
				// still degrades gracefully (falls through to
				// asset.SearchText) so a single broken strategy
				// does not block the IndexDocument, but the
				// degradation is visible.
				m.log.Warn("SearchTextBuilder.Build error; falling back to asset.SearchText",
					zap.String("asset_id", asset.ID),
					zap.Error(err))
			}
			// Fall through to asset.SearchText — never block
			// the IndexDocument on a search-text builder issue.
		} else if text != "" {
			return text
		}
	}
	return asset.SearchText
}
