// Package indexing — payload_builder_search.go: the semantic-document adapter
// for the writer-side payload builder.
//
// The canonical DocumentText composition now lives in
// internal/kernel/semanticdoc (item 7): SemanticDocumentComposer
// is the SINGLE owner of the embedding document text. This file adapts the
// infra IndexedMetadata shape into the semanticdoc.Input shape and returns the
// composed SemanticDocument — it does NOT re-compose a second, different text.
//
// The canonical 8-field composition (title / description / visual_summary /
// transcript / topics / entities / event / scene) and the multilingual
// transcript canon (original-language row bare, sequels as
// `transcript ({lang}): …`, Lang-ASC order) are owned by the semanticdoc
// package; see semanticdoc.go for the full contract and forward-prevention
// guarantees (link/locator metadata is never part of the document text).
package indexing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/semanticdoc"
)

// composeSemanticDocument maps the infra IndexedMetadata onto the canonical
// semanticdoc.Input and delegates the DocumentText composition to the
// SemanticDocumentComposer (the single owner). It returns the full composed
// document (DocumentText + Version + Hash) so the payload builder can stamp
// semantic_document_version and semantic_document_hash.
func composeSemanticDocument(doc *IndexDocument) semanticdoc.SemanticDocument {
	if doc == nil {
		return semanticdoc.Compose(semanticdoc.Input{})
	}
	transcripts := make([]semanticdoc.TranscriptSegment, len(doc.Metadata.Transcripts))
	for i, t := range doc.Metadata.Transcripts {
		transcripts[i] = semanticdoc.TranscriptSegment{Lang: t.Lang, Text: t.Text, IsOriginal: t.IsOriginal}
	}
	return semanticdoc.Compose(semanticdoc.Input{
		AssetID:          doc.AssetID,
		Title:            doc.Metadata.Title,
		Description:      doc.Metadata.Description,
		SemanticSummary:  doc.Metadata.SemanticSummary,
		Summary:          doc.Metadata.Summary,
		VisualSummary:    doc.Metadata.VisualSummary,
		OriginalLanguage: doc.Metadata.OriginalLanguage,
		Transcripts:      transcripts,
		Transcript:       doc.Metadata.Transcript,
		Topics:           doc.Metadata.Topics,
		Entities:         doc.Metadata.Entities,
		Tags:             doc.Metadata.Tags,
		Event:            doc.Metadata.Event,
		Scene:            doc.Metadata.Scene,
		Evidence:         resolveIndexedEvidence(doc),
	})
}

// resolveIndexedEvidence projects the IndexedMetadata candidates onto the
// canonical EvidenceResolver (single precedence SSOT). The transcript tier
// reads Metadata.Transcript (the canonical multilingual concatenated block);
// the metadata tiers read Summary / VisualSummary / Description directly.
func resolveIndexedEvidence(doc *IndexDocument) asset.EvidenceDocument {
	if doc == nil {
		return asset.EvidenceDocument{}
	}
	return asset.ResolveEvidence(asset.EvidenceInput{
		AssetID:         doc.AssetID,
		Transcript:      doc.Metadata.Transcript,
		SemanticSummary: doc.Metadata.SemanticSummary,
		VisualSummary:   doc.Metadata.VisualSummary,
		Summary:         doc.Metadata.Summary,
		Description:     doc.Metadata.Description,
		Language:        doc.Metadata.OriginalLanguage,
	})
}
