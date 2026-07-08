// types/types_published_artifact.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// PublishedArtifact represents an artifact that has been successfully
// published to a remote location. It extends VerifiedArtifact with
// the canonical AssetLocation.
//
// This is the input to AssetFinalizerTx.FinalizeAsset.
//
// P1.2 (July 2026): the `Required bool` field is replaced by the typed
// `Requirement ArtifactRequirement` enum carried through from
// VerifiedArtifact. The ArtifactPreparation service preserves the
// requirement during the local→remote publish step. Clean cutover —
// no back-compat alias per godlike/06 one-owner-per-fact.
type PublishedArtifact struct {
	// ArtifactID is the unique canonical identifier for this artifact.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the filename as published on the remote location.
	Filename string `json:"filename"`

	// MIMEType is the IANA media type.
	MIMEType string `json:"mime_type"`

	// SizeBytes is the artifact size in bytes.
	SizeBytes int64 `json:"size_bytes"`

	// SHA256 is the hex-encoded SHA-256 digest of the artifact content.
	SHA256 string `json:"sha256"`

	// SourceVersion is the logical version of the source.
	SourceVersion int64 `json:"source_version"`

	// Requirement classifies whether this artifact blocks job
	// completion (P1.2). Carried verbatim from VerifiedArtifact.Requirement
	// through ArtifactPreparation.Prepare. JobFinalizer uses this
	// typed field for the cross-reference against OptionalDeclarations.
	Requirement ArtifactRequirement `json:"requirement"`

	// IdempotencyKey is the deterministic key the worker supplied.
	IdempotencyKey string `json:"idempotency_key"`

	// Description is the human-readable English summary for the clip
	// or artifact. It is carried through to the canonical asset row
	// metadata for downstream search/indexing.
	Description string `json:"description,omitempty"`

	// ArtifactMetadata carries source-specific enrichment data that
	// the AssetTxFinalizer merges into media_assets.metadata_json.
	// This bridge preserves semantic fields (title, round, tags,
	// category, source_provider, drive_path, etc.) that would
	// otherwise be lost at the PublishedArtifact boundary.
	// The map is keyed by the JSON field name the PayloadMapper
	// expects when reading from media_assets.metadata_json.
	ArtifactMetadata map[string]any `json:"artifact_metadata,omitempty"`

	// Source is the content source identity ("stock", "youtube",
	// "artlist", "voiceover", etc.) written to media_assets.source.
	// When empty, the AssetTxFinalizer falls back to the publish
	// action string (Location.Action) for backward compat.
	// PR-STOCK-SOURCE-FIX (July 2026): stock assets must NOT use
	// Location.Action="created" as source — that's the publish
	// action, not the content source.
	Source string `json:"source,omitempty"`

	// Location is the canonical descriptor of where the artifact was
	// published.
	Location AssetLocation `json:"location"`
}
