// Package semanticdoc is the canonical SemanticDocument composer — the SINGLE
// owner of the embedding DocumentText (item 7).
//
// Before this package, the document text fed to the text embedder was
// assembled in three places that could drift apart: the writer-side
// embedding_text composer (payload_builder_search.go), the search-text
// strategies, and the sidecar. This package converges them onto ONE
// deterministic composition: AssetDraft metadata → SemanticDocument →
// DocumentText, with a version and a content hash so the index state can
// record exactly which document text was embedded.
//
// The canonical composition is PR-CATALOG-MULTILINGUA step 6 (8 sanctioned
// fields, in order):
//
//  1. title
//  2. description
//  3. visual_summary
//  4. transcript (multilingual: original-language row bare, sequels as
//     `transcript ({lang}): …`, legacy single-string fallback)
//  5. topics   ("topics: a, b, c")
//  6. entities ("entities: a, b, c")
//  7. event    ("event: …")
//  8. scene    ("scene: …")
//
// Empty fields are SKIPPED (godlike/07 NO-FAKE-AVAILABILITY — no placeholder
// lines). Link/locator metadata is never part of the composition (the
// forward-prevention contract).
package semanticdoc

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// Version is the canonical semantic-document composition version. It is
// anchored to the EmbeddingContract SSOT so the document version and the
// embedding contract can never drift independently.
const Version = coreembedding.SemanticDocumentVersionV3

// TranscriptSegment is one multilingual transcript row. It mirrors the
// infra-layer TranscriptTrack shape without importing the infra package
// (application → kernel dependency direction only).
type TranscriptSegment struct {
	Lang       string
	Text       string
	IsOriginal bool
}

// Input is the plain semantic surface the composer reads. It is deliberately
// transport-agnostic: the infra payload mapper converts its IndexedMetadata
// into this shape, and any other producer can do the same.
type Input struct {
	AssetID          string
	Title            string
	Description      string
	SemanticSummary  string
	Summary          string
	VisualSummary    string
	OriginalLanguage string
	Transcripts      []TranscriptSegment
	Transcript       string // legacy single-string transcript fallback
	Topics           []string
	Entities         []string
	Tags             []string
	Event            string
	Scene            string
	// Evidence is the single resolved grounding text (asset.EvidenceDocument)
	// produced by the canonical EvidenceResolver. It is carried through as a
	// SEPARATE field — it does NOT alter the 8-field DocumentText
	// composition. Consumers (script grounding, provenance/debug) read
	// doc.Evidence.Text; the embedder still embeds the 8-field DocumentText.
	Evidence asset.EvidenceDocument
}

// SemanticDocument is the canonical composed document. DocumentText is the
// exact text handed to the embedder; Hash is the SHA-256 fingerprint of that
// exact DocumentText; Version identifies the composition schema.
type SemanticDocument struct {
	AssetID        string
	Title          string
	Description    string
	Summary        string
	Entities       []string
	Tags           []string
	TranscriptText string
	VisualSummary  string
	DocumentText   string
	Version        string
	Hash           string
	// Evidence is the resolved grounding text carried through verbatim
	// from Input.Evidence (separate from the 8-field DocumentText).
	Evidence asset.EvidenceDocument
}

// Compose builds the canonical SemanticDocument for the given input. It is
// pure and deterministic: the same Input always yields the same DocumentText,
// Version and Hash.
func Compose(in Input) SemanticDocument {
	transcriptText := composeTranscript(in)
	d := SemanticDocument{
		AssetID:        in.AssetID,
		Title:          strings.TrimSpace(in.Title),
		Description:    strings.TrimSpace(in.Description),
		Summary:        strings.TrimSpace(in.Summary),
		Entities:       in.Entities,
		Tags:           in.Tags,
		TranscriptText: transcriptText,
		VisualSummary:  strings.TrimSpace(in.VisualSummary),
		Evidence:       in.Evidence,
		Version:        Version,
	}
	d.DocumentText = buildDocumentText(in, transcriptText)
	d.Hash = hashDocumentText(d.DocumentText)
	return d
}

// buildDocumentText assembles the canonical 8-field composition. transcript
// is pre-computed by Compose so it is not recomputed twice.
func buildDocumentText(in Input, transcript string) string {
	parts := make([]string, 0, 8)

	if v := strings.TrimSpace(in.Title); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(in.Description); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(in.VisualSummary); v != "" {
		parts = append(parts, v)
	}
	if transcript != "" {
		parts = append(parts, transcript)
	}
	if len(in.Topics) > 0 {
		parts = append(parts, "topics: "+strings.Join(in.Topics, ", "))
	}
	if len(in.Entities) > 0 {
		parts = append(parts, "entities: "+strings.Join(in.Entities, ", "))
	}
	if v := strings.TrimSpace(in.Event); v != "" {
		parts = append(parts, "event: "+v)
	}
	if v := strings.TrimSpace(in.Scene); v != "" {
		parts = append(parts, "scene: "+v)
	}
	return strings.Join(parts, "\n")
}

// composeTranscript renders the multilingual transcript canon. Three cases:
//
//  1. Transcripts non-empty: emit the original-language row as bare text
//     first, then each non-original row as `transcript ({lang}): {text}` in
//     Lang-ASC order (byte-stable). Empty-text rows are skipped.
//  2. Transcripts empty AND Transcript non-empty: emit the legacy single
//     string verbatim.
//  3. Both empty: return "" (the caller skips the transcript slot).
func composeTranscript(in Input) string {
	if len(in.Transcripts) > 0 {
		originalLang := strings.TrimSpace(in.OriginalLanguage)
		var originalLine string
		var others []TranscriptSegment
		for _, t := range in.Transcripts {
			text := strings.TrimSpace(t.Text)
			if text == "" {
				continue
			}
			if originalLang != "" && strings.EqualFold(t.Lang, originalLang) && t.IsOriginal {
				originalLine = text
				continue
			}
			if t.IsOriginal && originalLang == "" {
				// OriginalLanguage empty on the airlock but IsOriginal=true:
				// treat the FIRST IsOriginal row as bare text and route the
				// rest to the sequel bucket.
				if originalLine == "" {
					originalLine = text
					continue
				}
			}
			others = append(others, TranscriptSegment{Lang: t.Lang, Text: text})
		}
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
		for _, t := range others {
			if out == "" {
				out = "transcript (" + t.Lang + "): " + t.Text
			} else {
				out += "\ntranscript (" + t.Lang + "): " + t.Text
			}
		}
		return out
	}
	return strings.TrimSpace(in.Transcript)
}

// hashDocumentText returns the SHA-256 hex fingerprint of the exact document
// text sent to the embedder (semantic_document_hash).
func hashDocumentText(text string) string {
	sum := digest.SHA256Bytes([]byte(text))
	return sum
}
