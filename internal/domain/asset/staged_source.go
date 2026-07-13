// Package asset — StagedSource DTO (PR-MEDIATRANSFORMER-RENAME, July 2026).
//
// StagedSource is the canonical output DTO of assets.SourceStager
// (PR-SOURCESTAGER-CONSOLIDATE, July 2026). It is a pure data
// structure — no behavior, no infrastructure references — and is
// the SOLE owner of the on-disk file path + intermediate hash for
// a staged remote source.
//
// PR-MEDIATRANSFORMER-RENAME (July 2026): StagedSource was
// previously defined at internal/application/assets/ports.go.
// The new MediaTransformer contract (asset.MediaTransformer.Transform)
// takes a *StagedSource as a method parameter, so the type MUST
// live in the domain layer (per AGENTS.md Pattern 8: domain is
// the bottom of the import graph; application/domain must not
// be imported from domain). Moving the type to the domain layer
// resolves the layering violation and lets the SourceStager port
// in the application layer reference it as a parameter without
// creating an import cycle.
//
// godlike/06 SSOT: this file is the canonical SOLE owner of
// the StagedSource struct. No other file in the codebase may
// declare a type with this name (the stock pipeline's local
// `stockpipeline.StagedSource` is a DIFFERENT, smaller DTO and
// is unrelated to this canonical type).
//
// LocalPath is DETERMINISTIC: it is derived from a stable hash
// of SourceRef.URL (and DownloadSection when set), so two
// StageSource calls for the same SourceRef produce the same
// on-disk path. This gives natural cross-call dedupe at the
// filesystem level for callers that do not move/delete staged
// files between calls.
//
// IntermediateHash is the hex SHA-256 of the staged bytes,
// computed during the staging write so callers do not pay a
// second read pass for dedup checks (the prior inline-http
// path required the caller to hash the body themselves before
// dedup). The hash is "intermediate" because it is computed
// at staging time, before the asset is persisted; persistence
// owns the canonical asset_id and the authoritative
// media_assets.hash column.
//
// Bytes is the on-disk size after staging completed
// successfully.
//
// SourceID is the canonical locator that produced the staged
// file (the SourceRef.URL). It mirrors StagedAsset.SourceID
// and is kept for symmetry with the legacy StageSource output
// DTO.
//
// SourceRef is the full SourceRef that was passed to the
// stager (URL + DownloadSection + ForceKeyframes +
// MergeFormat). Callers use it to correlate the staged file
// with the originating request when the staged LocalPath is
// opaque (e.g., a SHA-256 hex digest).
//
// CleanedUp is set to true by the stager when CleanupStagedSource
// has removed the staged file. The MediaTransformer contract
// MUST NOT consume a StagedSource with CleanedUp=true (the
// LocalPath is dangling); the caller is responsible for the
// lifecycle (stage → use → cleanup) and must not stage-then-clean
// before passing the staged source to the transformer.
package asset

// StagedSource is the canonical output DTO of the
// assets.SourceStager port. It carries the staged LocalPath
// + IntermediateHash + Bytes + SourceID + SourceRef so the
// MediaTransformer can consume the staged bytes without
// re-downloading or re-hashing.
type StagedSource struct {
	// LocalPath is the deterministic on-disk path of the
	// staged file. Derived from a SHA-256 of SourceRef.URL
	// (and DownloadSection when set).
	LocalPath string
	// IntermediateHash is the hex SHA-256 of the staged
	// bytes, computed during the staging write.
	IntermediateHash string
	// Bytes is the on-disk size after staging.
	Bytes int64
	// SourceID is the canonical locator that produced the
	// staged file (the SourceRef.URL).
	SourceID string
	// SourceRef is the full SourceRef that was passed to
	// the stager.
	SourceRef SourceRef
	// CleanedUp is set to true by the stager when the
	// staged file has been removed via CleanupStagedSource.
	// A MediaTransformer MUST NOT consume a StagedSource
	// with CleanedUp=true.
	CleanedUp bool
}

// SourceRef identifies what to acquire / stage.
type SourceRef struct {
	URL             string
	DownloadSection string
	ForceKeyframes  bool
	MergeFormat     string
}
