// Package asset — canonical_metadata.go enshrines the typed SSOT
// shape for media_assets.metadata_json on generated images.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - This file is the SOLE canonical typed owner of
//     CanonicalMediaMetadata.
//   - The application-layer builder
//     (internal/application/images/storage_ingest_direct.go)
//     marshals THIS struct directly, never
//     json.Marshal(map[string]any{...}) ad-hoc literals.
//   - The 10 fields below + their JSON tags + their JSON keys are
//     the wire contract. Adding, renaming, or removing a key
//     requires:
//     (a) edit this file,
//     (b) edit buildCanonicalGeneratedMetadata,
//     (c) edit storage_ingest_metadata_test.go (canonicalRequiredKeys),
//     (d) edit canonical_metadata_test.go (expectedCanonicalFieldNames).
//
// Comparable scope: ClipSemanticMetadata (in this same package)
// covers the YouTube/Stock audio+video pipelines;
// CanonicalMediaMetadata covers the generated-image pipeline.
package asset

// CanonicalMediaMetadata is the typed authoritative shape of
// media_assets.metadata_json for a generated image that did NOT
// pass through the semantic enricher (godlike/06 SSOT,
// PR-CANONICAL-GENERATED-IMAGE-METADATA, July 2026).
//
// Consumers:
//   - WRITER: buildCanonicalGeneratedMetadata (application/images)
//   - ECHO:   generated_generate_handler.go returns the JSON to
//     callers via the "metadata_json" field on the response.
//   - READ:   Qdrant payload mapper, reconciler scanner, operator
//     DB dives into metadata_json.
//
// godlike/06 wire-shape invariants:
//   - Every field below ALWAYS appears in the wire JSON (no
//     `omitempty`). Adding omitempty on any field would silently
//     drop the key on zero values, breaking downstream readers
//     that iterate the canonical 10-key set.
//   - The JSON tags below ARE the wire keys; renaming a Go field
//     without updating the tag breaks persistence for every
//     existing media_assets row.
//   - Provider + Origin use the typed ImageProvider / ImageOrigin
//     enums (image_taxonomy.go) and round-trip through JSON as
//     their underlying string constants — preventing string
//     typo drift at the producer ("Generated" vs "generated",
//     "google-slides" vs "googleSlides", etc.).
//
// Adding a new canonical key: add the field here, update
// buildCanonicalGeneratedMetadata, update canonicalRequiredKeys
// in internal/application/images/storage_ingest_metadata_test.go
// AND update canonical_metadata_test.go (expectedCanonicalFieldNames).
// The reconciler reader (forward-pointer PR-QDRANT-IMAGES-RECONCILE)
// is the additional downstream gate.
type CanonicalMediaMetadata struct {
	// PromptOriginal is the user-authored description that seeded
	// the generation. Mirrors ImageAsset.Description upstream.
	PromptOriginal string `json:"prompt_original"`

	// SemanticDescription is the empty default for the unenriched
	// path. When the semantic enricher runs, it overrides this
	// field via a separate persistence write (forward-pointer
	// PR-ENRICH-METADATA); the unenriched fallback keeps "" so
	// downstream readers always see a non-absent key.
	SemanticDescription string `json:"semantic_description"`

	// Style is the visual style (e.g. "cinematic", "medievale",
	// "anime"). Mirror of ImageAsset.Style.
	Style string `json:"style"`

	// Tags is the deduplicated tag list. nil and []string{} both
	// round-trip distinctly ("tags": null vs "tags": []) — readers
	// must handle both shapes; we deliberately do not silently
	// coerce nil → []string{} in this struct.
	Tags []string `json:"tags"`

	// Provider is the typed ImageProvider enum. Today only
	// ProviderGoogleSlides is emitted on the generated-image
	// path; the typed enum prevents typo drift at the producer.
	Provider ImageProvider `json:"provider"`

	// Origin is the typed ImageOrigin enum. Locked to
	// ImageOriginGenerated on the AI path (godlike/06 invariant).
	Origin ImageOrigin `json:"origin"`

	// Width is the pixel width of the generated image. Mirrors
	// ImageAsset.Width (typed column).
	Width int `json:"width"`

	// Height is the pixel height of the generated image. Mirrors
	// ImageAsset.Height (typed column).
	Height int `json:"height"`

	// ContentHash is the SHA-256 fingerprint of the image bytes.
	// Mirrors the ImageAsset.Hash column. The reconciler
	// scanner reads metadata_json.$.content_hash to bind Qdrant
	// payloads to media_assets rows.
	ContentHash string `json:"content_hash"`

	// EmbeddingVersionVisual is the canonical SigLIP model
	// version string, sourced from
	// schema.VisualEmbeddingModelVersion. Rotating the visual
	// embedding model is a one-line edit at the schema package
	// — paired with a Qdrant schema migration so the named
	// vector "visual" dims stay in sync.
	EmbeddingVersionVisual string `json:"embedding_version_visual"`
}
