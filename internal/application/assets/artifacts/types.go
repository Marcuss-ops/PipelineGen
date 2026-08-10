// Package artifacts provides content-addressed artifact storage and lifecycle
// management for PipelineGen. Artifacts are the output of render/processing
// jobs (videos, audio, thumbnails). They flow through a state machine:
// STAGING → VERIFYING → READY/FAILED/QUARANTINED.
//
// Storage is content-addressed via SHA-256: the canonical key for any blob
// is artifacts/sha256/xx/xxxx... where xx is the first two hex chars.
//
// PR3 (unify artifact registry): provenance tracking (ArtifactSource),
// job-artifact linking (JobArtifact), URI resolution (ResolverRegistry),
// and job-payload binding extraction (BindingExtractorRegistry) now live
// here — the legacy internal/assetregistry package is being absorbed.
package artifacts

import (
	"context"
	"io"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Status ─────────────────────────────────────────────────────────

// Status represents the canonical artifact states.
type Status string

const (
	StatusStaging   Status = "STAGING"
	StatusVerifying Status = "VERIFYING"
	// The SQLite artifacts table uses READY as the durable state. Keep the
	// application-facing Staged name while retaining that persisted value for
	// compatibility with migration 051 and existing resolvers.
	StatusStaged      Status = "READY"
	StatusFailed      Status = "FAILED"
	StatusQuarantined Status = "QUARANTINED"
	StatusDeleted     Status = "DELETED"

	// StatusReady is the backward-compatible alias for StatusStaged.
	StatusReady Status = StatusStaged
)

// ── Artifact ───────────────────────────────────────────────────────

// Artifact is the domain model for a stored artifact.
type Artifact struct {
	ID             string `json:"id"`
	JobID          string `json:"job_id,omitempty"`
	Kind           string `json:"kind"` // video, audio, thumbnail, image
	Status         Status `json:"status"`
	StorageBackend string `json:"storage_backend"` // local, s3
	StorageKey     string `json:"storage_key"`     // canonical blob path
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	MimeType       string `json:"mime_type"`
	// Media-specific metadata (PR3: carried in from assetregistry.Asset).
	DurationMs int `json:"duration_ms,omitempty"`
	Width      int `json:"width,omitempty"`
	Height     int `json:"height,omitempty"`
	// Timestamps.
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
}

// ── Provenance ─────────────────────────────────────────────────────

// ArtifactSource records the provenance of an artifact (PR3: was assetregistry.AssetSource).
type ArtifactSource struct {
	SourceID        string    `json:"source_id"`
	ArtifactID      string    `json:"artifact_id"`
	SourceType      string    `json:"source_type"`
	SourceReference string    `json:"source_reference"`
	SourceAccountID string    `json:"source_account_id,omitempty"`
	ImportedAt      time.Time `json:"imported_at"`
}

// ── Job Linking ────────────────────────────────────────────────────

// JobArtifact links an artifact to a job with a role and ordinal (PR3: was assetregistry.JobAsset).
type JobArtifact struct {
	JobID      string    `json:"job_id"`
	ArtifactID string    `json:"artifact_id"`
	Role       string    `json:"role"`
	Ordinal    int       `json:"ordinal"`
	Required   bool      `json:"required"`
	CreatedAt  time.Time `json:"created_at"`
}

// ── BlobStore ──────────────────────────────────────────────────────

// BlobStore is the abstraction over content-addressed blob drive.
// Implementations: LocalBlobStore (filesystem), S3BlobStore (future).
type BlobStore interface {
	// Stage writes the contents of r to a temporary staging area and returns
	// a staging key. The caller should pass this key to VerifyAndPromote.
	Stage(ctx context.Context, hint string) (StagingWriter, error)

	// VerifyAndPromote computes SHA-256 of the staged blob, moves it to its
	// canonical content-addressed location, and returns the canonical key.
	// Returns an error if the hash doesn't match an optional expected value.
	VerifyAndPromote(ctx context.Context, stagingKey string, expectedSHA256 string) (PromoteResult, error)

	// Open returns a reader for the blob at the given canonical key.
	Open(ctx context.Context, storageKey string) (io.ReadCloser, error)

	// Delete removes a blob from drive.
	Delete(ctx context.Context, storageKey string) error

	// Stat returns metadata about a stored blob.
	Stat(ctx context.Context, storageKey string) (BlobInfo, error)
}

// StagingWriter provides a writable handle during the staging phase.
// The caller must call Close() to finalize the write.
type StagingWriter interface {
	io.WriteCloser
	// Key returns the staging key for this upload.
	Key() string
}

// PromoteResult holds the outcome of staging → canonical promotion.
type PromoteResult struct {
	StorageKey string
	SHA256     string
	SizeBytes  int64
}

// BlobInfo holds metadata about a stored blob.
type BlobInfo struct {
	SHA256    string
	SizeBytes int64
	Exists    bool
}

// ── Repository ─────────────────────────────────────────────────────

// Repository is the persistence contract for artifact metadata.
type Repository interface {
	// Core CRUD.
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status Status, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]Artifact, error)

	// Provenance (PR3: from assetregistry.AssetRepository).
	CreateSource(ctx context.Context, s *ArtifactSource) error

	// Job-artifact linking (PR3: from assetregistry.AssetRepository).
	UpsertJobArtifact(ctx context.Context, ja *JobArtifact) error
	ListJobArtifacts(ctx context.Context, jobID string) ([]JobArtifact, error)
	GetJobArtifact(ctx context.Context, jobID, artifactID string) (*JobArtifact, error)

	// TouchAccess updates last_accessed_at for an artifact (PR3: from assetregistry).
	TouchAccess(ctx context.Context, artifactID string) error
}

// ── URI Resolution ─────────────────────────────────────────────────

// Reference is a parsed asset URI like "velox-asset://ast_01ARZ3..."
type Reference struct {
	Scheme     string // velox-asset, drive, https, file
	ArtifactID string
	Raw        string // original URI string
}

// Resolver opens artifact content for a given URI scheme.
type Resolver interface {
	Scheme() string
	Open(ctx context.Context, ref Reference) (io.ReadCloser, error)
	Stat(ctx context.Context, ref Reference) (ObjectInfo, error)
}

// ObjectInfo holds metadata about a resolved artifact.
type ObjectInfo struct {
	SHA256    string
	SizeBytes int64
	MimeType  string
}

// ResolverRegistry manages scheme-to-resolver mappings (PR3: from assetregistry).
type ResolverRegistry struct {
	resolvers map[string]Resolver
}

// NewResolverRegistry creates an empty resolver registry.
func NewResolverRegistry() *ResolverRegistry {
	return &ResolverRegistry{resolvers: make(map[string]Resolver)}
}

// Register adds a resolver for a URI scheme.
func (r *ResolverRegistry) Register(resolver Resolver) {
	r.resolvers[resolver.Scheme()] = resolver
}

// Get returns the resolver for a scheme, or nil.
func (r *ResolverRegistry) Get(scheme string) Resolver {
	return r.resolvers[scheme]
}

// ── Binding Extraction ─────────────────────────────────────────────

// Binding represents a requested artifact for a job payload (PR3: from assetregistry).
type Binding struct {
	Role     Role      `json:"role"`
	Ordinal  int       `json:"ordinal"`
	Required bool      `json:"required"`
	Source   Reference `json:"source"`
}

// Role represents the artifact's role within a job.
type Role string

// ResolvedBinding pairs a binding with its resolved artifact ID.
type ResolvedBinding struct {
	Binding    Binding `json:"binding"`
	ArtifactID string  `json:"artifact_id"`
}

// BindingExtractor extracts artifact bindings from a job payload
// and rewrites the payload with resolved artifact references.
type BindingExtractor interface {
	JobType() string
	Extract(payload map[string]any) ([]Binding, error)
	Rewrite(payload map[string]any, resolved []ResolvedBinding) error
}

// BindingExtractorRegistry manages job-type to extractor mappings (PR3: from assetregistry).
type BindingExtractorRegistry struct {
	extractors []BindingExtractor
	byJobType  map[string][]BindingExtractor
}

// NewBindingExtractorRegistry creates an empty extractor registry.
func NewBindingExtractorRegistry() *BindingExtractorRegistry {
	return &BindingExtractorRegistry{
		extractors: make([]BindingExtractor, 0),
		byJobType:  make(map[string][]BindingExtractor),
	}
}

// Register adds an extractor for a job type.
func (e *BindingExtractorRegistry) Register(ext BindingExtractor) {
	e.extractors = append(e.extractors, ext)
	e.byJobType[ext.JobType()] = append(e.byJobType[ext.JobType()], ext)
}

// GetForJobType returns all extractors that match a job type.
func (e *BindingExtractorRegistry) GetForJobType(jobType string) []BindingExtractor {
	return e.byJobType[jobType]
}

// ── Service Input/Output ───────────────────────────────────────────

// CreateInput is the input for CreateAndVerify.
type CreateInput struct {
	ID             string
	JobID          string
	Kind           string
	MimeType       string
	Reader         io.Reader
	ExpectedSHA256 string // optional; empty = no pre-verification
}

// ResolveAndRegisterInput carries the data needed to register an artifact
// with content-addressed deduplication and provenance tracking.
// Ported from assetregistry.CreateInput (PR3 merge).
type ResolveAndRegisterInput struct {
	Kind       string
	SourceType string
	SourceRef  string
	AccountID  string
	MimeType   string
	DurationMs int
	Width      int
	Height     int
	Content    io.Reader
}

// ResolveAndRegisterResult holds the outcome of artifact registration.
type ResolveAndRegisterResult struct {
	Artifact     *Artifact
	SHA256       string
	NewlyCreated bool
}

// ── Path Safety Helpers (ported from assetregistry) ───────────────

// CleanArtifactPath normalizes a file URI or path for safe access.
func CleanArtifactPath(raw string) string {
	// Handle file:// prefix
	if len(raw) > 7 && raw[:7] == "file://" {
		raw = raw[7:]
	}
	return cleanFilePath(raw)
}

// IsSafePath checks whether a cleaned path falls within any of the allowed directories.
func IsSafePath(allowedDirs []string, cleanPath string) bool {
	for _, dir := range allowedDirs {
		if isUnderDir(cleanPath, dir) {
			return true
		}
	}
	return false
}

// cleanFilePath removes path traversal and normalizes.
func cleanFilePath(p string) string {
	// Remove leading ./
	for len(p) > 1 && p[0] == '.' && p[1] == '/' {
		p = p[2:]
	}
	// Resolve .. segments
	segments := splitPath(p)
	var cleaned []string
	for _, seg := range segments {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(cleaned) > 0 {
				cleaned = cleaned[:len(cleaned)-1]
			}
		default:
			cleaned = append(cleaned, seg)
		}
	}
	result := ""
	for i, seg := range cleaned {
		if i > 0 {
			result += "/"
		}
		result += seg
	}
	return result
}

// isUnderDir checks if a path is within a given directory.
func isUnderDir(path, dir string) bool {
	path = cleanFilePath(path)
	dir = cleanFilePath(dir)
	return path == dir || (len(path) > len(dir) && path[:len(dir)] == dir && path[len(dir)] == '/')
}

// MediaRecord is a legacy unified media record absorbed from media/assetregistry.
type MediaRecord struct {
	ID                  string
	Name                string
	Filename            string
	Source              string
	Category            string
	MediaType           string
	ExternalURL         string
	FolderID            string
	FolderPath          string
	Group               string
	LocalPath           string
	DriveLink           string
	DriveFileID         string
	DownloadLink        string
	FileHash            string
	ContentHash         string
	Metadata            string
	Duration            int
	Tags                []string
	Status              string
	PublishStatus       asset.AssetPublishStatus
	Error               string
	SourceID            string
	Subfolder           string
	PHash               string
	VisualEmbeddingJSON string
}

type FinalizeOptions struct {
	RequireLocal bool
	RequireHash  bool
	RequireDrive bool
	VerifyDB     bool
}

type FinalizeResult struct {
	OK            bool
	Status        string
	DBSaved       bool
	LocalExists   bool
	DriveUploaded bool
	Error         string
	Record        *MediaRecord
}

func splitPath(p string) []string {
	var segs []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			segs = append(segs, p[start:i])
			start = i + 1
		}
	}
	if start < len(p) {
		segs = append(segs, p[start:])
	}
	return segs
}
