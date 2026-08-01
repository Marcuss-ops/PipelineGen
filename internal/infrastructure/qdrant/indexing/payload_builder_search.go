// Package indexing — payload_builder_search.go: canonical search-text
// composers for the writer-side payload builder.
//
// Extracted from payload_builder.go (July 2026 domain split). Owns:
// buildCanonicalSearchDocument (the 8-field embedding_text composer) and
// composeMultilingualTranscriptBlock (the multilingual transcript canon).
package indexing

import (
	"sort"
	"strings"
)

// buildCanonicalSearchDocument composes the canonical search text for
// the asset per PR-CATALOG-MULTILINGUA step 6 (July 2026).
//
// The canonical 8-field composition is:
//
//  1. title            ← doc.Metadata.Title
//  2. description      ← doc.Metadata.Description
//  3. visual_summary   ← doc.Metadata.VisualSummary
//  4. transcript       ← doc.Metadata.Transcripts (multilingual joined;
//     original-language row bare, sequels as `transcript ({lang}): …`)
//     with document.Metadata.Transcript (single string) as the v1
//     fallback for callers that haven't yet adopted the TextTrackQuerier
//     pipeline.
//  5. topics           ← doc.Metadata.Topics (joined as "topics: a, b, c")
//  6. entities         ← doc.Metadata.Entities (joined as "entities: a, b, c")
//  7. event            ← doc.Metadata.Event
//  8. scene            ← doc.Metadata.Scene
//
// Empty fields are SKIPPED (godlike/07 NO-FAKE-AVAILABILITY: a missing
// field MUST NOT emit a placeholder line). Each non-empty field is on
// its own line so the operator can eyeball-verify the composer output.
//
// Multimodal transcript assembly (PR-CATALOG-MULTILINGUA step 6):
// the slice asset.Transcripts carries one row per `is_current=1`
// transcript in asset_text_tracks. The composer:
//   - emits the IsOriginal row (matched against
//     doc.Metadata.OriginalLanguage via case-folded equality) as BARE
//     text on its own line — the primary embedding-text signal.
//   - emits each non-original row as
//     `transcript ({Lang}): {Text}` on a new line — language-coded
//     sequels. Deterministic order: original row first (if any), then
//     non-original rows in Lang-ASC alphabetical order so re-runs
//     produce byte-stable embedding_text.
//
// Forward-prevention contract (pinned by payload_builder_test.go):
// the composition MUST NOT contain any link/locator metadata. The
// following forbidden substrings — if any appear in the output —
// trigger the test failure surface:
//
//	drive_link / drive_path / source_url / http:// / https://
//	source_video_id / youtube_video_id / youtube_url
//	job_id / workflow_id / policy_version / chunk_index / total_chunks
//	run_fingerprint / chunk_id / clip_id / asset_id
//
// godlike/06 SSOT: the canonical field set is the SINGLE source of
// truth for the embedding_text composition. Per-source variations
// (YouTube additional hook, Stock tags formatted as "Tags: ...")
// belong in payload filter fields, NOT in the embedding_text. The
// embedding_text is the search vector input — link/locator in it
// dilutes the embedding's semantic focus.
//
// godlike/07 minimum-blast-radius: the pre-existing per-source
// switch-case helper (which embedded workflow_id, job_id, run_fingerprint,
// chunk_index, total_chunks, policy_version, source_video_id, source_provider
// labels) was REMOVED in this PR alongside the strconv import — that
// import was only used by that helper. The 8-field composer is the
// ONLY embedding_text composer in the file.
func buildCanonicalSearchDocument(doc *IndexDocument) string {
	if doc == nil {
		return ""
	}
	parts := make([]string, 0, 8)

	if v := strings.TrimSpace(doc.Metadata.Title); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(doc.Metadata.Description); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(doc.Metadata.VisualSummary); v != "" {
		parts = append(parts, v)
	}
	if transcript := composeMultilingualTranscriptBlock(doc); transcript != "" {
		parts = append(parts, transcript)
	}
	if len(doc.Metadata.Topics) > 0 {
		parts = append(parts, "topics: "+strings.Join(doc.Metadata.Topics, ", "))
	}
	if len(doc.Metadata.Entities) > 0 {
		parts = append(parts, "entities: "+strings.Join(doc.Metadata.Entities, ", "))
	}
	if v := strings.TrimSpace(doc.Metadata.Event); v != "" {
		parts = append(parts, "event: "+v)
	}
	if v := strings.TrimSpace(doc.Metadata.Scene); v != "" {
		parts = append(parts, "scene: "+v)
	}
	return strings.Join(parts, "\n")
}

// composeMultilingualTranscriptBlock renders the multilingual transcript
// canon for embedding_text per PR-CATALOG-MULTILINGUA step 6. Three cases:
//
//  1. doc.Metadata.Transcripts is non-empty: emit IsOriginal row as bare
//     text first, then each non-original row as
//     `transcript ({Lang}): {Text}` on a new line. Rows with empty
//     Text are SKIPPED (godlike/07 NO-FAKE-AVAILABILITY). Lang code
//     is case-folded for the original-language match so `"EN"` and
//     `"en"` are equivalent. Order within the sequels is Lang-ASC
//     alphabetical for byte-stable output.
//  2. doc.Metadata.Transcripts is empty AND doc.Metadata.Transcript
//     is non-empty (legacy single-string fallback): emit
//     doc.Metadata.Transcript verbatim. This keeps the embedding-text
//     stable for the transition window for callers that haven't yet
//     adopted the new TextTrackQuerier flow.
//  3. Both empty: emit "" (the embedding_text composer at the caller
//     layer SKIPS the field — embedding-text just doesn't include the
//     transcript slot).
func composeMultilingualTranscriptBlock(doc *IndexDocument) string {
	if doc == nil {
		return ""
	}
	if len(doc.Metadata.Transcripts) > 0 {
		originalLang := strings.TrimSpace(doc.Metadata.OriginalLanguage)
		var originalLine string
		var others []TranscriptTrack
		for _, t := range doc.Metadata.Transcripts {
			text := strings.TrimSpace(t.Text)
			if text == "" {
				continue // godlike/07 NO-FAKE-AVAILABILITY: skip empty rows
			}
			if originalLang != "" && strings.EqualFold(t.Lang, originalLang) && t.IsOriginal {
				originalLine = text
				continue
			}
			if t.IsOriginal && originalLang == "" {
				// OriginalLanguage empty on the airlock but IsOriginal=true
				// (text_track_repository wires IsOriginal from media_assets.language;
				// if that is empty, ANY row can claim IsOriginal). Treat the
				// FIRST IsOriginal row as bare text and route subsequent rows
				// to the sequel bucket so a single bare-text slot is emitted.
				if originalLine == "" {
					originalLine = text
					continue
				}
			}
			others = append(others, TranscriptTrack{Lang: t.Lang, Text: text})
		}
		// Sort sequels by Lang ASC for deterministic byte-stable output.
		sort.SliceStable(others, func(i, j int) bool {
			return others[i].Lang < others[j].Lang
		})
		out := ""
		if originalLine != "" {
			out = originalLine
			for _, t := range others {
				out += "\ntranscript (" + t.Lang + "): " + t.Text
			}
			return out
		}
		// No IsOriginal row identified. Emit ALL rows as sequels with a
		// stable Lang-ASC order so the rubric stays non-empty.
		for _, t := range others {
			if out == "" {
				out = "transcript (" + t.Lang + "): " + t.Text
			} else {
				out += "\ntranscript (" + t.Lang + "): " + t.Text
			}
		}
		return out
	}
	// Legacy single-string fallback (pre-step-6 callers).
	return strings.TrimSpace(doc.Metadata.Transcript)
}
