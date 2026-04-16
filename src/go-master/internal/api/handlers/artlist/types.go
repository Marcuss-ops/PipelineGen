package artlistpipeline

import "velox/go-master/internal/artlistdb"

// SentenceAssociation represents a sentence associated with a search term and clips.
type SentenceAssociation struct {
	Sentence    string                  `json:"sentence"`
	SentenceIdx int                     `json:"sentence_idx"`
	Timestamp   string                  `json:"timestamp"`
	Keyword     string                  `json:"keyword"`
	ArtlistTerm string                  `json:"artlist_term"`
	Clips       []artlistdb.ArtlistClip `json:"clips"`
	ClipsNeeded int                     `json:"clips_needed"`
}

// GenerateFullVideoRequest is the request body for full video generation.
type GenerateFullVideoRequest struct {
	Text              string `json:"text" binding:"required"`
	Topic             string `json:"topic"`
	OutputName        string `json:"output_name"`
	MaxClipsPerPhrase int    `json:"max_clips_per_phrase"`
	ParallelDownloads int    `json:"parallel_downloads"`
}

// AssociateRequest is the request body for text-clip association preview.
type AssociateRequest struct {
	Text     string `json:"text" binding:"required"`
	MaxClips int    `json:"max_clips"`
}

// DownloadClipsRequest is the request body for step-by-step download.
type DownloadClipsRequest struct {
	Associations []SentenceAssociation `json:"associations"`
	Topic        string                `json:"topic"`
}

// BatchDownloadRequest is the request body for batch download.
type BatchDownloadRequest struct {
	Terms        []string `json:"terms"`
	ClipsPerTerm int      `json:"clips_per_term"`
	Parallel     int      `json:"parallel"`
}

// DownloadResult holds the result of a clip download operation.
type DownloadResult struct {
	SentenceIdx int
	Clip        artlistdb.ArtlistClip
	DriveFileID string
	DriveURL    string
	LocalPath   string
	Err         error
}

// TimestampEntry represents a timestamp mapping for a clip.
type TimestampEntry struct {
	SentenceIdx int    `json:"sentence_idx"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	ClipID      string `json:"clip_id"`
	DriveURL    string `json:"drive_url"`
}
