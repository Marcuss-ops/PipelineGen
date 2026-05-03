package artlist

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

type Service struct {
	cfg              *config.Config
	mainDB           *sql.DB
	artlistDB        *sql.DB
	jobsDB           *sql.DB
	nodeScraperDir   string
	clipsRepo        *clips.Repository
	driveClient      *driveapi.Service
	driveFolderID    string
	driveDestination *drivedestination.Service
	mediaProcessor   *mediaasset.Processor
	log              *zap.Logger
}

func NewService(cfg *config.Config, mainDB *sql.DB, artlistDBPath, nodeScraperDir string, clipsRepo *clips.Repository, driveClient *driveapi.Service, driveFolderID string, driveDestination *drivedestination.Service, mediaProcessor *mediaasset.Processor, log *zap.Logger) (*Service, error) {
	var artlistDB *sql.DB
	var err error
	if artlistDBPath != "" {
		artlistDB, err = sql.Open("sqlite3", artlistDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			return nil, err
		}
	}
	// Open jobs database connection with WAL mode and busy timeout
	jobsDBPath := filepath.Join(cfg.Storage.DataDir, "jobs.db.sqlite")
	jobsDB, err := sql.Open("sqlite3", jobsDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		if artlistDB != nil {
			artlistDB.Close()
		}
		return nil, fmt.Errorf("failed to open jobs database: %w", err)
	}
	// Set connection pool settings for jobsDB
	jobsDB.SetMaxOpenConns(5)
	jobsDB.SetMaxIdleConns(2)
	jobsDB.SetConnMaxLifetime(0)

	// Set connection pool settings for artlistDB if present
	if artlistDB != nil {
		artlistDB.SetMaxOpenConns(5)
		artlistDB.SetMaxIdleConns(2)
		artlistDB.SetConnMaxLifetime(0)
	}
	return &Service{
		cfg:              cfg,
		mainDB:           mainDB,
		jobsDB:           jobsDB,
		artlistDB:        artlistDB,
		nodeScraperDir:   nodeScraperDir,
		clipsRepo:        clipsRepo,
		driveClient:      driveClient,
		driveFolderID:    driveFolderID,
		driveDestination: driveDestination,
		mediaProcessor:   mediaProcessor,
		log:              log,
	}, nil
}

func (s *Service) Close() error {
	var firstErr error
	if s.artlistDB != nil {
		if err := s.artlistDB.Close(); err != nil {
			firstErr = err
		}
	}
	if s.jobsDB != nil {
		if err := s.jobsDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

// RunTagRequest represents the full Artlist tag pipeline request.
type RunTagRequest struct {
	Term         string `json:"term"`
	Limit        int    `json:"limit"`
	RootFolderID string `json:"root_folder_id,omitempty"`
	Strategy     string `json:"strategy,omitempty"`
	DryRun       bool   `json:"dry_run,omitempty"`
	// Deprecated: kept for backward compatibility with older clients.
	ForceReupload bool `json:"force_reupload,omitempty"`
}

// ToMap converts RunTagRequest to a map for job payload.
func (r *RunTagRequest) ToMap() map[string]any {
	return map[string]any{
		"term":          r.Term,
		"limit":         r.Limit,
		"root_folder_id": r.RootFolderID,
		"strategy":      r.Strategy,
		"dry_run":       r.DryRun,
	}
}

// RunDedupKey creates a deduplication key for artlist jobs.
func RunDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	return runDedupKey(term, rootFolderID, strategy, dryRun)
}

// RunTagItem represents the result for a single clip in the full pipeline.
type RunTagItem struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	DownloadURL  string `json:"download_url,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Error        string `json:"error,omitempty"`
}

// RunTagResponse represents the result of the full tag pipeline.
type RunTagResponse struct {
	OK              bool         `json:"ok"`
	RunID           string       `json:"run_id,omitempty"`
	Status          string       `json:"status,omitempty"`
	Term            string       `json:"term"`
	Strategy        string       `json:"strategy,omitempty"`
	DryRun          bool         `json:"dry_run,omitempty"`
	RootFolderID    string       `json:"root_folder_id,omitempty"`
	TagFolderID     string       `json:"tag_folder_id,omitempty"`
	Requested       int          `json:"requested"`
	Found           int          `json:"found"`
	Processed       int          `json:"processed"`
	Skipped         int          `json:"skipped"`
	Failed          int          `json:"failed"`
	WouldProcess    int          `json:"would_process,omitempty"`
	WouldSkip       int          `json:"would_skip,omitempty"`
	EstimatedSize   int          `json:"estimated_size,omitempty"`
	LastProcessedAt *string      `json:"last_processed_at,omitempty"`
	StartedAt       *string      `json:"started_at,omitempty"`
	EndedAt         *string      `json:"ended_at,omitempty"`
	Items           []RunTagItem `json:"items,omitempty"`
	Error           string       `json:"error,omitempty"`
}

// DiagnosticsResponse reports the current Artlist wiring and database readiness.
type DiagnosticsResponse struct {
	OK                bool    `json:"ok"`
	RootFolderID      string  `json:"root_folder_id,omitempty"`
	DriveFolderID     string  `json:"drive_folder_id,omitempty"`
	NodeScraperDir    string  `json:"node_scraper_dir,omitempty"`
	HasDriveClient    bool    `json:"has_drive_client"`
	HasArtlistDB      bool    `json:"has_artlist_db"`
	MainDBReady       bool    `json:"main_db_ready"`
	ClipsTotal        int     `json:"clips_total"`
	ArtlistClipsTotal int     `json:"artlist_clips_total"`
	SearchTerm        string  `json:"search_term,omitempty"`
	MatchingClips     int     `json:"matching_clips,omitempty"`
	EstimatedSize     int     `json:"estimated_size,omitempty"`
	LastProcessedAt   *string `json:"last_processed_at,omitempty"`
	Error             string  `json:"error,omitempty"`
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
