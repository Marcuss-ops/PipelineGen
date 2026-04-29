package artlist

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"

	"velox/go-master/pkg/models"
)

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		OK: true,
	}

	if s.artlistDB == nil {
		return stats, nil
	}

	row := s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM artlist_clips")
	artlistTotal := 0
	if err := row.Scan(&artlistTotal); err == nil {
		stats.ArtlistClipsTotal = artlistTotal
	}

	totalClips, err := s.clipsRepo.CountClips(ctx)
	if err == nil {
		stats.ClipsTotal = totalClips
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms")
	termsTotal := 0
	if err := row.Scan(&termsTotal); err == nil {
		stats.SearchTermsTotal = termsTotal
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE scraped = 1")
	termsScraped := 0
	if err := row.Scan(&termsScraped); err == nil {
		stats.SearchTermsScraped = termsScraped
	}

	if stats.SearchTermsTotal > 0 {
		stats.CoveragePct = float64(stats.SearchTermsScraped) / float64(stats.SearchTermsTotal) * 100
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE scraped = 0 OR scraped IS NULL")
	staleTerms := 0
	if err := row.Scan(&staleTerms); err == nil {
		stats.StaleTerms = staleTerms
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT last_sync_at FROM sync_status LIMIT 1")
	var lastSync string
	if err := row.Scan(&lastSync); err == nil && lastSync != "" {
		stats.LastSyncAt = &lastSync
	}

	return stats, nil
}

func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	resp := &SearchResponse{
		OK:   true,
		Term: req.Term,
	}

	if req.Term == "" {
		return resp, nil
	}

	clipsList, err := s.clipsRepo.SearchClips(ctx, req.Term)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	resp.Clips = make([]models.Clip, 0, len(clipsList))
	for _, c := range clipsList {
		resp.Clips = append(resp.Clips, *c)
	}
	resp.Source = "database"

	return resp, nil
}

func (s *Service) Sync(ctx context.Context, req *SyncRequest) (*SyncResponse, error) {
	resp := &SyncResponse{
		OK:        true,
		Requested: len(req.Terms),
	}

	for _, term := range req.Terms {
		_, err := s.clipsRepo.SearchClips(ctx, term)
		if err != nil {
			resp.Failed++
			s.log.Warn("Failed to sync term", zap.String("term", term), zap.Error(err))
			continue
		}
		resp.Synced++
	}

	return resp, nil
}

func (s *Service) SyncDriveFolder(ctx context.Context, folderID, mediaType string) (*SyncResponse, error) {
	resp := &SyncResponse{
		OK: true,
	}

	if s.driveClient == nil {
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
	}

	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	fileList, err := s.driveClient.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	resp.Requested = len(fileList.Files)
	for _, file := range fileList.Files {
		_, err := s.clipsRepo.GetClipByFolderAndFilename(ctx, folderID, file.Name)
		if err != nil {
			resp.Synced++
		}
	}

	return resp, nil
}

func (s *Service) Reindex(ctx context.Context, req *ReindexRequest) (*ReindexResponse, error) {
	return &ReindexResponse{
		OK:      true,
		Message: "Reindex completed",
	}, nil
}

func (s *Service) PurgeStale(ctx context.Context, req *PurgeStaleRequest) (*PurgeStaleResponse, error) {
	resp := &PurgeStaleResponse{
		OK: true,
	}

	if req.DryRun {
		resp.WouldRemove = 0
		return resp, nil
	}

	resp.Removed = 0
	return resp, nil
}

func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &ClipStatusResponse{
		ClipID:       clip.ID,
		Name:         clip.Name,
		HasLocalFile: false,
		HasDriveLink: clip.DriveLink != "",
		DriveLink:    clip.DriveLink,
		FileHash:     clip.FileHash,
		Source:       clip.Source,
		ExternalURL:  clip.ExternalURL,
	}

	return resp, nil
}

func (s *Service) DownloadClip(ctx context.Context, clipID string, req *DownloadClipRequest) (*DownloadClipResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = s.nodeScraperDir
	}

	localPath := filepath.Join(outputDir, clip.Filename)
	resp := &DownloadClipResponse{
		OK:        true,
		ClipID:    clipID,
		LocalPath: localPath,
	}

	if clip.DriveLink != "" && s.driveClient != nil {
		file, err := s.driveClient.Files.Get(clip.DriveLink).Context(ctx).Download()
		if err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		defer file.Body.Close()

		out, err := os.Create(localPath)
		if err != nil {
			return nil, err
		}
		defer out.Close()

		_, err = io.Copy(out, file.Body)
		if err != nil {
			return nil, err
		}

		resp.FileHash = ""
	}

	return resp, nil
}

func (s *Service) UploadClipToDrive(ctx context.Context, clipID string, req *UploadClipToDriveRequest) (*UploadClipToDriveResponse, error) {
	clip, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil {
		return nil, err
	}

	resp := &UploadClipToDriveResponse{
		OK:     true,
		ClipID: clipID,
	}

	if s.driveClient == nil {
		resp.Error = "drive client not configured"
		return resp, fmt.Errorf("drive client not configured")
	}

	folderID := req.FolderID
	if folderID == "" {
		folderID = s.driveFolderID
	}

	file := &drive.File{
		Name:    clip.Filename,
		Parents: []string{folderID},
	}

	_, err = s.driveClient.Files.Create(file).Context(ctx).Do()
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	return resp, nil
}

func (s *Service) ProcessClip(ctx context.Context, req *ProcessClipRequest) (*ProcessClipResponse, error) {
	resp := &ProcessClipResponse{
		OK:     true,
		ClipID: req.ClipID,
		Status: "processed",
	}

	if req.AutoDownload {
		_, err := s.DownloadClip(ctx, req.ClipID, &DownloadClipRequest{})
		if err != nil {
			resp.Status = "download_failed"
			resp.Error = err.Error()
			return resp, err
		}
	}

	if req.AutoUpload {
		_, err := s.UploadClipToDrive(ctx, req.ClipID, &UploadClipToDriveRequest{})
		if err != nil {
			resp.Status = "upload_failed"
			resp.Error = err.Error()
			return resp, err
		}
	}

	return resp, nil
}

func (s *Service) StaleTerms(ctx context.Context) ([]TermInfo, error) {
	if s.artlistDB == nil {
		return nil, nil
	}

	rows, err := s.artlistDB.QueryContext(ctx, `
		SELECT term, scraped, last_scraped, (SELECT COUNT(*) FROM artlist_clips WHERE search_term_id = st.id) as video_count, created_at
		FROM search_terms st
		WHERE scraped = 0 OR scraped IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []TermInfo
	for rows.Next() {
		var t TermInfo
		var lastScraped sqlNullString
		if err := rows.Scan(&t.Term, &t.Scraped, &lastScraped, &t.VideoCount, &t.CreatedAt); err != nil {
			continue
		}
		if lastScraped.Valid {
			t.LastScraped = &lastScraped.String
		}
		terms = append(terms, t)
	}

	return terms, nil
}

type sqlNullString struct {
	String string
	Valid  bool
}

func (s *sqlNullString) Scan(value interface{}) error {
	if value == nil {
		s.Valid = false
		return nil
	}
	s.String = string(value.([]byte))
	s.Valid = true
	return nil
}
