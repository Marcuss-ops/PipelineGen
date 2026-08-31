// types/types_published_artifact.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// AssetRenditionLocation describes a single technical variant of a
// published artifact (master, mezzanine, proxy, thumbnail, storyboard,
// etc.) so that AssetFinalizerTx can persist it as an asset_locations
// row and a matching asset_renditions row inside the same transaction.
type AssetRenditionLocation struct {
	// Kind is the semantic role of this rendition, e.g. "master",
	// "mezzanine", "proxy", "thumbnail", "storyboard".
	Kind string `json:"kind"`

	// Provider identifies the storage backend (e.g. "local", "drive", "s3").
	Provider string `json:"provider"`

	// FileID is the provider-specific file identifier, if any.
	FileID string `json:"file_id,omitempty"`

	// URI is the physical location of the rendition (e.g. local path).
	URI string `json:"uri"`

	// WebViewLink is the human-readable URL to view the file.
	WebViewLink string `json:"web_view_link,omitempty"`

	// DownloadLink is the direct download URL for the file.
	DownloadLink string `json:"download_link,omitempty"`

	// MimeType is the IANA media type.
	MimeType string `json:"mime_type,omitempty"`

	// SizeBytes is the file size in bytes.
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// LegacyFileMD5 is the SHA-256 digest of the rendition content.
	LegacyFileMD5 string `json:"legacy_file_md5,omitempty"`

	// Width and Height are the pixel dimensions.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// FPS is the frame rate.
	FPS float64 `json:"fps,omitempty"`

	// Bitrate is the average bitrate in bits per second.
	Bitrate int64 `json:"bitrate,omitempty"`

	// Container is the file container, e.g. "mp4", "mov".
	Container string `json:"container,omitempty"`

	// Codec is the video/audio codec, e.g. "h264".
	Codec string `json:"codec,omitempty"`
}

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
	Source    string `json:"source,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Language  string `json:"language,omitempty"`

	// Location is the canonical descriptor of where the artifact was
	// published.
	Location AssetLocation `json:"location"`

	// Renditions are additional technical variants (master, mezzanine,
	// proxy, thumbnail, storyboard, etc.) that should be persisted
	// alongside the primary artifact. The canonical AssetFinalizerTx
	// writes each rendition as an asset_locations row and a matching
	// asset_renditions row inside the same transaction.
	// Optional — when empty, only the primary Location is written.
	Renditions []AssetRenditionLocation `json:"renditions,omitempty"`

	// ───────────────────────────────────────────────────────────────────
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): canonical
	// post-commit materialize fan-out payload.
	//
	// SourceText is the human-readable text that should be translated
	// into the configured MaterializeLanguages set after the artifact
	// has been durably persisted. SourceTextHash is the SHA-256 of
	// SourceText (the materialize ActiveKey uses this — callers must
	// populate BOTH fields; an empty SourceTextHash means "no fan-out").
	// SourceLanguage is the BCP-47 of the source text.
	//
	// godlike/07 minimum-blast-radius: ALL three fields are OPTIONAL.
	// Pre-Fase-4 callers (lightweight assets without source text —
	// pure soundeffects, image-only chunks) populate NOTHING here
	// and the canonical FirePostCommitHooks short-circuits silently.
	//
	// godlike/06 SSOT: this is the canonical seam between
	// AssetFinalizerTx (which persists the asset row) and the texttracks
	// package (which fans out translation jobs). Callers that compute
	// the source text at finalize time (pipeline-specific) populate
	// these fields; the hook fires AFTER tx.Commit.
	// ───────────────────────────────────────────────────────────────────
	SourceTextHash string `json:"source_text_hash,omitempty"`
	SourceLanguage string `json:"source_language,omitempty"`
}
