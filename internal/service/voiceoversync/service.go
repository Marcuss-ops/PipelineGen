package voiceoversync

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/voiceovers"
)

const folderMimeType = "application/vnd.google-apps.folder"

type Service struct {
	driveClient *drive.Service
	log         *zap.Logger
	repo         *voiceovers.Repository
	rootFolderID string
}

func NewService(driveClient *drive.Service, repo *voiceovers.Repository, rootFolderID string, log *zap.Logger) *Service {
	return &Service{
		driveClient: driveClient,
		log:         log,
		repo:         repo,
		rootFolderID: rootFolderID,
	}
}

type Summary struct {
	OK        bool      `json:"ok"`
	RootID    string    `json:"root_id"`
	Synced    int       `json:"synced"`
	Failed    int       `json:"failed"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Error     string    `json:"error,omitempty"`
}

func (s *Service) Sync(ctx context.Context) (*Summary, error) {
	summary := &Summary{
		OK:        true,
		RootID:    s.rootFolderID,
		StartedAt: time.Now().UTC(),
	}

	if s.driveClient == nil {
		summary.OK = false
		summary.Error = "drive client not configured"
		return summary, fmt.Errorf("drive client not configured")
	}

	if s.repo == nil {
		summary.OK = false
		summary.Error = "voiceover repository not configured"
		return summary, fmt.Errorf("voiceover repository not configured")
	}

	if strings.TrimSpace(s.rootFolderID) == "" {
		summary.OK = false
		summary.Error = "voiceover root folder ID not configured"
		return summary, fmt.Errorf("voiceover root folder ID not configured")
	}

	synced, failed, err := s.syncFolderRecursive(ctx, s.rootFolderID, "")
	if err != nil {
		summary.OK = false
		summary.Error = err.Error()
	}

	summary.Synced = synced
	summary.Failed = failed
	summary.EndedAt = time.Now().UTC()

	return summary, err
}

func (s *Service) syncFolderRecursive(ctx context.Context, folderID, folderPath string) (int, int, error) {
	children, err := s.listChildren(ctx, folderID)
	if err != nil {
		return 0, 1, err
	}

	synced := 0
	failed := 0

	for _, child := range children {
		if child == nil {
			continue
		}

		childName := strings.TrimSpace(child.Name)
		if childName == "" {
			childName = child.Id
		}

		childPath := path.Join(folderPath, childName)

		if child.MimeType == folderMimeType {
			subSynced, subFailed, err := s.syncFolderRecursive(ctx, child.Id, childPath)
			if err != nil {
				s.log.Warn("failed to sync subfolder",
					zap.String("folder_id", child.Id),
					zap.Error(err),
				)
			}
			synced += subSynced
			failed += subFailed
		} else if s.isAudioFile(child.Name) {
			if err := s.syncFile(ctx, child, childPath); err != nil {
				s.log.Warn("failed to sync voiceover file",
					zap.String("file_id", child.Id),
					zap.String("name", child.Name),
					zap.Error(err),
				)
				failed++
			} else {
				synced++
			}
		}
	}

	return synced, failed, nil
}

func (s *Service) syncFile(ctx context.Context, file *drive.File, filePath string) error {
	link := strings.TrimSpace(file.WebViewLink)
	if link == "" {
		link = strings.TrimSpace(file.WebContentLink)
	}
	if link == "" {
		link = "https://drive.google.com/file/d/" + file.Id
	}

	// Extract language from filename or path (e.g., test_it.mp3 -> it)
	language := s.extractLanguage(file.Name)

	// Generate an ID based on file hash or drive file ID
	id := "vo_sync_" + file.Id

	// Check if already exists
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Get folder ID for the file
	folderID := ""
	// The parent of the file is the folder it's in
	if len(file.Parents) > 0 {
		folderID = file.Parents[0]
	}

	now := time.Now().UTC()
	rec := &voiceovers.Record{
		ID:           id,
		RequestID:    "sync_" + time.Now().Format("20060102"),
		TextHash:     file.Id, // Use drive file ID as hash for synced files
		TextPreview:  file.Name,
		Language:     language,
		Voice:        "", // Unknown for synced files
		Filename:     file.Name,
		LocalPath:    "",
		CleanedPath:  "",
		FolderID:     folderID,
		FolderPath:   filePath,
		DriveFileID:  file.Id,
		DriveLink:    link,
		DownloadLink: "https://drive.google.com/uc?id=" + file.Id,
		FileHash:     "",
		Status:       "processed",
		Strategy:     "sync",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if existing != nil {
		// Preserve existing fields
		if existing.LocalPath != "" {
			rec.LocalPath = existing.LocalPath
		}
		if existing.CleanedPath != "" {
			rec.CleanedPath = existing.CleanedPath
		}
		if existing.Voice != "" {
			rec.Voice = existing.Voice
		}
		if existing.FileHash != "" {
			rec.FileHash = existing.FileHash
		}
		if !existing.CreatedAt.IsZero() {
			rec.CreatedAt = existing.CreatedAt
		}
	}

	return s.repo.Upsert(ctx, rec)
}

func (s *Service) listChildren(ctx context.Context, folderID string) ([]*drive.File, error) {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	call := s.driveClient.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink, parents)").
		PageSize(1000).
		Context(ctx)

	var files []*drive.File
	err := call.Pages(ctx, func(fl *drive.FileList) error {
		files = append(files, fl.Files...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *Service) isAudioFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	return ext == ".mp3" || ext == ".wav" || ext == ".m4a" || ext == ".aac"
}

func (s *Service) extractLanguage(filename string) string {
	// Try to extract language from filename (e.g., test_it.mp3 -> it)
	base := strings.TrimSuffix(filename, path.Ext(filename))
	parts := strings.Split(base, "_")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		// Check if it's a language code (e.g., "it", "en", "en-US")
		if len(lastPart) >= 2 && len(lastPart) <= 10 {
			return lastPart
		}
	}
	return "unknown"
}
