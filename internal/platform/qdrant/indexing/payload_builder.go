// Package indexing — payload_builder.go: canonical writer-side payload builder.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: BuildPayloadFromDocument (the orchestrator) + firstNonEmpty.
//
// The per-domain payload fillers live in sibling files of this package
// (July 2026 split of the flat emitter — script/scene/voiceover/media
// all share one canonical wire shape, so the split follows payload
// domain rather than document type):
//
//	payload_builder_identity.go — base identity + classification keys
//	payload_builder_content.go  — content metadata + provenance/versioning
//	                              + Drive location/timestamps + per-channel
//	                              embedding_version_* provenance
//	payload_builder_semantic.go — title/event/scene/topics/entities/search
//	payload_builder_canonical.go— canonical SSOT block + text-track
//	                              projection
//	payload_builder_vlm.go      — VLM visual-summary block
//	payload_builder_search.go   — composeSemanticDocument adapter
//	                              (delegates to the canonical
//	                              SemanticDocumentComposer, item 7)
package indexing

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

// ══════════════════════════════════════════════════════════════════════════
// PR 6 (refactor/qdrant-index-document) — canonical Mapper airlock.
// ══════════════════════════════════════════════════════════════════════════

// BuildPayloadFromDocument is the canonical writer-side payload builder.
// Reads an IndexDocument (defined in index_document.go) and emits the
// canonical Qdrant payload.
//
// PR 6 (verdict §11): the per-channel embedding_version_<channel>
// payload keys are written from doc.Embeddings[channel].ModelVersion
// (the OBSERVED provenance recorded in the artifact), NOT from
// schema.DenseVectors.ModelVersion (the schema's EXPECTED). The schema
// declares the contract the operator wants; the artifact records what
// the writer actually emitted. Drift surface: the per-channel counter
// in schema.SwitchReport.VersionMismatchPerChannel (PR 12).
//
// FORBIDDEN at this emitter: local_path, status payload keys (locked by
// the IndexDocument airlock — neither field exists on IndexDocument).
// drive_link IS now canonical in payload (PR-CATALOG-MULTILINGUA step
// 6, July 2026) but is FORBIDDEN in embedding_text (forward-prevention
// test
// TestBuildPayloadFromDocument_CanonicalSearchDocument_NoLinkOrLocatorInEmbeddingText
// pins that half of the invariant). The freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocumentCanonicalTypes
// pins the forbidden-SSOT half.
func BuildPayloadFromDocument(doc *IndexDocument, schema *schema.IndexSchema) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	semanticTitle := doc.Metadata.SemanticTitle
	if semanticTitle == "" {
		semanticTitle = buildSemanticTitle(doc.Metadata.Title, doc.Metadata.Event, doc.Metadata.Round, doc.Metadata.Scene, doc.Metadata.Subject)
	}
	// PR-CATALOG-MULTILINGUA step 6 + item 7: the canonical SEARCH DOCUMENT
	// is ALWAYS composed by the SemanticDocumentComposer (the single owner,
	// internal/kernel/semanticdoc). No caller can evade the
	// forward-prevention check by sneaking link/locator text into
	// doc.Metadata.EmbeddingText: the composer reads ONLY the 8 sanctioned
	// fields and emits an empty string when none are populated.
	semanticDoc := composeSemanticDocument(doc)
	embeddingText := semanticDoc.DocumentText
	sourceURL := firstNonEmpty(doc.Metadata.SourceURL, doc.Metadata.YouTubeURL)
	sourceVideoID := firstNonEmpty(doc.Metadata.SourceVideoID, doc.Metadata.YouTubeID)
	name := firstNonEmpty(doc.Metadata.Name, doc.Metadata.Title, doc.Metadata.SemanticTitle, doc.AssetID)
	// PR 6 (July 2026): removed the legacy fall-back to `doc.Metadata.Source`
	// for destination / source_provider / origin. The pre-PR-6 builder
	// silently filled these from asset.Source as a "make sure we have
	// SOMETHING" default — exactly the godlike/07 NO-FAKE-AVAILABILITY
	// placeholder-string anti-pattern. Callers that want these fields MUST
	// set them explicitly via top-level AssetData fields or MetadataJSON.
	destination := doc.Metadata.Destination
	origin := doc.Metadata.Origin
	sourceProvider := doc.Metadata.SourceProvider
	entities := mergeStringSlices(doc.Metadata.Entities, doc.Metadata.People, doc.Metadata.Speakers, doc.Metadata.MentionedPeople)
	if len(entities) == 0 {
		entities = mergeStringSlices(doc.Metadata.People, doc.Metadata.Topics, doc.Metadata.SearchKeywords)
	}

	// Domain fillers (sibling files of this package). The base seed +
	// identity keys live in fillIdentityPayload; each subsequent filler
	// mutates the same map so the emitted payload is byte-identical to
	// the pre-split flat emitter (same keys, same omitempty guards).
	payload := make(map[string]any, 64)
	// item 7: stamp the semantic-document identity so the index state can
	// record exactly which document text was embedded.
	payload["semantic_document_version"] = semanticDoc.Version
	payload["semantic_document_hash"] = semanticDoc.Hash
	// The text vector and query embedder share this exact contract. Stamp the
	// contract identity alongside the observed model/version so a projection
	// cannot look compatible merely because its dimension is 768.
	payload["embedding_contract_hash"] = coreembedding.CanonicalText.Hash()
	fillIdentityPayload(payload, doc, name, destination, origin, sourceProvider, sourceURL, semanticTitle, embeddingText)
	fillContentPayload(payload, doc)
	fillSemanticPayload(payload, doc, entities)
	fillCanonicalPayload(payload, doc, sourceVideoID)
	fillTextTrackPayload(payload, doc)
	fillVLMPayload(payload, doc)

	return payload
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
