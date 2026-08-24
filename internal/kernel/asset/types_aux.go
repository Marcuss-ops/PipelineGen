package asset

import (
	"context"
	"errors"
	"time"
)

// ArtifactStatus tracks the lifecycle of a stored artifact.
type ArtifactStatus string

const (
	ArtifactStaging     ArtifactStatus = "STAGING"
	ArtifactVerifying   ArtifactStatus = "VERIFYING"
	ArtifactReady       ArtifactStatus = "READY"
	ArtifactFailed      ArtifactStatus = "FAILED"
	ArtifactQuarantined ArtifactStatus = "QUARANTINED"
	ArtifactDeleted     ArtifactStatus = "DELETED"
)

// Artifact represents a generated or ingested binary (video, audio, image, etc.).
type Artifact struct {
	ID             string         `json:"id"`
	JobID          string         `json:"job_id,omitempty"`
	Kind           string         `json:"kind"`
	Status         ArtifactStatus `json:"status"`
	StorageBackend string         `json:"storage_backend"`
	StorageKey     string         `json:"storage_key"`
	SHA256         string         `json:"sha256"`
	SizeBytes      int64          `json:"size_bytes"`
	MimeType       string         `json:"mime_type"`
	DurationMs     int            `json:"duration_ms,omitempty"`
	Width          int            `json:"width,omitempty"`
	Height         int            `json:"height,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	VerifiedAt     *time.Time     `json:"verified_at,omitempty"`
	LastAccessedAt *time.Time     `json:"last_accessed_at,omitempty"`
}

// ── Step-1 typed migration (PR-IMAGES-AI-VS-NORMAL-PLAN, A1, July 2026) ──
//
// The legacy generation-style struct (14 fields including Description /
// Tags / DefaultWidth / DefaultHeight / AllowedProviders / AllowedModels
// + *bool tri-state Enabled pointer) was retired. Its slim 8-field
// replacement + Go type alias now lives in `types_style.go`
// (godlike/06 one-owner-per-fact).
//
// The legacy methods EffectiveSuffix / IsEnabled / Validate were
// retired along with the struct. Their semantics are:
//
//   - EffectiveSuffix: PromptSuffix preferred, Description fall-back.
//     Description is gone, so the only resolved suffix is PromptSuffix
//     itself. Callers that previously relied on the Description fall-
//     back must migrate to caller-supplied prompts OR pin a
//     PromptSuffix in the YAML.
//   - IsEnabled: *bool tri-state pointer (nil = enabled default).
//     Replaced by bool (silent-flip: absent defaults to false; the
//     existing config/generation_styles.yaml pins enabled explicitly
//     so production is transparent).
//   - Validate: fail-closed on missing DisplayName + missing both
//     PromptSuffix+Description. Migrated to StyleDefinition.Valid()
//     in types_style.go with the new fail-closed contract (DisplayName
//     + PromptSuffix; ID added).
//
// Package embedding defines the canonical contract for text-embedding
// generators consumed by the application layer. Concrete implementations
// live in internal/platform/embeddings/ (PR-D.5.1 split:
//
//   - application/<X>/ holds business logic and depends on Embedder
//   - platform/embeddings/ holds concrete PythonScriptEmbedder
//     (subprocess) and HTTPEmbedder (sidecar client) implementations.
//
// This separation enforces AGENTS.md architectural split: the
// application layer must NOT directly call os/exec or talk to a
// sidecar HTTP server — it depends on this interface and receives the
// concrete implementation at construction time from internal/app/
// composition root.

// EmbeddingResult is the canonical envelope returned by every concrete
// Embedder implementation. It carries the raw vector, observed model
// identity, model version, and dimensions so consumers (PayloadMapper,
// Qdrant index writer, etc.) can record provenance at write time.
//
// QDRANT-001 introduced this type; QDRANT-001b (July 2026) propagates
// it through PythonScriptEmbedder and HTTPTextEmbedder.
type EmbeddingResult struct {
	// Vector is the raw embedding as []float32.
	Vector []float32 `json:"embedding"`

	// Dimensions is the vector length (e.g. 768 for e5-base).
	Dimensions int `json:"dimensions"`

	// Model is the canonical model name (e.g. "intfloat/multilingual-e5-base").
	Model string `json:"model"`

	// ModelVersion is the model release or fine-tune label
	// (e.g. "<hf_revision>|<project_semver>").
	ModelVersion string `json:"model_version"`

	// ContractHash fingerprints the complete embedding contract (model,
	// revision, dimensions, preprocessing and distance), not just the model
	// label. It is supplied by the effective embedder runtime.
	ContractHash string `json:"contract_hash"`
}

// Embedder generates semantic embedding vectors for text. Both inputs
// and outputs ([]float32) match what the Python e5-base-multilingual
// script in bridges/generate_embedding.py returns; the sidecar HTTP
// adapter (infrastructures/embeddings/http.go) returns []float64 but
// the application layer normalises to []float32 via the wrapper.
//
// QDRANT-001b (July 2026): the return type has been promoted from
// ([]float32, error) to (EmbeddingResult, error) so callers receive
// the full provenance envelope (Model, ModelVersion, Dimensions)
// alongside the raw vector. Both PythonScriptEmbedder and
// HTTPTextEmbedder now return EmbeddingResult; adapters that need
// []float32 unwrap via result.Vector.
type Embedder interface {
	// Embed returns a vector representation of text. Empty text is
	// permitted and returns (EmbeddingResult{}, nil) so callers can
	// short-circuit on blank-input pipelines without an error.
	Embed(ctx context.Context, text string) (EmbeddingResult, error)
}

// Canonical domain errors for asset operations.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("asset already exists")
	ErrInvalidID     = errors.New("invalid asset ID")
	ErrSoftDeleted   = errors.New("asset is soft-deleted")
)

// Version represents a single version record for an asset.
type Version struct {
	ID            int64     `json:"id"`
	AssetID       string    `json:"asset_id"`
	VersionNumber int       `json:"version_number"`
	SourceURI     string    `json:"source_uri"`
	LegacyFileMD5 string    `json:"legacy_file_md5"`
	FileSizeBytes int64     `json:"file_size_bytes"`
	MimeType      string    `json:"mime_type"`
	MetadataJSON  string    `json:"metadata_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// AssetExecutionResult is a unified result type for asset processing across
// all modules. It combines fields from BatchItem, RunTagItem, ExtractItem,
// AssetResult, FinalizeResult. It is a DTO passed between pipeline stages
// rather than a stored entity — see processing_types.go for the persistent
// ProcessingRecord shape.
type AssetExecutionResult struct {
	ID            string `json:"id,omitempty"`
	Source        string `json:"source,omitempty"`     // e.g. "youtube", "artlist", "voiceover"
	MediaType     string `json:"media_type,omitempty"` // e.g. "video", "audio", "image"
	Filename      string `json:"filename,omitempty"`
	LocalPath     string `json:"local_path,omitempty"`
	DriveLink     string `json:"drive_link,omitempty"`
	DownloadLink  string `json:"download_link,omitempty"`
	LegacyFileMD5 string `json:"legacy_file_md5,omitempty"`
	Status        string `json:"status,omitempty"` // e.g. "processed", "skipped_existing", "failed"
	Error         string `json:"error,omitempty"`
}

// Filter defines query parameters for listing assets.
//
// WorkspaceID + IsAdmin (QDRANT-001 closure): the Filter carries a
// tenant-isolation predicate. Repositories that back the multi-tenant
// media_assets table MUST translate this into a SQL clause:
//
//	AND (workspace_id = ? OR ? = true)   // admin bypass in SQL is OK but discouraged
//
// or simpler in Go:
//
//	if filter.WorkspaceID != "" && !filter.IsAdmin { conds += workspace_id = ? }
//
// The IsAdmin bit is preferred in code over the SQL OR: it keeps the
// query plan simple and lets repos log which path was taken. The
// caller (composition root / use case) is the only place that knows
// whether the current principal is admin.
type Filter struct {
	Source       string   `json:"source,omitempty"`
	MediaType    string   `json:"media_type,omitempty"`
	States       []string `json:"states,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	ExcludeIDs   []string `json:"exclude_ids,omitempty"`
	HasEmbedding *bool    `json:"has_embedding,omitempty"`
	IsFolder     *bool    `json:"is_folder,omitempty"`
	Category     string   `json:"category,omitempty"`
	Group        string   `json:"group_name,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`

	// WorkspaceID, when non-empty AND IsAdmin is false, restricts
	// results to rows where workspace_id = ?. Empty means "no
	// workspace filter" (legacy behaviour, used by internal
	// admin/maintenance tooling that scans the whole catalog).
	// QDRANT-001 (June 2026): added alongside the workspace_id
	// hydration in mediasearchReadAdapter.
	WorkspaceID string `json:"workspace_id,omitempty"`

	// IsAdmin, when true, skips the workspace_id WHERE predicate.
	// Auth context is the only place this flag is set; ordinary
	// service-layer callers leave it false and trust the workspace.
	IsAdmin bool `json:"is_admin,omitempty"`
}
