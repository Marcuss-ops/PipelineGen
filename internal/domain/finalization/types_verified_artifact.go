// types/types_verified_artifact.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// VerifiedArtifact represents an artifact that has been locally
// validated (content hash computed, size verified) but has NOT yet
// been published to a remote location.
//
// This is the output of a capability's production step. The
// ArtifactPreparationService transforms it into a PublishedArtifact.
//
// P1.2 (July 2026): the `Required bool` field is replaced by the typed
// `Requirement ArtifactRequirement` enum. The zero value
// (ArtifactRequirementInvalid) is explicitly rejected by the
// JobFinalizer at validation time so callers cannot accidentally
// default to "Optional" by leaving the field unset.
type VerifiedArtifact struct {
	// ArtifactID is the unique canonical identifier for this artifact.
	// Derived from a content hash (SHA-256) or a deterministic
	// capability-level ID.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the desired filename (without path) for publication.
	Filename string `json:"filename"`

	// LocalPath is the absolute path to the artifact on local disk.
	LocalPath string `json:"local_path"`

	// MIMEType is the IANA media type (e.g. "video/mp4", "image/png").
	MIMEType string `json:"mime_type"`

	// SizeBytes is the artifact size in bytes.
	SizeBytes int64 `json:"size_bytes"`

	// SHA256 is the hex-encoded SHA-256 digest of the artifact content.
	SHA256 string `json:"sha256"`

	// SourceVersion is the logical version of the source that produced
	// this artifact. Monotonically increasing within a capability.
	SourceVersion int64 `json:"source_version"`

	// Requirement classifies whether this artifact blocks job
	// completion. Set explicitly to ArtifactRequirementRequired for
	// block-on-missing artifacts, ArtifactRequirementOptional for
	// best-effort sidecars. The zero value (ArtifactRequirementInvalid)
	// is rejected at validation — callers MUST set this field.
	Requirement ArtifactRequirement `json:"requirement"`

	// IdempotencyKey is a deterministic key that makes publication
	// and finalisation idempotent. Same content → same key.
	IdempotencyKey string `json:"idempotency_key"`

	// RootFolderName is the human-readable top-level folder name used
	// by Drive path builders when the artifact belongs to a named run.
	// When empty, infrastructure falls back to a synthetic label.
	RootFolderName string `json:"root_folder_name,omitempty"`

	// RootFolderOverride is the legacy explicit provider root folder ID.
	// It remains for compatibility with older infrastructure callers.
	// New application producers MUST use ResolvedFolderID together with
	// RootFolderResolved instead of this escape hatch.
	RootFolderOverride string `json:"root_folder_override,omitempty"`

	// ResolvedFolderID is the semantic, already-resolved Drive folder
	// identifier for this artifact. It is eligible for direct delivery
	// only when RootFolderResolved is true.
	ResolvedFolderID string `json:"resolved_folder_id,omitempty"`

	// RootFolderResolved records whether the upstream Drive folder
	// resolution gate already verified the Drive folder corresponding
	// to this artifact's root before VerifyArtifact was constructed.
	// Stock pipeline sets this from RunInput.DriveFolderResolved at
	// every VerifiedArtifact construction site
	// (step_extract_clips, step_publish_chunks_phase,
	// step_publish_metadata_phase). When true, downstream consumers
	// can skip the resolution probe and treat the root as resolved;
	// when false (zero value, or legacy callers that predate this
	// field), downstream must resolve the folder itself or refuse per
	// the DriveFolderResolved gate contract.
	//
	// This field is a sticky-resolved-state trace, NOT a redirector:
	// it does not name or alter the folder path. The carrier fields
	// are RootFolderName (human-readable top-level label) and
	// ResolvedFolderID (semantic direct destination). Legacy
	// RootFolderOverride remains an infrastructure compatibility seam.
	RootFolderResolved bool `json:"root_folder_resolved,omitempty"`

	// Description is the human-readable English summary for the clip
	// or artifact. Stock uses it to carry per-timestamp narration into
	// Drive metadata and Qdrant payloads.
	Description string `json:"description,omitempty"`

	// PathLeafName is the human-readable leaf folder name used for the
	// artifact's immediate Drive subfolder (for example, the timestamp
	// directory under the named run folder). When empty, infrastructure
	// falls back to an index-based synthetic label.
	PathLeafName string `json:"path_leaf_name,omitempty"`

	// Source is the content source identity ("stock", "youtube",
	// "artlist", "voiceover", "document", etc.) carried through
	// ArtifactPreparation.Prepare to PublishedArtifact.Source, which
	// the AssetTxFinalizer writes to media_assets.source.
	//
	// PR-SOURCE-FIX (July 2026): without this field, the
	// AssetTxFinalizer falls back to string(a.Location.Action)
	// which is "created" — the publish action, not the content
	// source. Every pipeline MUST set this before calling Prepare.
	Source string `json:"source,omitempty"`
}
