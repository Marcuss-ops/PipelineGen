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

// GenerationStyle defines a reusable prompt style for AI generation.
// Used by internal/application/assets/generation/style_registry.go, which loads
// style definitions from config/generation_styles.yaml.
//
// This type was moved here from the now-deleted internal/domain/media/styles.go
// during Wave-14. The domain/media package previously held these types
// alongside type aliases; since only the aliases were meant to be deleted,
// the GenerationStyle types are preserved in the canonical domain/asset package.
type GenerationStyle struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// GenerationStyles is a container for multiple styles (YAML on-disk shape).
type GenerationStyles struct {
	Styles []GenerationStyle `yaml:"styles" json:"styles"`
}

// Package embedding defines the canonical contract for text-embedding
// generators consumed by the application layer. Concrete implementations
// live in internal/infrastructure/embeddings/ (PR-D.5.1 split:
//
//   - application/<X>/ holds business logic and depends on Embedder
//   - infrastructure/embeddings/ holds concrete PythonScriptEmbedder
//     (subprocess) and HTTPEmbedder (sidecar client) implementations.
//
// This separation enforces AGENTS.md architectural split: the
// application layer must NOT directly call os/exec or talk to a
// sidecar HTTP server — it depends on this interface and receives the
// concrete implementation at construction time from internal/app/
// composition root.

// Embedder generates semantic embedding vectors for text. Both inputs
// and outputs ([]float32) match what the Python e5-base-multilingual
// script in bridges/generate_embedding.py returns; the sidecar HTTP
// adapter (infrastructures/embeddings/http.go) returns []float64 but
// the application layer normalises to []float32 via the wrapper.
type Embedder interface {
	// Embed returns a vector representation of text. Empty text is
	// permitted and returns (nil, nil) so callers can short-circuit on
	// blank-input pipelines without an error.
	Embed(ctx context.Context, text string) ([]float32, error)
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
	FileHash      string    `json:"file_hash"`
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
	ID           string `json:"id,omitempty"`
	Source       string `json:"source,omitempty"`     // e.g. "youtube", "artlist", "voiceover"
	MediaType    string `json:"media_type,omitempty"` // e.g. "video", "audio", "image"
	Filename     string `json:"filename,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Status       string `json:"status,omitempty"` // e.g. "processed", "skipped_existing", "failed"
	Error        string `json:"error,omitempty"`
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
