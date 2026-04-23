package clipsearch

import (
	"context"
	"sync"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/ml/ollama"
)

type Service struct {
	downloader *ClipDownloader
	uploader   *DriveUploader
	persister  *ClipPersister
	finder     *ClipFinder
	processor  *ClipProcessor

	artlistSrc *clip.ArtlistSource
	indexer    *clip.Indexer
	ollama     *ollama.Client

	ytDlpPath  string
	ffmpegPath string

	downloadDir string

	postCycleSync func(context.Context) error
	mu            sync.Mutex

	keywordFailures map[string]int
	keywordBlocked  map[string]time.Time
	checkpoints     *ClipJobCheckpointStore
	uploadMu        sync.Mutex

	// Concurrency control
	workerSemaphore chan struct{}
}

const (
	defaultPerKeywordTimeout = 90 * time.Second // Increased for parallel load
	keywordFailThreshold     = 3
	keywordBlockDuration     = 10 * time.Minute
	maxParallelDownloads     = 5
)

type SearchOptions struct {
	ForceFresh         bool
	MaxClipsPerKeyword int
}

type SearchResult struct {
	Keyword           string   `json:"keyword"`
	ClipID            string   `json:"clip_id"`
	Filename          string   `json:"filename"`
	Source            string   `json:"source,omitempty"`
	DriveURL          string   `json:"drive_url"`
	DriveID           string   `json:"drive_id"`
	Folder            string   `json:"folder"`
	FolderID          string   `json:"folder_id,omitempty"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	StartSec          float64  `json:"start_sec,omitempty"`
	EndSec            float64  `json:"end_sec,omitempty"`
	Score             float64  `json:"score,omitempty"`
	TranscriptSnippet string   `json:"transcript_snippet,omitempty"`
	ThumbnailURL      string   `json:"thumbnail_url,omitempty"`
	TextDriveURL      string   `json:"text_drive_url,omitempty"`
	TextDriveID       string   `json:"text_drive_id,omitempty"`
}

type DriveUploadResult struct {
	DriveID    string
	Filename   string
	DriveURL   string
	FolderID   string
	FolderName string
	FolderPath string
	TextFileID string
	TextURL    string
	TextName   string
}
