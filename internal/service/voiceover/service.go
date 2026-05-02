package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"velox/go-master/internal/service/drivedestination"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/pkg/media/audio"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/hashutil"
	driveupload "velox/go-master/internal/upload/drive"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type Service struct {
	cfg              *config.Config
	pythonScriptsDir string
	outputDir        string
	log              *zap.Logger
	driveClient      *gdrive.Service
	driveDestination *drivedestination.Service
	repo             *voiceovers.Repository
}

func NewService(
	cfg *config.Config,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveClient *gdrive.Service,
	driveDestination *drivedestination.Service,
	repo *voiceovers.Repository,
) *Service {
	return &Service{
		cfg:              cfg,
		pythonScriptsDir: pythonScriptsDir,
		outputDir:        outputDir,
		log:              log,
		driveClient:      driveClient,
		driveDestination:  driveDestination,
		repo:             repo,
	}
}

func (s *Service) SetDriveDestination(d *drivedestination.Service) {
	s.driveDestination = d
}

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	resp, err := s.GenerateBatch(ctx, &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    false,
		UploadDrive:      false,
		SaveDB:           false,
		Strategy:         "replace",
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no voiceover generated")
	}

	item := resp.Items[0]
	if item.Status == "failed" {
		return nil, fmt.Errorf(item.Error)
	}

	return &VoiceoverResult{
		OK:    true,
		Voice: item.Voice,
		Path:  item.LocalPath,
	}, nil
}

func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	req = normalizeBatchRequest(req)

	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("text is required")
	}

	requestID := buildRequestID()
	textHash := textToHash(req.Text)

	var dest *ResolvedDestination
	if req.UploadDrive && req.Destination != nil {
		var err error
		dest, err = s.resolveDestination(ctx, req.Destination)
		if err != nil {
			return nil, err
		}
	}

	resp := &BatchResponse{
		OK:        true,
		RequestID: requestID,
	}

	for _, lang := range req.Languages {
		item := s.processLanguage(ctx, requestID, textHash, lang, req, dest)
		if item.Status == "failed" {
			resp.OK = false
		}
		resp.Items = append(resp.Items, item)
	}

	return resp, nil
}

func (s *Service) processLanguage(
	ctx context.Context,
	requestID string,
	textHash string,
	language string,
	req *BatchRequest,
	dest *ResolvedDestination,
) BatchItem {
	filename := s.buildFilename(req, language, textHash)

	folderID := ""
	if dest != nil {
		folderID = dest.FolderID
	}

	id := s.buildVoiceoverID(textHash, language, folderID)

	item := BatchItem{
		ID:       id,
		Language: language,
		Filename: filename,
		Status:   "processing",
	}

	existing, _ := s.findExisting(ctx, textHash, language, "", folderID)
	if shouldSkipExisting(existing, req.Strategy) {
		return existingToItem(existing, "skipped_existing")
	}

	rawPath, genResult, err := s.generateAudio(ctx, req.Text, language, filename)
	if err != nil {
		return item.fail("generate_failed", err)
	}

	item.LocalPath = rawPath
	item.Voice = genResult.Voice

	finalPath := rawPath
	if req.RemoveSilence {
		cleanedPath, err := s.removeSilence(ctx, rawPath)
		if err != nil {
			return item.fail("silence_remove_failed", err)
		}
		finalPath = cleanedPath
		item.CleanedPath = cleanedPath
	}

	fileHash, err := s.hashFile(finalPath)
	if err != nil {
		return item.fail("hash_failed", err)
	}
	item.FileHash = fileHash

	if req.UploadDrive && dest != nil {
		upload, err := s.uploadToDrive(ctx, finalPath, dest.FolderID, filepath.Base(finalPath))
		if err != nil {
			return item.fail("upload_failed", err)
		}
		item.DriveLink = upload.WebViewLink
		item.DownloadLink = upload.DownloadLink
	}

	if req.SaveDB {
		if err := s.saveRecord(ctx, req, item, requestID, textHash, dest); err != nil {
			return item.fail("db_save_failed", err)
		}
	}

	item.Status = "processed"
	return item
}

func (s *Service) resolveDestination(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error) {
	if dest == nil {
		return &ResolvedDestination{}, nil
	}

	resolved, err := s.driveDestination.Resolve(ctx, &drivedestination.Request{
		Group:           dest.Group,
		FolderID:        dest.FolderID,
		FolderPath:      dest.FolderPath,
		SubfolderName:   dest.SubfolderName,
		CreateSubfolder: dest.CreateSubfolder,
	})
	if err != nil {
		return nil, err
	}

	return &ResolvedDestination{
		Group:      resolved.Group,
		FolderID:   resolved.FolderID,
		FolderPath: resolved.FolderPath,
		DriveLink:  resolved.DriveLink,
	}, nil
}

func (s *Service) findExisting(ctx context.Context, textHash, language, voice, folderID string) (*voiceovers.Record, error) {
	if s.repo == nil {
		return nil, nil
	}
	return s.repo.FindExisting(ctx, textHash, language, voice, folderID)
}

func shouldSkipExisting(existing *voiceovers.Record, strategy string) bool {
	if existing == nil {
		return false
	}

	switch strings.ToLower(strategy) {
	case "replace":
		return false
	case "skip":
		return existing.DriveLink != "" || existing.LocalPath != ""
	case "verify", "":
		return existing.Status == "processed" && (existing.DriveLink != "" || existing.CleanedPath != "" || existing.LocalPath != "")
	default:
		return existing.Status == "processed"
	}
}

func existingToItem(existing *voiceovers.Record, status string) BatchItem {
	return BatchItem{
		ID:           existing.ID,
		Language:     existing.Language,
		Voice:        existing.Voice,
		Filename:     existing.Filename,
		LocalPath:    existing.LocalPath,
		CleanedPath:  existing.CleanedPath,
		DriveLink:    existing.DriveLink,
		DownloadLink: existing.DownloadLink,
		FileHash:     existing.FileHash,
		Status:       status,
	}
}

func (s *Service) hashFile(path string) (string, error) {
	return hashutil.MD5File(path)
}

func (s *Service) saveRecord(ctx context.Context, req *BatchRequest, item BatchItem, requestID string, textHash string, dest *ResolvedDestination) error {
	if s.repo == nil {
		return nil
	}

	rec := &voiceovers.Record{
		ID:          item.ID,
		RequestID:   requestID,
		TextHash:    textHash,
		TextPreview: truncateString(req.Text, 100),
		Language:    item.Language,
		Voice:       item.Voice,
		Filename:    item.Filename,
		LocalPath:   item.LocalPath,
		CleanedPath: item.CleanedPath,
		Status:      item.Status,
		FileHash:    item.FileHash,
		Strategy:    req.Strategy,
	}

	if dest != nil {
		rec.FolderID = dest.FolderID
		rec.FolderPath = dest.FolderPath
	}

	if item.DriveLink != "" {
		rec.DriveLink = item.DriveLink
		rec.DownloadLink = item.DownloadLink
		rec.Status = "processed"
	}

	metadata := ""
	if req.Metadata != nil {
		b, _ := json.Marshal(req.Metadata)
		metadata = string(b)
	}
	rec.Metadata = metadata

	return s.repo.Upsert(ctx, rec)
}

func (s *Service) removeSilence(ctx context.Context, input string) (string, error) {
	ext := filepath.Ext(input)
	base := strings.TrimSuffix(input, ext)
	output := base + ".cleaned.mp3"

	if err := audio.RemoveSilence(ctx, s.cfg.External.FfmpegPath, input, output); err != nil {
		return "", err
	}

	return output, nil
}

func (s *Service) uploadToDrive(ctx context.Context, path, folderID, filename string) (*driveupload.UploadResult, error) {
	uploader := &driveupload.Uploader{
		Service: s.driveClient,
		Log:     s.log,
	}

	return uploader.UploadFile(ctx, path, folderID, filename)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
