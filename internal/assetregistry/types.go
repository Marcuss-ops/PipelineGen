// Package assetregistry provides a generic, database-backed asset store
// with content-addressed deduplication, provenance tracking, and
// resolver-based content access.
//
// PR-7: Asset Registry generico e database-backed.
package assetregistry

import (
	"context"
	"io"
	"time"
)

// ── Asset ──────────────────────────────────────────────────────────────

// Kind represents the asset category.
type Kind string

const (
	KindVoiceover   Kind = "voiceover"
	KindSceneImage  Kind = "scene_image"
	KindStockClip   Kind = "stock_clip"
	KindMusic       Kind = "music"
	KindFont        Kind = "font"
	KindSubtitle    Kind = "subtitle"
	KindThumbnail   Kind = "thumbnail"
)

// Status represents the asset lifecycle state.
type Status string

const (
	StatusPending Status = "PENDING"
	StatusReady   Status = "READY"
	StatusFailed  Status = "FAILED"
	StatusDeleted Status = "DELETED"
)

// Role represents the asset's role within a job.
type Role string

const (
	RoleVoiceover   Role = "voiceover"
	RoleSceneImage  Role = "scene_image"
	RoleStockClip   Role = "stock_clip"
	RoleMusic       Role = "music"
	RoleFont        Role = "font"
	RoleSubtitle    Role = "subtitle"
	RoleThumbnail   Role = "thumbnail"
)

// Asset is the canonical asset record.
type Asset struct {
	AssetID        string     `json:"asset_id"`
	Kind           Kind       `json:"kind"`
	Status         Status     `json:"status"`
	SHA256         string     `json:"sha256"`
	StorageBackend string     `json:"storage_backend"`
	StorageKey     string     `json:"storage_key"`
	MimeType       string     `json:"mime_type,omitempty"`
	SizeBytes      int64      `json:"size_bytes"`
	DurationMs     int        `json:"duration_ms,omitempty"`
	Width          int        `json:"width,omitempty"`
	Height         int        `json:"height,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// AssetSource records the provenance of an asset.
type AssetSource struct {
	SourceID        string    `json:"source_id"`
	AssetID         string    `json:"asset_id"`
	SourceType      string    `json:"source_type"`
	SourceReference string    `json:"source_reference"`
	SourceAccountID string    `json:"source_account_id,omitempty"`
	ImportedAt      time.Time `json:"imported_at"`
}

// JobAsset links an asset to a job with a role and ordinal.
type JobAsset struct {
	JobID    string    `json:"job_id"`
	AssetID  string    `json:"asset_id"`
	Role     Role      `json:"role"`
	Ordinal  int       `json:"ordinal"`
	Required bool      `json:"required"`
	CreatedAt time.Time `json:"created_at"`
}

// ── URI Reference ──────────────────────────────────────────────────────

// Reference is a parsed asset URI like "velox-asset://ast_01ARZ3..."
type Reference struct {
	Scheme  string // velox-asset, drive, https, file
	AssetID string
	Raw     string // original URI string
}

// ── Resolver Registry ──────────────────────────────────────────────────

// Resolver opens asset content for a given URI scheme.
type Resolver interface {
	Scheme() string
	Open(ctx context.Context, ref Reference) (io.ReadCloser, error)
	Stat(ctx context.Context, ref Reference) (ObjectInfo, error)
}

// ObjectInfo holds metadata about a resolved asset.
type ObjectInfo struct {
	SHA256    string
	SizeBytes int64
	MimeType  string
}

// ResolverRegistry manages scheme-to-resolver mappings.
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

// ── BindingExtractor Registry ──────────────────────────────────────────

// Binding represents a requested asset for a job payload.
type Binding struct {
	Role     Role   `json:"role"`
	Ordinal  int    `json:"ordinal"`
	Required bool   `json:"required"`
	Source   Reference `json:"source"`
}

// ResolvedBinding pairs a binding with its resolved asset ID.
type ResolvedBinding struct {
	Binding Binding `json:"binding"`
	AssetID string  `json:"asset_id"`
}

// BindingExtractor extracts asset bindings from a job payload
// and rewrites the payload with resolved asset references.
type BindingExtractor interface {
	JobType() string
	Extract(payload map[string]any) ([]Binding, error)
	Rewrite(payload map[string]any, resolved []ResolvedBinding) error
}

// BindingExtractorRegistry manages job-type to extractor mappings.
// Multiple extractors can be registered for the same job type.
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

// ── AssetService Interface ─────────────────────────────────────────────

// CreateInput carries the data needed to register an asset.
type CreateInput struct {
	Kind        Kind
	SourceType  string
	SourceRef   string
	AccountID   string
	MimeType    string
	DurationMs  int
	Width       int
	Height      int
	Content     io.Reader
}

// CreateResult holds the outcome of asset creation.
type CreateResult struct {
	Asset   *Asset
	SHA256  string
	NewlyCreated bool
}

// ArtifactWriter writes asset content to the storage backend.
type ArtifactWriter interface {
	Put(ctx context.Context, key string, r io.Reader) (string, int64, error)
}

// AssetRepository is the persistence contract for assets.
type AssetRepository interface {
	CreateAsset(ctx context.Context, a *Asset) error
	GetAsset(ctx context.Context, assetID string) (*Asset, error)
	GetAssetBySHA256(ctx context.Context, sha256 string) (*Asset, error)
	UpdateStatus(ctx context.Context, assetID string, status Status) error
	TouchAccess(ctx context.Context, assetID string) error
	CreateSource(ctx context.Context, s *AssetSource) error
	UpsertJobAsset(ctx context.Context, ja *JobAsset) error
	ListJobAssets(ctx context.Context, jobID string) ([]JobAsset, error)
	GetJobAsset(ctx context.Context, jobID, assetID string) (*JobAsset, error)
}

// ── Path Safety Helpers ────────────────────────────────────────────────

// CleanAssetPath normalizes a file URI or path for safe access.
func CleanAssetPath(raw string) string {
	// Handle file:// prefix
	if len(raw) > 7 && raw[:7] == "file://" {
		raw = raw[7:]
	}
	// Clean the path (removes .., redundant separators)
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
	// Exact match or prefix with separator
	return path == dir || (len(path) > len(dir) && path[:len(dir)] == dir && path[len(dir)] == '/')
}

// splitPath splits a path into segments.
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
