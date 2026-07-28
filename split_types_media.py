#!/usr/bin/env python3
import os
import subprocess
import textwrap

ROOT = os.path.dirname(os.path.abspath(__file__))

def write(path, content):
    full = os.path.join(ROOT, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w") as f:
        f.write(content)
    return full

def run(cmd):
    subprocess.run(cmd, cwd=ROOT, check=True)

media_type = '''// Package asset — canonical MediaType taxonomy.
package asset

// MediaType classifies the content type of an asset (stock footage, video
// clip, image, audio/voiceover, document, generated video, sound effect).
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
	// by script_assets-family providers.
	MediaTypeScript MediaType = "script"
)

// IsValid reports whether the MediaType matches a known constant.
func (m MediaType) IsValid() bool {
\tswitch m {
\tcase MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument, MediaTypeImageVideo, MediaTypeSoundEffect, MediaTypeScript:
\t\treturn true
\t}
\treturn false
}
'''

location = '''package asset

import "time"

// LocationKind categorises where a Location physically lives.
type LocationKind string

const (
\tLocationKindLocal         LocationKind = "local"
\tLocationKindDrive         LocationKind = "drive"
\tLocationKindObjectStorage LocationKind = "object_storage"
)

// Location is the canonical domain entity for an asset location record.
type Location struct {
\tID            int64        `json:"id"`
\tAssetID       string       `json:"asset_id"`
\tLocationKind  LocationKind `json:"location_kind"`
\tURI           string       `json:"uri"`
\tExternalID    string       `json:"external_id,omitempty"`
\tAccessURL     string       `json:"access_url,omitempty"`
\tDownloadURL   string       `json:"download_url,omitempty"`
\tMimeType      string       `json:"mime_type"`
\tFileSizeBytes int64        `json:"file_size_bytes"`
\tFileHash      string       `json:"file_hash"`
\tIsPrimary     bool         `json:"is_primary"`
\tCreatedAt     time.Time    `json:"created_at"`
\tUpdatedAt     time.Time    `json:"updated_at"`
}
'''

location_resolver = '''package asset

import "context"

// Resolver is the canonical interface for resolving asset destinations.
type Resolver interface {
\t// Resolve returns the destination URI and metadata for an asset.
\tResolve(ctx context.Context, req *ResolveRequest) (*ResolveResult, error)
}

// ResolveRequest contains the information needed to resolve a destination.
type ResolveRequest struct {
\tSource          string // e.g. "youtube", "artlist", "voiceover"
\tGroup           string // Name of the group folder
\tFolderID        string // explicit folder ID (overrides group)
\tFolderPath      string // optional path info
\tSubfolderName   string // Name of the subfolder or video ID
\tCreateSubfolder bool   // whether to create subfolder if not exists
\tAssetID         string
\tAssetType       string // "clip", "stock", "artlist", "image", "voiceover"
\tProjectID       string
\tFolderName      string
\t// StyleGroup (PR-VO-B2, June 2026) is forwarded through the
\t// resolver so the per-call resolution can record the original
\t// style-cohort selector against the resolved destination.
\tStyleGroup string
\tMetadata   map[string]any
}

// ResolveResult contains the resolved destination information.
type ResolveResult struct {
\tLocationKind string // "drive", "local", "s3", etc.
\tURI          string // Drive folder ID, local path, etc.
\tFolderID     string // Drive folder ID
\tFolderPath   string // Full folder path
\tDriveLink    string // Drive web link
\tExtra        map[string]any
}
'''

asset_details = '''package asset

// Details is the full representation of an asset including all sub-entities.
type Details struct {
\tAsset      *Asset              `json:"asset"`
\tLocations  []*Location         `json:"locations,omitempty"`
\tProcessing []*ProcessingRecord `json:"processing,omitempty"`
\tVersions   []*Version          `json:"versions,omitempty"`
}

// LocalLocation returns the local Location for the asset.
func (d *Details) LocalLocation() *Location {
\tif d == nil {
\t\treturn nil
\t}
\tfor _, loc := range d.Locations {
\t\tif loc == nil {
\t\t\tcontinue
\t\t}
\t\tif loc.IsPrimary && loc.LocationKind == LocationKindLocal {
\t\t\treturn loc
\t\t}
\t}
\tfor _, loc := range d.Locations {
\t\tif loc != nil && loc.LocationKind == LocationKindLocal {
\t\t\treturn loc
\t\t}
\t}
\treturn nil
}

// DriveLocation returns the drive Location for the asset.
func (d *Details) DriveLocation() *Location {
\tif d == nil {
\t\treturn nil
\t}
\tfor _, loc := range d.Locations {
\t\tif loc == nil {
\t\t\tcontinue
\t\t}
\t\tif loc.IsPrimary && loc.LocationKind == LocationKindDrive {
\t\t\treturn loc
\t\t}
\t}
\tfor _, loc := range d.Locations {
\t\tif loc != nil && loc.LocationKind == LocationKindDrive {
\t\t\treturn loc
\t\t}
\t}
\treturn nil
}

// ProcessingStep returns a pointer to the processing record for the given step name.
func (d *Details) ProcessingStep(step string) *ProcessingRecord {
\tif d == nil {
\t\treturn nil
\t}
\tfor _, p := range d.Processing {
\t\tif p != nil && p.Step == step {
\t\t\treturn p
\t\t}
\t}
\treturn nil
}
'''

asset_summary = '''package asset

import "time"

// Summary is a lightweight projection of an asset for list views.
type Summary struct {
\tID             string         `json:"id"`
\tSource         Source         `json:"source"`
\tName           string         `json:"name"`
\tFilename       string         `json:"filename"`
\tMediaType      MediaType      `json:"media_type"`
\tCategory       string         `json:"category"`
\tLifecycleState LifecycleState `json:"lifecycle_state"`
\tPrimaryURI     string         `json:"primary_uri,omitempty"`
\tCreatedAt      time.Time      `json:"created_at"`
\tUpdatedAt      time.Time      `json:"updated_at"`
}
'''

subject = '''package asset

import "time"

// Subject represents a known entity (person, place, thing) for image generation.
type Subject struct {
\tID          int64     `json:"id"`
\tSlug        string    `json:"slug"`
\tDisplayName string    `json:"display_name"`
\tWikidataID  string    `json:"wikidata_id,omitempty"`
\tAliases     []string  `json:"aliases"` // Stored as JSON in the DB.
\tCategory    string    `json:"category"`
\tNotes       string    `json:"notes"`
\tCreatedAt   time.Time `json:"created_at"`
\tUpdatedAt   time.Time `json:"updated_at"`
}
'''

image_asset = '''package asset

import "time"

// ImageAsset represents an image stored in the asset index.
type ImageAsset struct {
\tID        int64  `json:"id"`
\tHash      string `json:"hash"`
\tSubjectID string `json:"subject_id"`
\tSlugID    string `json:"slug_id,omitempty"`
\tPathRel   string `json:"path_rel"`
\tLocalPath string `json:"local_path,omitempty"`
\tSourceURL string `json:"source_url"`
\tLicense   string `json:"license"`
\tWidth     int    `json:"width"`
\tHeight    int    `json:"height"`
\tSizeBytes int64  `json:"size_bytes"`
\tQualityScore int  `json:"quality_score"`
\tDescription string `json:"description"`
\tDriveFileID string `json:"drive_file_id,omitempty"`
\tStatus    string `json:"status,omitempty"`
\tError     string `json:"error,omitempty"`
\tMetadataJSON string `json:"metadata_json"`
\tCreatedAt time.Time `json:"created_at"`
\tTags      []string  `json:"tags,omitempty"`
\tOrigin    ImageOrigin `json:"origin,omitempty"`
\tProvider  ImageProvider `json:"provider,omitempty"`
}

// ImageUsage tracks usage of an image inside a rendered video.
type ImageUsage struct {
\tID      int64     `json:"id"`
\tImageID int64     `json:"image_id"`
\tVideoID string    `json:"video_id"`
\tUsedAt  time.Time `json:"used_at"`
}

// ImageTag represents a tag associated with an image.
type ImageTag struct {
\tID   int64  `json:"id"`
\tName string `json:"name"`
\tType string `json:"type"`
}
'''

category_channel = '''package asset

// CategoryChannel represents a YouTube channel subscribed to a specific
// category/folder.
type CategoryChannel struct {
\tID              string `json:"id"`
\tCategory        string `json:"category"`
\tChannelURL      string `json:"channel_url"`
\tChannelName     string `json:"channel_name,omitempty"`
\tKeywords        string `json:"keywords,omitempty"`
\tMinViews        int    `json:"min_views,omitempty"`
\tMaxClipDuration int    `json:"max_clip_duration,omitempty"`
\tDriveFolderID   string `json:"drive_folder_id,omitempty"`
\tSemanticKeywords string `json:"semantic_keywords,omitempty"`
\tMinSemanticScore int    `json:"min_semantic_score,omitempty"`
\tPlaylistEnd     int    `json:"playlist_end,omitempty"`
\tCheckInterval   string `json:"check_interval,omitempty"`
\tMaxVideosPerRun int    `json:"max_videos_per_run,omitempty"`
\tPriority        int    `json:"priority,omitempty"`
\tLookbackDays    int    `json:"lookback_days,omitempty"`
\tMaxSegments     int    `json:"max_segments,omitempty"`
\tSegmentPrompt   string `json:"segment_prompt,omitempty"`
\tEnabled         int    `json:"enabled,omitempty"`
\tNextCheckAt     string `json:"next_check_at,omitempty"`
\tLastCheckedAt   string `json:"last_checked_at,omitempty"`
\tConsecutiveFailures int `json:"consecutive_failures,omitempty"`
\tLastError       string `json:"last_error,omitempty"`
\tLastSuccessAt   string `json:"last_success_at,omitempty"`
\tLeaseOwner      string `json:"lease_owner,omitempty"`
\tLeaseUntil      string `json:"lease_until,omitempty"`
\tLastCursor      string `json:"last_cursor,omitempty"`
\tCreatedAt       string `json:"created_at"`
\tUpdatedAt       string `json:"updated_at"`
}

// TableName returns the database table name for the CategoryChannel model.
func (CategoryChannel) TableName() string {
\treturn "category_channels"
}
'''

search_query = '''package asset

// SearchQuery represents a recurring YouTube topic search.
type SearchQuery struct {
\tID                   string `json:"id"`
\tQuery                string `json:"query"`
\tCategory             string `json:"category"`
\tDriveFolderID        string `json:"drive_folder_id,omitempty"`
\tMinScore             int    `json:"min_score"`
\tMaxResults           int    `json:"max_results"`
\tCheckInterval        string `json:"check_interval"`
\tLastRunAt            string `json:"last_run_at,omitempty"`
\tLastVideoPublishedAt string `json:"last_video_published_at,omitempty"`
\tIsActive             int    `json:"is_active"`
\tCreatedAt            string `json:"created_at"`
\tUpdatedAt            string `json:"updated_at"`
}

// TableName returns the database table name for SearchQuery.
func (SearchQuery) TableName() string { return "search_queries" }

// SearchQueryResult records a processed video from a search query.
type SearchQueryResult struct {
\tQueryID     string `json:"query_id"`
\tVideoID     string `json:"video_id"`
\tVideoTitle  string `json:"video_title"`
\tChannelName string `json:"channel_name,omitempty"`
\tPublishedAt string `json:"published_at,omitempty"`
\tProcessedAt string `json:"processed_at"`
\tScore       int    `json:"score"`
}

// TableName returns the database table name for SearchQueryResult.
func (SearchQueryResult) TableName() string { return "search_query_results" }
'''

delivery_status = '''package delivery

// DeliveryStatus tracks the lifecycle of an outbox delivery ATTEMPT.
type DeliveryStatus string

const (
\tDeliveryPending     DeliveryStatus = "PENDING"
\tDeliveryLeased      DeliveryStatus = "LEASED"
\tDeliveryRunning     DeliveryStatus = "RUNNING"
\tDeliveryRetryWait   DeliveryStatus = "RETRY_WAIT"
\tDeliverySucceeded   DeliveryStatus = "SUCCEEDED"
\tDeliveryFailed      DeliveryStatus = "FAILED"
\tDeliveryBlockedAuth DeliveryStatus = "BLOCKED_AUTH"
\tDeliveryCancelled   DeliveryStatus = "CANCELLED"
)
'''

delivery_attempt = '''package delivery

import "time"

// Delivery represents an attempt to deliver an artifact to a destination.
type Delivery struct {
\tID               string         `json:"id"`
\tArtifactID       string         `json:"artifact_id"`
\tDestinationID    string         `json:"destination_id"`
\tProvider         string         `json:"provider"`
\tStatus           DeliveryStatus `json:"status"`
\tAttemptCount     int            `json:"attempt_count"`
\tMaxAttempts      int            `json:"max_attempts"`
\tNextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
\tLockedBy         string         `json:"locked_by,omitempty"`
\tLockedUntil      *time.Time     `json:"locked_until,omitempty"`
\tRemoteID         string         `json:"remote_id,omitempty"`
\tRemoteURL        string         `json:"remote_url,omitempty"`
\tLastErrorCode    string         `json:"last_error_code,omitempty"`
\tLastErrorMessage string         `json:"last_error_message,omitempty"`
\tIdempotencyKey   string         `json:"idempotency_key,omitempty"`
\tStorageKey       string         `json:"storage_key,omitempty"`
\tSHA256           string         `json:"sha256,omitempty"`
\tSizeBytes        int64          `json:"size_bytes,omitempty"`
\tMimeType         string         `json:"mime_type,omitempty"`
\tCreatedAt        time.Time      `json:"created_at"`
\tUpdatedAt        time.Time      `json:"updated_at"`
\tCompletedAt      *time.Time     `json:"completed_at,omitempty"`
}
'''

delivery_destination = '''package delivery

// DeliveryDestination is a configured delivery target.
type DeliveryDestination struct {
\tDestinationID string `json:"destination_id"`
\tName          string `json:"name"`
\tProvider      string `json:"provider"`
\tEnabled       bool   `json:"enabled"`
\tConfigJSON    string `json:"config_json,omitempty"`
\tCreatedAt     string `json:"created_at"`
}
'''

delivery_doc = '''// Package delivery owns the canonical contracts for publishing assets to
// external destinations and for tracking the lifecycle of outbox delivery
// attempts.
package delivery
'''

files = {
    "internal/domain/asset/media_type.go": media_type,
    "internal/domain/asset/location.go": location,
    "internal/domain/asset/location_resolver.go": location_resolver,
    "internal/domain/asset/asset_details.go": asset_details,
    "internal/domain/asset/asset_summary.go": asset_summary,
    "internal/domain/asset/subject.go": subject,
    "internal/domain/asset/image_asset.go": image_asset,
    "internal/domain/asset/category_channel.go": category_channel,
    "internal/domain/asset/search_query.go": search_query,
    "internal/domain/delivery/status.go": delivery_status,
    "internal/domain/delivery/attempt.go": delivery_attempt,
    "internal/domain/delivery/destination.go": delivery_destination,
    "internal/domain/delivery/doc.go": delivery_doc,
}

for path, content in files.items():
    write(path, content)

# Remove old types_media.go if it still exists.
old = os.path.join(ROOT, "internal/domain/asset/types_media.go")
if os.path.exists(old):
    os.remove(old)

# Update stale references.
def replace(path, old, new):
    full = os.path.join(ROOT, path)
    with open(full, "r") as f:
        text = f.read()
    text = text.replace(old, new)
    with open(full, "w") as f:
        f.write(text)

replace("cmd/archcheck/scan/percheck_metadataregistry.go",
        '"internal/domain/asset/types_media.go":                     true, // owner: platform-asset-metadata, deadline: 2026-08-15',
        '"internal/domain/asset/location_resolver.go":               true, // owner: platform-asset-metadata, deadline: 2026-08-15')
replace("internal/infrastructure/database/sqlite/assets/asset_store.go",
        "types_media.go, lifecycle_core.go, store_helpers.go) are reachable",
        "lifecycle_core.go, store_helpers.go, and the split domain files) are reachable")
replace("internal/infrastructure/database/sqlite/assets/location_queries.go",
        "internal/domain/asset/types_media.go)",
        "internal/domain/asset/location.go)")
replace("internal/infrastructure/database/sqlite/assets/location_queries.go",
        "migrated from types_media.go",
        "migrated from location.go")
replace("internal/infrastructure/database/sqlite/assets/channels/channels_repository.go",
        "(domain/asset/types_media.go)",
        "(domain/asset/category_channel.go)")
replace("internal/domain/asset/asset_publish_status.go",
        "DeliveryStatus in types_media.go",
        "delivery.DeliveryStatus in\\n// internal/domain/delivery/status.go")

# Ensure store_helpers uses the delivery package.
sh = os.path.join(ROOT, "internal/domain/asset/store_helpers.go")
with open(sh, "r") as f:
    text = f.read()
text = text.replace(
    '"context"\\n\\n\\t"go.uber.org/zap"',
    '"context"\\n\\n\\t"github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"\\n\\t"go.uber.org/zap"')
text = text.replace("*Delivery)", "*delivery.Delivery)")
text = text.replace("*Delivery}", "*delivery.Delivery}")
with open(sh, "w") as f:
    f.write(text)

run(["git", "add", "internal/domain/asset/", "internal/domain/delivery/", "cmd/archcheck/scan/percheck_metadataregistry.go", "internal/infrastructure/database/sqlite/assets/asset_store.go", "internal/infrastructure/database/sqlite/assets/location_queries.go", "internal/infrastructure/database/sqlite/assets/channels/channels_repository.go"])
run(["git", "commit", "-m", "refactor(asset): split types_media.go and extract delivery types\\n\\n- Split internal/domain/asset/types_media.go into focused files.\\n- Move delivery types to internal/domain/delivery/.\\n- Update stale references and store_helpers to use delivery package.", "-n", "--no-verify"])
print("committed")
