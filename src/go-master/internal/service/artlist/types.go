package artlist

import (
	"database/sql"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
	"google.golang.org/api/drive/v3"
)

type Service struct {
	mainDB         *sql.DB
	artlistDB      *sql.DB
	nodeScraperDir string
	clipsRepo      *clips.Repository
	driveClient    *drive.Service
	driveFolderID  string
	log            *zap.Logger
}

func NewService(mainDB *sql.DB, artlistDBPath, nodeScraperDir string, clipsRepo *clips.Repository, driveClient *drive.Service, driveFolderID string, log *zap.Logger) (*Service, error) {
	var artlistDB *sql.DB
	var err error
	if artlistDBPath != "" {
		artlistDB, err = sql.Open("sqlite3", artlistDBPath)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		mainDB:         mainDB,
		artlistDB:      artlistDB,
		nodeScraperDir: nodeScraperDir,
		clipsRepo:      clipsRepo,
		driveClient:    driveClient,
		driveFolderID:  driveFolderID,
		log:            log,
	}, nil
}

func (s *Service) Close() error {
	if s.artlistDB != nil {
		return s.artlistDB.Close()
	}
	return nil
}

type nodeScraperResponse struct {
	OK    bool `json:"ok"`
	Clips []struct {
		Title       string `json:"title"`
		ClipPageURL string `json:"clip_page_url"`
		PrimaryURL  string `json:"primary_url"`
		ClipID      string `json:"clip_id"`
	} `json:"clips"`
}

// Stats represents the statistics for Artlist endpoints
type Stats struct {
	OK                 bool    `json:"ok"`
	ClipsTotal         int     `json:"clips_total"`
	ArtlistClipsTotal  int     `json:"artlist_clips_total"`
	SearchTermsTotal   int     `json:"search_terms_total"`
	SearchTermsScraped int     `json:"search_terms_scraped"`
	SearchTermsPending int     `json:"search_terms_pending"`
	CoveragePct        float64 `json:"coverage_pct"`
	LastSyncAt         *string `json:"last_sync_at,omitempty"`
	StaleTerms         int     `json:"stale_terms"`
}

// SearchRequest represents a search request
type SearchRequest struct {
	Term     string `json:"term"`
	Limit    int    `json:"limit"`
	PreferDB bool   `json:"prefer_db"`
	SaveDB   bool   `json:"save_db"`
}

// SearchResponse represents a search response
type SearchResponse struct {
	OK     bool          `json:"ok"`
	Term   string        `json:"term"`
	Source string        `json:"source"`
	Clips  []models.Clip `json:"clips"`
	Error  string        `json:"error,omitempty"`
}

// SyncRequest represents a sync request
type SyncRequest struct {
	Terms       []string `json:"terms"`
	Limit       int      `json:"limit"`
	SaveDB      bool     `json:"save_db"`
	OnlyPending bool     `json:"only_pending"`
}

// SyncResponse represents a sync response
type SyncResponse struct {
	OK         bool   `json:"ok"`
	Requested  int    `json:"requested"`
	Synced     int    `json:"synced"`
	Failed     int    `json:"failed"`
	SavedClips int    `json:"saved_clips"`
	Error      string `json:"error,omitempty"`
}

// ClipStatusResponse represents the status of a clip
type ClipStatusResponse struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	HasLocalFile bool   `json:"has_local_file"`
	LocalPath    string `json:"local_path"`
	DriveLink    string `json:"drive_link"`
	HasDriveLink bool   `json:"has_drive_link"`
	FileHash     string `json:"file_hash"`
	Source       string `json:"source"`
	ExternalURL  string `json:"external_url"`
}

// DownloadClipRequest represents a download request
type DownloadClipRequest struct {
	OutputDir string `json:"output_dir"`
}

// DownloadClipResponse represents a download response
type DownloadClipResponse struct {
	OK        bool   `json:"ok"`
	ClipID    string `json:"clip_id"`
	LocalPath string `json:"local_path"`
	FileHash  string `json:"file_hash"`
	Error     string `json:"error,omitempty"`
}

// UploadClipToDriveRequest represents an upload to Drive request
type UploadClipToDriveRequest struct {
	FolderID string `json:"folder_id"`
}

// UploadClipToDriveResponse represents an upload response
type UploadClipToDriveResponse struct {
	OK           bool   `json:"ok"`
	ClipID       string `json:"clip_id"`
	DriveLink    string `json:"drive_link"`
	DownloadLink string `json:"download_link"`
	Error        string `json:"error,omitempty"`
}

// ProcessClipRequest represents a process clip request
type ProcessClipRequest struct {
	Term         string `json:"term"`
	ClipID       string `json:"clip_id"`
	AutoDownload bool   `json:"auto_download"`
	AutoUpload   bool   `json:"auto_upload_drive"`
}

// ProcessClipResponse represents a process clip response
type ProcessClipResponse struct {
	OK     bool   `json:"ok"`
	ClipID string `json:"clip_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// KeywordBatchRequest represents a keyword-driven batch process
type KeywordBatchRequest struct {
	Term           string `json:"term"`
	Limit          int    `json:"limit"`
	CandidateLimit int    `json:"candidate_limit"`
}

// KeywordBatchItem represents the outcome for one candidate clip
type KeywordBatchItem struct {
	ClipID    string `json:"clip_id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	DriveLink string `json:"drive_link,omitempty"`
	FileHash  string `json:"file_hash,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// KeywordBatchResponse represents a keyword batch execution
type KeywordBatchResponse struct {
	OK               bool               `json:"ok"`
	Term             string             `json:"term"`
	Requested        int                `json:"requested"`
	CandidatesFound  int                `json:"candidates_found"`
	Processed        int                `json:"processed"`
	SkippedOnDrive   int                `json:"skipped_on_drive"`
	SkippedDuplicate int                `json:"skipped_duplicate"`
	FolderID         string             `json:"folder_id"`
	FolderName       string             `json:"folder_name"`
	Results          []KeywordBatchItem `json:"results"`
}

// ReindexRequest represents a reindex request
type ReindexRequest struct {
	Mode         string `json:"mode"`
	SourceFilter string `json:"source_filter"`
}

// ReindexResponse represents a reindex response
type ReindexResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// PurgeStaleRequest represents a purge stale request
type PurgeStaleRequest struct {
	OlderThanDays  int  `json:"older_than_days"`
	OnlyDownloaded bool `json:"only_downloaded"`
	DryRun         bool `json:"dry_run"`
}

// PurgeStaleResponse represents a purge stale response
type PurgeStaleResponse struct {
	OK          bool   `json:"ok"`
	WouldRemove int    `json:"would_remove,omitempty"`
	Removed     int    `json:"removed,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SyncDriveStatusRequest represents a sync drive status request
type SyncDriveStatusRequest struct {
	FixBroken bool `json:"fix_broken"`
	DryRun    bool `json:"dry_run"`
}

// SyncDriveStatusResponse represents a sync drive status response
type SyncDriveStatusResponse struct {
	OK           bool `json:"ok"`
	TotalChecked int  `json:"total_checked"`
	BrokenLinks  int  `json:"broken_links"`
	Fixed        int  `json:"fixed"`
}

// TermInfo represents information about a search term
type TermInfo struct {
	Term        string    `json:"term"`
	Scraped     bool      `json:"scraped"`
	LastScraped *string   `json:"last_scraped"`
	VideoCount  int       `json:"video_count"`
	CreatedAt   time.Time `json:"created_at"`
}
