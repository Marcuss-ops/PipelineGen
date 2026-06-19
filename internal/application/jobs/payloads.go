package jobs

import (
	"encoding/json"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
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

// BatchScriptGeneratePayload is sent with JobTypeBatchScriptGenerate.
type BatchScriptGeneratePayload struct {
	Project   string `json:"project"`
	VideoName string `json:"video_name"`
	Count     int    `json:"count"`
}

// ClipScriptGeneratePayload is sent with JobTypeClipScriptGenerate.
type ClipScriptGeneratePayload struct {
	ClipIDs   []string `json:"clip_ids"`
	Project   string   `json:"project,omitempty"`
	VideoName string   `json:"video_name,omitempty"`
}

// CatalogScriptGeneratePayload is sent with JobTypeCatalogScriptGenerate.
type CatalogScriptGeneratePayload struct {
	Query     string `json:"query"`
	Project   string `json:"project,omitempty"`
	VideoName string `json:"video_name,omitempty"`
}

// MediaStockPayload is sent with JobTypeMediaStock.
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

// BulkUploadYouTubeClipsPayload is sent with JobTypeBulkUploadYouTubeClips.
//
// Scans a local folder recursively for .mp4 files (each with optional
// clip_manifest.json / transcript.txt siblings), uploads them to Google
// Drive, creates media_assets records, generates semantic + transcript
// embeddings via the Python embedding server, and pushes points to Qdrant.
//
// Designed for the "add a folder of orphan YouTube clips back into the
// pipeline" workflow — runs entirely async via the job system, so callers
// can poll progress through GET /api/jobs/{id}/full.
type BulkUploadYouTubeClipsPayload struct {
	LocalFolder         string   `json:"local_folder"`                // absolute path to scan recursively
	DriveFolderID       string   `json:"drive_folder_id,omitempty"`   // target Drive folder (root of subdirs)
	DriveFolderName     string   `json:"drive_folder_name,omitempty"` // fallback: create folder by this name under ClipsRoot
	Source              string   `json:"source,omitempty"`            // source label written to media_assets (default: youtube-local)
	Category            string   `json:"category,omitempty"`          // category written to media_assets
	SubdirAsDriveSubdir bool     `json:"subdir_as_drive_subdir"`      // mirror local subdir structure on Drive (default true)
	Recursive           bool     `json:"recursive"`                   // recurse into subdirs (default true)
	DryRun              bool     `json:"dry_run"`                     // scan + report, do not upload
	Limit               int      `json:"limit,omitempty"`             // cap number of clips (0 = no limit)
	SkipUpload          bool     `json:"skip_upload,omitempty"`       // only create DB records + embeddings
	SkipEmbeddings      bool     `json:"skip_embeddings,omitempty"`   // only upload + create DB records
	SkipQdrant          bool     `json:"skip_qdrant,omitempty"`       // generate embeddings but skip Qdrant push
	Concurrency         int      `json:"concurrency,omitempty"`       // parallel uploads (default 2)
	FilePatterns        []string `json:"file_patterns,omitempty"`     // extra glob patterns to include (default [*.mp4])
	SkipPatterns        []string `json:"skip_patterns,omitempty"`     // paths to skip (substring match)
}

// DriveFolderSyncPayload is sent with JobTypeDriveFolderSync.
type DriveFolderSyncPayload struct {
	DriveFolderID string `json:"drive_folder_id"`
	Source        string `json:"source,omitempty"`
	Name          string `json:"name,omitempty"`
	MediaType     string `json:"media_type,omitempty"`
}

// TypedPayload is a constraint satisfied by all job payload structs.
type TypedPayload interface {
	BatchScriptGeneratePayload |
		ClipScriptGeneratePayload | CatalogScriptGeneratePayload |
		MediaStockPayload | YouTubeClipExtractPayload |
		CatalogSyncPayload |
		SystemCleanupPayload | VoiceoverBatchPayload |
		BooksProcessPayload | LessonsProcessPayload |
		MediaReindexPayload | ArtlistRunPayload | BulkUploadYouTubeClipsPayload |
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
