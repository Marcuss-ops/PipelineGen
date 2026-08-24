package jobs

import (
	"encoding/json"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Typed job payloads (Punto 22) ─────────────────────────────────────────
// Each job type has a corresponding payload struct and a factory that
// unmarshals the raw JSON into the concrete type. This eliminates runtime
// map[string]any assertions and documents the expected fields per job type.
//
// Adding a new job type:
//   1. Define the payload struct below.
//   2. Register it in payloadDecoders.
//   3. In the handler, call DecodePayload[*NewPayload](job).

type MediaStockPayload struct {
	Project   string `json:"project"`
	VideoName string `json:"video_name"`
	Query     string `json:"query"`
	Count     int    `json:"count"`
}

// YouTubeClipExtractPayload is sent with JobTypeYouTubeClipExtract.
type YouTubeClipExtractPayload struct {
	URL       string `json:"url"`
	Project   string `json:"project,omitempty"`
	VideoName string `json:"video_name,omitempty"`
	StartTime string `json:"start_time,omitempty"`
	EndTime   string `json:"end_time,omitempty"`
}

// CatalogSyncPayload is sent with JobTypeCatalogSync.
type CatalogSyncPayload struct {
	Source    string `json:"source"` // "youtube", "stock", "artlist"
	ForceFull bool   `json:"force_full,omitempty"`
}

// SystemCleanupPayload is sent with JobTypeSystemCleanup.
type SystemCleanupPayload struct {
	Deep   bool `json:"deep"`
	DryRun bool `json:"dry_run"`
}

// VoiceoverBatchPayload is sent with JobTypeVoiceoverBatch.
type VoiceoverBatchPayload struct {
	Project     string   `json:"project"`
	TargetCount int      `json:"target_count,omitempty"`
	VoiceIDs    []string `json:"voice_ids,omitempty"`
}

// BooksProcessPayload is sent with JobTypeBooksProcess.
type BooksProcessPayload struct {
	BookIDs []string `json:"book_ids"`
	Project string   `json:"project,omitempty"`
}

// LessonsProcessPayload is sent with JobTypeLessonsProcess.
type LessonsProcessPayload struct {
	LessonIDs []string `json:"lesson_ids"`
	Project   string   `json:"project,omitempty"`
}

// MediaReindexPayload is sent with JobTypeMediaReindex.
type MediaReindexPayload struct {
	AssetID string `json:"asset_id,omitempty"` // empty = reindex all
	Source  string `json:"source,omitempty"`
}

// ArtlistRunPayload is sent with JobTypeArtlistRun.
type ArtlistRunPayload struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// ArtlistCacheRefreshPayload is sent with TypeArtlistCacheRefresh.
type ArtlistCacheRefreshPayload struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit,omitempty"`
	PreferRemote bool   `json:"prefer_remote,omitempty"`
}

// BulkUploadYouTubeClipsPayload is sent with JobTypeBulkUploadYouTubeClips.
//
// PR-13 (July 2026, refactor(api): drop runtime-tunable noise configs):
// the canonical client surface is the WHAT (local_folder, drive_folder_id,
// source, category). The HOW (extensions, layout, indexing policy) lives in
// server config — see Cfg.Storage + Cfg.Jobs. Recursion and concurrency are
// the only runtime knobs exposed to callers.
type BulkUploadYouTubeClipsPayload struct {
	LocalFolder   string `json:"local_folder"`              // absolute path to scan
	DriveFolderID string `json:"drive_folder_id,omitempty"` // target Drive folder (root of subdirs)
	Source        string `json:"source,omitempty"`          // source label written to media_assets (default: youtube-local)
	Category      string `json:"category,omitempty"`        // category written to media_assets
	Recursive     bool   `json:"recursive,omitempty"`       // true = recursive scan; false = shallow scan
	Concurrency   int    `json:"concurrency,omitempty"`     // parallel workers (default from server config)
}

// DriveFolderSyncPayload is sent with JobTypeDriveFolderSync.
//
// QDRANT-001 (June 2026) closure: the /internal/v1 variant (server-to-server)
// also copies the Idempotency-Key header into the payload so the worker
// has a stable per-request key for retry dedup. The Caller field is for
// observability ("api_admin" vs "internal_v1"); the dispatch helper
// stamps it when the job is enqueued.
type DriveFolderSyncPayload struct {
	DriveFolderID  string `json:"drive_folder_id"`
	Source         string `json:"source,omitempty"`
	Name           string `json:"name,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	Caller         string `json:"caller,omitempty"`
}

// TypedPayload is a constraint satisfied by all job payload structs.
type TypedPayload interface {
	MediaStockPayload | YouTubeClipExtractPayload |
		CatalogSyncPayload |
		SystemCleanupPayload | VoiceoverBatchPayload |
		BooksProcessPayload | LessonsProcessPayload |
		MediaReindexPayload | ArtlistRunPayload | ArtlistCacheRefreshPayload | BulkUploadYouTubeClipsPayload |
		DriveFolderSyncPayload
}

// DecodePayload unmarshals the job's raw payload into the requested type T.
// Returns an error if the payload cannot be decoded.
func DecodePayload[T TypedPayload](j *job.Job) (*T, error) {
	var payload T
	if len(j.Payload) == 0 {
		return &payload, nil
	}
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// ── Versioned payload envelope (future use) ───────────────────────────────
// When payloads need to evolve, wrap them in a versioned envelope:
//
//	type PayloadEnvelope struct {
//	    Version int             `json:"v"`
//	    Data    json.RawMessage `json:"data"`
//	}
