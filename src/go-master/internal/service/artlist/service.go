package artlist

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/models"
)

type Service struct {
	mainDB         *sql.DB
	artlistDB      *sql.DB
	nodeScraperDir string
	clipsRepo      *clips.Repository
	driveClient    *drive.DocClient
	driveFolderID  string
	log            *zap.Logger
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

func NewService(
	mainDB *sql.DB,
	artlistDBPath string,
	nodeScraperDir string,
	clipsRepo *clips.Repository,
	driveClient *drive.DocClient,
	driveFolderID string,
	log *zap.Logger,
) (*Service, error) {
	var artlistDB *sql.DB
	var err error

	if artlistDBPath != "" {
		artlistDB, err = sql.Open("sqlite3", artlistDBPath)
		if err != nil {
			log.Warn("Failed to open artlist_videos.db, continuing without it", zap.Error(err))
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

// GetStats returns statistics about Artlist clips and search terms
func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{OK: true}

	// Get clips total
	err := s.mainDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM clips").Scan(&stats.ClipsTotal)
	if err != nil {
		s.log.Warn("Failed to get clips total", zap.Error(err))
	}

	// Get artlist clips total
	err = s.mainDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM clips WHERE source = 'artlist'").Scan(&stats.ArtlistClipsTotal)
	if err != nil {
		s.log.Warn("Failed to get artlist clips total", zap.Error(err))
	}

	// Get stats from artlist_videos.db if available
	if s.artlistDB != nil {
		// Search terms total
		err = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms").Scan(&stats.SearchTermsTotal)
		if err != nil {
			s.log.Warn("Failed to get search terms total", zap.Error(err))
		}

		// Search terms scraped
		err = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE scraped = 1").Scan(&stats.SearchTermsScraped)
		if err != nil {
			s.log.Warn("Failed to get scraped terms", zap.Error(err))
		}

		// Search terms pending
		err = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE COALESCE(scraped, 0) = 0").Scan(&stats.SearchTermsPending)
		if err != nil {
			s.log.Warn("Failed to get pending terms", zap.Error(err))
		}

		// Coverage percentage
		if stats.SearchTermsTotal > 0 {
			stats.CoveragePct = float64(stats.SearchTermsScraped) / float64(stats.SearchTermsTotal) * 100
		}

		// Last sync time
		var lastSync string
		err = s.artlistDB.QueryRowContext(ctx, "SELECT MAX(last_scraped) FROM search_terms WHERE last_scraped IS NOT NULL").Scan(&lastSync)
		if err == nil && lastSync != "" {
			stats.LastSyncAt = &lastSync
		}

		// Stale terms (not scraped in last 7 days or never scraped)
		staleThreshold := time.Now().AddDate(0, 0, -7).Format("2006-01-02 15:04:05")
		err = s.artlistDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM search_terms WHERE COALESCE(video_count, 0) = 0 OR last_scraped < ? OR last_scraped IS NULL",
			staleThreshold).Scan(&stats.StaleTerms)
		if err != nil {
			s.log.Warn("Failed to get stale terms", zap.Error(err))
		}
	}

	return stats, nil
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

// Search searches for Artlist clips
func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	resp := &SearchResponse{
		OK:   true,
		Term: req.Term,
	}

	if req.Limit <= 0 {
		req.Limit = 8
	}

	// Try DB first if prefer_db is set
	if req.PreferDB {
		clips, err := s.SearchDB(ctx, req.Term, req.Limit)
		if err == nil && len(clips) > 0 {
			resp.Source = "db"
			resp.Clips = clips
			return resp, nil
		}
	}

	// Fall back to live search via Node.js scraper
	clips, err := s.searchLive(ctx, req.Term, req.Limit, req.SaveDB)
	if err != nil {
		return nil, fmt.Errorf("live search failed: %w", err)
	}

	// Save live results to main DB if requested or if SaveDB is set
	if req.SaveDB {
		for _, clip := range clips {
			if err := s.clipsRepo.UpsertClip(ctx, &clip); err != nil {
				s.log.Warn("Failed to save live clip to main DB", zap.Error(err), zap.String("clip", clip.Name))
			}
		}
	}

	resp.Source = "live"
	resp.Clips = clips
	return resp, nil
}

// SearchDB searches clips in the database
func (s *Service) SearchDB(ctx context.Context, term string, limit int) ([]models.Clip, error) {
	// Use the clips repo to search
	clips, err := s.clipsRepo.SearchClips(ctx, term)
	if err != nil {
		return nil, err
	}

	// Filter for artlist source only
	var result []models.Clip
	for _, clip := range clips {
		if clip.Source == "artlist" {
			result = append(result, *clip)
			if len(result) >= limit {
				break
			}
		}
	}

	return result, nil
}

// GetPendingTerms returns Artlist search terms that still need syncing.
func (s *Service) GetPendingTerms(ctx context.Context, limit int) ([]string, error) {
	if s.artlistDB == nil {
		return []string{}, nil
	}

	query := `
		SELECT term
		FROM search_terms
		WHERE COALESCE(video_count, 0) = 0 OR scraped = 0
		ORDER BY COALESCE(video_count, 0) ASC, lower(term) ASC
	`
	var args []interface{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.artlistDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	terms := make([]string, 0)
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return nil, err
		}
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		terms = append(terms, term)
	}
	return terms, rows.Err()
}

// markSearchTermScraped updates stats for a search term after a sync run.
func (s *Service) markSearchTermScraped(ctx context.Context, term string, videoCount int) error {
	if s.artlistDB == nil {
		return nil
	}

	_, err := s.artlistDB.ExecContext(ctx, `
		UPDATE search_terms
		SET scraped = 1,
		    last_scraped = CURRENT_TIMESTAMP,
		    video_count = ?
		WHERE lower(term) = lower(?)
	`, videoCount, term)
	return err
}

// searchLive searches using the Node.js scraper
func (s *Service) searchLive(ctx context.Context, term string, limit int, saveDB bool) ([]models.Clip, error) {
	if strings.TrimSpace(s.nodeScraperDir) == "" {
		return nil, fmt.Errorf("node scraper directory is not configured")
	}

	scraperDir := s.nodeScraperDir
	if absDir, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = absDir
	}
	scriptPath := filepath.Join(scraperDir, "artlist_search.js")

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	args := []string{
		scriptPath,
		"--term", term,
		"--limit", strconv.Itoa(limit),
	}
	if saveDB {
		args = append(args, "--save-db")
	}

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = scraperDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("scraper failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var payload nodeScraperResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	results := make([]models.Clip, 0, len(payload.Clips))
	for _, c := range payload.Clips {
		results = append(results, models.Clip{
			ID:          c.ClipID,
			Name:        c.Title,
			ExternalURL: c.PrimaryURL,
			DriveLink:   c.ClipPageURL,
			Source:      "artlist",
			Category:    "dynamic",
			Tags:        []string{term},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		})
	}

	return results, nil
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

// GetClipStatus returns the status of a clip
func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.clipsRepo.GetClipByID(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}

	// Check if local file exists
	hasLocal := false
	if clip.LocalPath != "" {
		if _, err := os.Stat(clip.LocalPath); err == nil {
			hasLocal = true
		}
	}

	return &ClipStatusResponse{
		ClipID:       clip.ID,
		Name:         clip.Name,
		HasLocalFile: hasLocal,
		LocalPath:    clip.LocalPath,
		DriveLink:    clip.DriveLink,
		HasDriveLink: strings.HasPrefix(clip.DriveLink, "https://drive.google.com"),
		FileHash:     clip.FileHash,
		Source:       clip.Source,
		ExternalURL:  clip.ExternalURL,
	}, nil
}

// DownloadClipRequest represents a download request
type DownloadClipRequest struct {
	OutputDir string `json:"output_dir"` // Optional output directory
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
	FolderID string `json:"folder_id"` // Optional Drive folder ID
}

// UploadClipToDriveResponse represents an upload response
type UploadClipToDriveResponse struct {
	OK         bool   `json:"ok"`
	ClipID     string `json:"clip_id"`
	DriveLink  string `json:"drive_link"`
	DownloadLink string `json:"download_link"`
	Error      string `json:"error,omitempty"`
}

// UploadClipToDrive uploads a clip to Google Drive
func (s *Service) UploadClipToDrive(ctx context.Context, clipID string, req *UploadClipToDriveRequest) (*UploadClipToDriveResponse, error) {
	if s.driveClient == nil {
		return nil, fmt.Errorf("drive client not initialized")
	}

	clip, err := s.clipsRepo.GetClipByID(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}

	if clip.LocalPath == "" {
		return nil, fmt.Errorf("clip has no local file to upload")
	}

	if _, err := os.Stat(clip.LocalPath); err != nil {
		return nil, fmt.Errorf("local file not found: %w", err)
	}

	// Determine target folder
	targetFolderID := ""
	if req != nil && req.FolderID != "" {
		targetFolderID = req.FolderID
	} else if s.driveFolderID != "" {
		targetFolderID = s.driveFolderID
		// Create subfolder based on first tag if available
		if len(clip.Tags) > 0 {
			tagName := clip.Tags[0]
			folderID, err := s.driveClient.GetOrCreateFolder(ctx, tagName, targetFolderID)
			if err == nil {
				targetFolderID = folderID
			} else {
				s.log.Warn("Failed to create/get tag folder on drive, using root", zap.Error(err), zap.String("tag", tagName))
			}
		}
	}

	if targetFolderID == "" {
		return nil, fmt.Errorf("no drive folder ID configured or provided")
	}

	// Upload file
	fileName := fmt.Sprintf("%s.mp4", clip.Name)
	driveFile, err := s.driveClient.UploadFile(ctx, fileName, clip.LocalPath, "video/mp4", targetFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to upload to drive: %w", err)
	}

	// Update DB with Drive info
	clip.DriveLink = driveFile.WebViewLink
	// Construct a direct download link (standard Google Drive pattern)
	downloadLink := fmt.Sprintf("https://drive.google.com/uc?id=%s&export=download", driveFile.Id)
	
	if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
		s.log.Error("Failed to update clip with drive link", zap.Error(err), zap.String("clip_id", clipID))
	}

	return &UploadClipToDriveResponse{
		OK:           true,
		ClipID:       clipID,
		DriveLink:    driveFile.WebViewLink,
		DownloadLink: downloadLink,
	}, nil
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
	OK        bool   `json:"ok"`
	ClipID    string `json:"clip_id"`
	Status    string `json:"status"` // downloaded, uploaded, completed
	Error     string `json:"error,omitempty"`
}

// ProcessClip processes a clip: download → upload to Drive → update DB
func (s *Service) ProcessClip(ctx context.Context, req *ProcessClipRequest) (*ProcessClipResponse, error) {
	// If term is provided, search and get clip ID
	clipID := req.ClipID
	if clipID == "" && req.Term != "" {
		// Search for clip
		searchResp, err := s.Search(ctx, &SearchRequest{
			Term:     req.Term,
			Limit:    1,
			PreferDB: true,
		})
		if err != nil || len(searchResp.Clips) == 0 {
			// Search live
			searchResp, err = s.Search(ctx, &SearchRequest{
				Term:   req.Term,
				Limit:  1,
				SaveDB: true,
			})
			if err != nil || len(searchResp.Clips) == 0 {
				return nil, fmt.Errorf("no clip found for term: %s", req.Term)
			}
		}
		if len(searchResp.Clips) > 0 {
			clipID = searchResp.Clips[0].ID
		}
	}

	if clipID == "" {
		return nil, fmt.Errorf("clip ID is required")
	}

	status := "pending"
	
	// Download if needed
	if req.AutoDownload {
		_, err := s.DownloadClip(ctx, clipID, "")
		if err != nil {
			return nil, fmt.Errorf("download failed: %w", err)
		}
		status = "downloaded"
	}

	// Upload to Drive if needed
	if req.AutoUpload {
		_, err := s.UploadClipToDrive(ctx, clipID, nil)
		if err != nil {
			return nil, fmt.Errorf("upload failed: %w", err)
		}
		status = "uploaded"
	}

	return &ProcessClipResponse{
		OK:     true,
		ClipID: clipID,
		Status:  status,
	}, nil
}

// DownloadClip downloads a clip from Artlist external URL
func (s *Service) DownloadClip(ctx context.Context, clipID string, outputDir string) (*DownloadClipResponse, error) {
	clip, err := s.clipsRepo.GetClipByID(ctx, clipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}

	if clip.ExternalURL == "" {
		return nil, fmt.Errorf("clip has no external URL to download from")
	}

	// Set default output directory
	if outputDir == "" {
		outputDir = filepath.Join(s.nodeScraperDir, "..", "downloads", "artlist")
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	// Generate safe filename
	safeName := sanitizeFilename(clip.Name) + ".mp4"
	outputPath := filepath.Join(outputDir, safeName)

	// Download using ffmpeg (HLS support)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-i", clip.ExternalURL, "-c", "copy", "-y", outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}

	// Calculate file hash
	fileHash, err := calculateFileHash(outputPath)
	if err != nil {
		s.log.Warn("Failed to calculate file hash", zap.Error(err))
	}

	// Update clip in DB
	clip.LocalPath = outputPath
	if fileHash != "" {
		clip.FileHash = fileHash
	}
	if err := s.clipsRepo.UpsertClip(ctx, clip); err != nil {
		return nil, fmt.Errorf("failed to update clip: %w", err)
	}

	return &DownloadClipResponse{
		OK:        true,
		ClipID:    clipID,
		LocalPath: outputPath,
		FileHash:  fileHash,
	}, nil
}

// sanitizeFilename removes invalid characters from filename
func sanitizeFilename(name string) string {
	// Remove invalid chars
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, c := range invalid {
		result = strings.ReplaceAll(result, c, "_")
	}
	// Trim spaces
	result = strings.TrimSpace(result)
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}

// calculateFileHash calculates MD5 hash of a file
func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Sync syncs Artlist clips for given terms
func (s *Service) Sync(ctx context.Context, req *SyncRequest) (*SyncResponse, error) {
	resp := &SyncResponse{OK: true}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	terms := req.Terms
	if len(terms) == 0 || req.OnlyPending {
		pending, err := s.GetPendingTerms(ctx, req.Limit)
		if err != nil {
			return nil, err
		}
		terms = pending
	}

	if len(terms) == 0 {
		resp.Requested = 0
		return resp, nil
	}

	if req.Limit > 0 && len(terms) > req.Limit {
		terms = terms[:req.Limit]
	}

	resp.Requested = len(terms)
	
	// Parallel execution with worker pool
	numWorkers := 3
	if len(terms) < numWorkers {
		numWorkers = len(terms)
	}

	termsChan := make(chan string, len(terms))
	for _, t := range terms {
		termsChan <- t
	}
	close(termsChan)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for term := range termsChan {
				clips, err := s.searchLive(ctx, term, req.Limit, req.SaveDB)
				
				mu.Lock()
				if err != nil {
					resp.Failed++
					s.log.Error("Sync failed for term", zap.String("term", term), zap.Error(err))
				} else {
					// Save to main clips repository
					if s.clipsRepo != nil {
						for _, clip := range clips {
							c := clip
							if err := s.clipsRepo.UpsertClip(ctx, &c); err != nil {
								s.log.Warn("Failed to upsert clip to main DB", zap.String("clip_id", c.ID), zap.Error(err))
							}
						}
					}
					resp.Synced++
					resp.SavedClips += len(clips)
					_ = s.markSearchTermScraped(ctx, term, len(clips))
				}
				mu.Unlock()

				// Small delay between searches to be nice to Artlist
				time.Sleep(1 * time.Second)
			}
		}()
	}

	wg.Wait()
	return resp, nil
}

// ReindexRequest represents a reindex request
type ReindexRequest struct {
	Mode         string `json:"mode"` // tags, terms, full
	SourceFilter string `json:"source_filter"`
}

// ReindexResponse represents a reindex response
type ReindexResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// Reindex rebuilds local indexes
func (s *Service) Reindex(ctx context.Context, req *ReindexRequest) (*ReindexResponse, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "full"
	}

	if s.artlistDB == nil {
		return &ReindexResponse{
			OK:      true,
			Message: "artlist database unavailable; nothing to reindex",
		}, nil
	}

	tx, err := s.artlistDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if mode == "terms" || mode == "full" || mode == "tags" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE search_terms
			SET video_count = (
				SELECT COUNT(*)
				FROM video_links v
				WHERE v.search_term_id = search_terms.id
			),
			scraped = CASE
				WHEN EXISTS (
					SELECT 1
					FROM video_links v
					WHERE v.search_term_id = search_terms.id
				) THEN 1
				ELSE scraped
			END
		`); err != nil {
			return nil, err
		}
	}

	if _, err := tx.ExecContext(ctx, "REINDEX"); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "ANALYZE"); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ReindexResponse{
		OK:      true,
		Message: "artlist indexes rebuilt",
	}, nil
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

// PurgeStale removes stale data
func (s *Service) PurgeStale(ctx context.Context, req *PurgeStaleRequest) (*PurgeStaleResponse, error) {
	if s.artlistDB == nil {
		return &PurgeStaleResponse{OK: true}, nil
	}

	olderThanDays := req.OlderThanDays
	if olderThanDays <= 0 {
		olderThanDays = 30
	}

	threshold := time.Now().AddDate(0, 0, -olderThanDays).Format("2006-01-02 15:04:05")
	condition := `
		(COALESCE(video_count, 0) = 0 OR last_scraped IS NULL OR last_scraped < ?)
	`
	var args []interface{}
	args = append(args, threshold)

	if req.OnlyDownloaded {
		condition += `
			AND EXISTS (
				SELECT 1
				FROM video_links v
				WHERE v.search_term_id = search_terms.id AND v.downloaded = 1
			)
			AND NOT EXISTS (
				SELECT 1
				FROM video_links v
				WHERE v.search_term_id = search_terms.id AND v.downloaded = 0
			)
		`
	}

	countQuery := `SELECT COUNT(*) FROM search_terms WHERE ` + condition
	var wouldRemove int
	if err := s.artlistDB.QueryRowContext(ctx, countQuery, args...).Scan(&wouldRemove); err != nil {
		return nil, err
	}

	if req.DryRun {
		return &PurgeStaleResponse{
			OK:          true,
			WouldRemove: wouldRemove,
		}, nil
	}

	tx, err := s.artlistDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	deleteQuery := `DELETE FROM search_terms WHERE ` + condition
	res, err := tx.ExecContext(ctx, deleteQuery, args...)
	if err != nil {
		return nil, err
	}

	removed, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &PurgeStaleResponse{
		OK:      true,
		Removed: int(removed),
	}, nil
}

// Helper function to extract keywords from a search term
func extractKeywords(term string) []string {
	term = strings.ToLower(term)
	words := strings.FieldsFunc(term, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_'
	})

	var keywords []string
	for _, w := range words {
		if len(w) > 2 {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// normalizeURL ensures URL is properly formatted
func normalizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.String()
}

// ensureDir ensures a directory exists
func ensureDir(path string) error {
	return nil // TODO: implement if needed
}
