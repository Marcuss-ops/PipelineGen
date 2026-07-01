// Package asset — canonical media-type taxonomy + asset location/domain DTOs
// (Wave C / Phase 3 slim).
//
// Phase 3 (Wave C / Blocco 1 Asset SSOT, June 2026): the 6 SQL
// receivers that used to live here (UpsertLocation/GetPrimaryLocation/
// ListLocationsByAsset/SetPrimaryLocation/DeleteLocation/DeleteAllLocations)
// + the scanLocation helper + the locationRepositoryAdapter struct +
// the LocationRepository() factory were relocated to Local infra at
// internal/infrastructure/database/sqlite/assets/location_queries.go,
// reachable via HYBRID-embed promotion through the legacy
// *AssetStoreSQLite struct.
//
// This file now carries ONLY the canonical type surface: MediaType enum
// + LocationKind enum + the Location / Delivery* / Resolver / Details /
// Summary / Subject / Image* / CategoryChannel / SearchQuery DTOs.
// NO SQL primitives, NO `database/sql` import.
package asset

import (
	"context"
	"time"
)

// MediaType classifies the content type of an asset (stock footage, video
// clip, image, audio/voiceover, document, generated video, sound effect).
//
// History (Wave-14 cut-over, Jun 2026):
//
//   - Phase 1: declared locally in internal/domain/asset/asset_types.go as a
//     named string type.
//   - Phase 3 (Wave-15 follow-up): replaced by `type MediaType = media.MediaType`
//     alias to converge with the now-deleted internal/domain/media/media.go (where the
//     canonical const set lived). Callers did not notice because the alias
//     was transparent — `asset.MediaType`, `asset.MediaTypeClip`, etc. all
//     worked either way.
//   - Wave-14: internal/domain/media/ was deleted. This file now declares
//     MediaType natively and re-hosts the const set that previously lived
//     in media.media.go. The Phase 3 alias was the correct bridge while
//     media co-existed; now that media is gone, the alias itself is gone.
//
// Naming rationale: the type remains `MediaType` (so existing call sites
// `asset.MediaType(...)`, `asset.MediaType("clip")`, `*Asset{MediaType:
// asset.MediaTypeClip}` continue to compile unchanged).
type MediaType string

const (
	// MediaTypeStock refers to stock footage.
	MediaTypeStock MediaType = "stock"
	// MediaTypeClip refers to a video clip.
	MediaTypeClip MediaType = "clip"
	// MediaTypeImage refers to an image.
	MediaTypeImage MediaType = "image"
	// MediaTypeAudio refers to an audio file (voiceover).
	MediaTypeAudio MediaType = "audio"
	// MediaTypeDocument refers to a document (Google Doc).
	MediaTypeDocument MediaType = "document"
	// MediaTypeImageVideo is for generated video files.
	MediaTypeImageVideo MediaType = "image_video"
	// MediaTypeSoundEffect is for extracted sound effect audio clips.
	MediaTypeSoundEffect MediaType = "sound_effect"
	// MediaTypeScript identifies script-to-asset catalog entries emitted
	// by script_assets-family providers. Distinct from video/audio/image
	// because script output is a textual artifact (not a media asset on
	// disk); downstream composition fetches the resolved assets
	// separately through MediaTypeClip / Image / Audio handlers.
	MediaTypeScript MediaType = "script"
)

// IsValid reports whether the MediaType matches a known constant.
func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument, MediaTypeImageVideo, MediaTypeSoundEffect, MediaTypeScript:
		return true
	}
	return false
}

// LocationKind categorises where a Location physically lives.
type LocationKind string

const (
	LocationKindLocal         LocationKind = "local"
	LocationKindDrive         LocationKind = "drive"
	LocationKindObjectStorage LocationKind = "object_storage"
)

// Location is the canonical domain entity for an asset location record.
type Location struct {
	ID            int64        `json:"id"`
	AssetID       string       `json:"asset_id"`
	LocationKind  LocationKind `json:"location_kind"`
	URI           string       `json:"uri"`
	ExternalID    string       `json:"external_id,omitempty"`
	AccessURL     string       `json:"access_url,omitempty"`
	DownloadURL   string       `json:"download_url,omitempty"`
	MimeType      string       `json:"mime_type"`
	FileSizeBytes int64        `json:"file_size_bytes"`
	FileHash      string       `json:"file_hash"`
	IsPrimary     bool         `json:"is_primary"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Resolver is the canonical interface for resolving asset destinations.
// It unifies drive destination, local destination, and other output targets.
type Resolver interface {
	// Resolve returns the destination URI and metadata for an asset.
	Resolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
}

// ResolveRequest contains the information needed to resolve a destination.
type ResolveRequest struct {
	Source          string // e.g. "youtube", "artlist", "voiceover"
	Group           string // Name of the group folder
	FolderID        string // explicit folder ID (overrides group)
	FolderPath      string // optional path info
	SubfolderName   string // Name of the subfolder or video ID
	CreateSubfolder bool   // whether to create subfolder if not exists
	AssetID         string
	AssetType       string // "clip", "stock", "artlist", "image", "voiceover"
	ProjectID       string
	FolderName      string
	// StyleGroup (PR-VO-B2, June 2026) is forwarded through the
	// resolver so the per-call resolution can record the original
	// style-cohort selector against the resolved destination. The
	// resolver is a pure plumbing layer (it does NOT generate
	// StyleGroup on its own); the value is set by the caller and
	// echoed back through ResolvedDestination in the caller-supplied
	// pattern (resolveDestination does `resolved.StyleGroup =
	// dest.StyleGroup` after the resolver returns). ResolveResult
	// deliberately does NOT carry it: the resolver is a folder-
	// mapping concern, not a style-routing concern.
	StyleGroup string
	Metadata   map[string]any
}

// ResolveResult contains the resolved destination information.
type ResolveResult struct {
	LocationKind string // "drive", "local", "s3", etc.
	URI          string // Drive folder ID, local path, etc.
	FolderID     string // Drive folder ID
	FolderPath   string // Full folder path
	DriveLink    string // Drive web link
	Extra        map[string]any
}

// DeliveryStatus tracks the lifecycle of a delivery attempt.
type DeliveryStatus string

const (
	DeliveryPending     DeliveryStatus = "PENDING"
	DeliveryLeased      DeliveryStatus = "LEASED"
	DeliveryRunning     DeliveryStatus = "RUNNING"
	DeliveryRetryWait   DeliveryStatus = "RETRY_WAIT"
	DeliverySucceeded   DeliveryStatus = "SUCCEEDED"
	DeliveryFailed      DeliveryStatus = "FAILED"
	DeliveryBlockedAuth DeliveryStatus = "BLOCKED_AUTH"
	DeliveryCancelled   DeliveryStatus = "CANCELLED"
)

// Delivery represents an attempt to deliver an artifact to a destination.
type Delivery struct {
	ID               string         `json:"id"`
	ArtifactID       string         `json:"artifact_id"`
	DestinationID    string         `json:"destination_id"`
	Provider         string         `json:"provider"`
	Status           DeliveryStatus `json:"status"`
	AttemptCount     int            `json:"attempt_count"`
	MaxAttempts      int            `json:"max_attempts"`
	NextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
	LockedBy         string         `json:"locked_by,omitempty"`
	LockedUntil      *time.Time     `json:"locked_until,omitempty"`
	RemoteID         string         `json:"remote_id,omitempty"`
	RemoteURL        string         `json:"remote_url,omitempty"`
	LastErrorCode    string         `json:"last_error_code,omitempty"`
	LastErrorMessage string         `json:"last_error_message,omitempty"`
	IdempotencyKey   string         `json:"idempotency_key,omitempty"`
	StorageKey       string         `json:"storage_key,omitempty"`
	SHA256           string         `json:"sha256,omitempty"`
	SizeBytes        int64          `json:"size_bytes,omitempty"`
	MimeType         string         `json:"mime_type,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
}

// DeliveryDestination is a configured delivery target.
type DeliveryDestination struct {
	DestinationID string `json:"destination_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Enabled       bool   `json:"enabled"`
	ConfigJSON    string `json:"config_json,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// Details is the full representation of an asset including all sub-entities.
type Details struct {
	Asset      *Asset              `json:"asset"`
	Locations  []*Location         `json:"locations,omitempty"`
	Processing []*ProcessingRecord `json:"processing,omitempty"`
	Versions   []*Version          `json:"versions,omitempty"`
}

// LocalLocation returns the local Location for the asset.
func (d *Details) LocalLocation() *Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == LocationKindLocal {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == LocationKindLocal {
			return loc
		}
	}
	return nil
}

// DriveLocation returns the drive Location for the asset.
func (d *Details) DriveLocation() *Location {
	if d == nil {
		return nil
	}
	for _, loc := range d.Locations {
		if loc == nil {
			continue
		}
		if loc.IsPrimary && loc.LocationKind == LocationKindDrive {
			return loc
		}
	}
	for _, loc := range d.Locations {
		if loc != nil && loc.LocationKind == LocationKindDrive {
			return loc
		}
	}
	return nil
}

// ProcessingStep returns a pointer to the processing record for the given step name.
func (d *Details) ProcessingStep(step string) *ProcessingRecord {
	if d == nil {
		return nil
	}
	for _, p := range d.Processing {
		if p != nil && p.Step == step {
			return p
		}
	}
	return nil
}

// Summary is a lightweight projection of an asset for list views.
type Summary struct {
	ID             string         `json:"id"`
	Source         Source         `json:"source"`
	Name           string         `json:"name"`
	Filename       string         `json:"filename"`
	MediaType      MediaType      `json:"media_type"`
	Category       string         `json:"category"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	PrimaryURI     string         `json:"primary_uri,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// ── Subjects ────────────────────────────────────────────────────────

// Subject represents a known entity (person, place, thing) for image generation.
type Subject struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	WikidataID  string    `json:"wikidata_id,omitempty"`
	Aliases     []string  `json:"aliases"` // Stored as JSON in the DB.
	Category    string    `json:"category"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ── Image Assets ────────────────────────────────────────────────────

// ImageAsset represents an image stored in the asset index.
//
// SubjectID is a string (TEXT in the database) holding the Subject's slug.
// SlugID is an explicit alias used by some callers and stays equivalent to
// SubjectID in practice; preserve both for backward compat.
//
// Step 1 (July 2026): Origin and Provider fields added for territory
// separation (retrieved vs generated vs uploaded).
type ImageAsset struct {
	ID           int64     `json:"id"`
	Hash         string    `json:"hash"`
	SubjectID    string    `json:"subject_id"`        // TEXT in database (slug)
	SlugID       string    `json:"slug_id,omitempty"` // Alias for internal logic
	PathRel      string    `json:"path_rel"`
	SourceURL    string    `json:"source_url"`
	License      string    `json:"license"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int64     `json:"size_bytes"`
	QualityScore int       `json:"quality_score"`
	Description  string    `json:"description"`
	DriveFileID  string    `json:"drive_file_id,omitempty"`
	Status       string    `json:"status,omitempty"`
	Error        string    `json:"error,omitempty"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
	Tags         []string  `json:"tags,omitempty"`

	// Origin classifies the image provenance: retrieved, generated, or uploaded.
	Origin ImageOrigin `json:"origin,omitempty"`

	// Provider identifies the specific source/generator (wikipedia, flux, etc.).
	Provider ImageProvider `json:"provider,omitempty"`
}

// ImageUsage tracks usage of an image inside a rendered video.
type ImageUsage struct {
	ID      int64     `json:"id"`
	ImageID int64     `json:"image_id"`
	VideoID string    `json:"video_id"`
	UsedAt  time.Time `json:"used_at"`
}

// ImageTag represents a tag associated with an image.
type ImageTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ── Categories ──────────────────────────────────────────────────────

// CategoryChannel represents a YouTube channel subscribed to a specific
// category/folder. Each Drive folder category (e.g. "boxe", "rap",
// "comedy") can have multiple channels; when the channel monitor runs it
// checks these channels and downloads clips that match the category.
type CategoryChannel struct {
	ID              string `json:"id"`
	Category        string `json:"category"`
	ChannelURL      string `json:"channel_url"`
	ChannelName     string `json:"channel_name,omitempty"`
	Keywords        string `json:"keywords,omitempty"` // JSON array — title-level keyword match.
	MinViews        int    `json:"min_views,omitempty"`
	MaxClipDuration int    `json:"max_clip_duration,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`

	// SemanticKeywords enables transcript-level content matching.
	// JSON array of themes/topics (e.g. '["health","dieta","fitness"]').
	// When set, monitor downloads subtitles and asks Ollama for relevance score.
	SemanticKeywords string `json:"semantic_keywords,omitempty"`

	// MinSemanticScore is the minimum Ollama confidence (0-100) to accept
	// a match. Default 60. Higher = fewer but more relevant clips.
	MinSemanticScore int `json:"min_semantic_score,omitempty"`

	// PlaylistEnd overrides the global playlist_end for retroactive full-scan.
	// 0 = all videos, -1 = use global default, >0 = specific count.
	PlaylistEnd int `json:"playlist_end,omitempty"`

	// CheckInterval overrides the global check interval for this channel.
	// Format: "7d", "24h", "30m". Default "7d".
	CheckInterval string `json:"check_interval,omitempty"`

	// MaxVideosPerRun limits how many videos are processed per check. 0 = no limit.
	MaxVideosPerRun int `json:"max_videos_per_run,omitempty"`

	// Priority: 1=hot (check 2x), 2=normal, 3=cold (check 0.5x). Default 2.
	Priority int `json:"priority,omitempty"`

	// LookbackDays limits the scan to videos published within N days. 0 = no limit.
	LookbackDays int `json:"lookback_days,omitempty"`

	// MaxSegments limits how many segments to extract per video. Default 2.
	MaxSegments int `json:"max_segments,omitempty"`

	// SegmentPrompt is a custom prompt for the AI segment finder.
	SegmentPrompt string `json:"segment_prompt,omitempty"`

	// PR 2 (June 2026): monitoring state columns. category_channels is now
	// the single source of truth; the JSON fallback is removed.
	Enabled       int    `json:"enabled,omitempty"`
	NextCheckAt   string `json:"next_check_at,omitempty"`
	LastCheckedAt string `json:"last_checked_at,omitempty"`

	// PR 3 (June 2026): scheduler state for persistent single-ticker scheduling.
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastSuccessAt       string `json:"last_success_at,omitempty"`
	LeaseOwner          string `json:"lease_owner,omitempty"`
	LeaseUntil          string `json:"lease_until,omitempty"`

	// PR 5 (June 2026): incremental sync cursor — tracks the last video ID
	// processed so the monitor can resume from where it left off.
	LastCursor string `json:"last_cursor,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// TableName returns the database table name for the CategoryChannel model.
func (CategoryChannel) TableName() string {
	return "category_channels"
}

// ── Search Queries ──────────────────────────────────────────────────

// SearchQuery represents a recurring YouTube topic search.
// E.g. "Floyd Mayweather interview" → monitor finds new videos periodically.
type SearchQuery struct {
	ID                   string `json:"id"`
	Query                string `json:"query"`
	Category             string `json:"category"`
	DriveFolderID        string `json:"drive_folder_id,omitempty"`
	MinScore             int    `json:"min_score"`
	MaxResults           int    `json:"max_results"`
	CheckInterval        string `json:"check_interval"`
	LastRunAt            string `json:"last_run_at,omitempty"`
	LastVideoPublishedAt string `json:"last_video_published_at,omitempty"`
	IsActive             int    `json:"is_active"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

// TableName returns the database table name for SearchQuery.
func (SearchQuery) TableName() string { return "search_queries" }

// SearchQueryResult records a processed video from a search query.
// Used for dedup — prevents re-processing the same video.
type SearchQueryResult struct {
	QueryID     string `json:"query_id"`
	VideoID     string `json:"video_id"`
	VideoTitle  string `json:"video_title"`
	ChannelName string `json:"channel_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	ProcessedAt string `json:"processed_at"`
	Score       int    `json:"score"`
}

// TableName returns the database table name for SearchQueryResult.
func (SearchQueryResult) TableName() string { return "search_query_results" }
