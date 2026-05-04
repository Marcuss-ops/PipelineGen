package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/internal/service/assetpipeline"
	"velox/go-master/internal/service/audioasset"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

type Service struct {
	cfg               *config.Config
	pythonScriptsDir  string
	outputDir         string
	log               *zap.Logger
	driveClient       *gdrive.Service
	assetDestResolver *assetdestination.Resolver
	audioProcessor    *audioasset.Processor
	mediaFinalizer    *mediaregistry.Finalizer
	assetPipeline     *assetpipeline.Service
	repo              *voiceovers.Repository
}

func NewService(
	cfg *config.Config,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveClient *gdrive.Service,
	mediaFinalizer *mediaregistry.Finalizer,
	assetPipeline *assetpipeline.Service,
	repo *voiceovers.Repository,
) *Service {
	// Create asset destination resolver
	assetDestResolver := assetdestination.NewResolver(cfg, log, driveClient)

	// Create audio asset processor
	audioProcessor := audioasset.NewProcessor(
		pythonScriptsDir,
		driveClient,
		assetDestResolver,
		log,
	)

	return &Service{
		cfg:               cfg,
		pythonScriptsDir:  pythonScriptsDir,
		outputDir:         outputDir,
		log:               log,
		driveClient:       driveClient,
		assetDestResolver: assetDestResolver,
		audioProcessor:    audioProcessor,
		mediaFinalizer:    mediaFinalizer,
		assetPipeline:     assetPipeline,
		repo:              repo,
	}
}



func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	resp, err := s.GenerateBatch(ctx, &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    boolPtr(false),
		UploadDrive:      boolPtr(false),
		SaveDB:           boolPtr(false),
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
		return nil, fmt.Errorf("%s", item.Error)
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
	if boolDefault(req.UploadDrive, false) && req.Destination != nil {
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

	// Check existing via repository (deduplication)
	existing, _ := s.findExisting(ctx, textHash, language, folderID)
	if shouldSkipExisting(existing, req.Strategy) {
		return existingToItem(existing, "skipped_existing")
	}

	// Build audio input for processor
	audioInput := &audioasset.AudioInput{
		ID:            id,
		Text:          req.Text,
		Language:      language,
		OutputDir:     s.outputDir,
		Filename:      filename,
		RemoveSilence: boolDefault(req.RemoveSilence, false),
	}

	// Set destination from original request if upload is enabled
	if req.Destination != nil && boolDefault(req.UploadDrive, false) {
		audioInput.Destination = &assetdestination.ResolveRequest{
			Source:         "voiceover",
			Group:          req.Destination.Group,
			FolderID:       req.Destination.FolderID,
			FolderPath:     req.Destination.FolderPath,
			SubfolderName:  req.Destination.SubfolderName,
			CreateSubfolder: req.Destination.CreateSubfolder,
		}
	}

	// Generate audio via audioasset processor
	result, err := s.audioProcessor.Generate(ctx, audioInput)
	if err != nil {
		return item.fail("generate_failed", err)
	}

	item.LocalPath = result.LocalPath
	item.CleanedPath = result.CleanedPath
	item.FileHash = result.FileHash
	item.DriveLink = result.DriveLink
	item.Voice = language
	item.Status = result.Status

	if result.Status == "" {
		item.Status = "processed"
	}

	// Save to DB if requested
	if boolDefault(req.SaveDB, false) {
		// Build metadata with voiceover-specific fields
		meta := map[string]interface{}{
			"text_hash":     textHash,
			"text_preview":  truncateString(req.Text, 100),
			"language":      item.Language,
			"voice":         item.Voice,
			"strategy":      req.Strategy,
			"request_id":    requestID,
			"cleaned_path":  item.CleanedPath,
		}
		metaJSON, _ := json.Marshal(meta)

		// Use assetpipeline.Finalize if available
		if s.assetPipeline != nil {
			finalizeInput := &assetpipeline.FinalizeInput{
				ID:          item.ID,
				Name:        truncateString(req.Text, 100),
				Filename:    item.Filename,
				Kind:        assetpipeline.AssetKindAudio,
				Source:      "voiceover",
				Group:       dest.Group,
				Subfolder:   "",
				LocalPath:   item.CleanedPath,
				FolderID:    dest.FolderID,
				FolderPath:  dest.FolderPath,
				DriveLink:   item.DriveLink,
				DownloadLink: item.DownloadLink,
				Metadata:    string(metaJSON),
				RequireLocal: false,
				RequireHash:  false,
				RequireDrive: false,
				VerifyDB:    true,
			}

			finalizeResult, err := s.assetPipeline.Finalize(ctx, finalizeInput)
			if err != nil {
				return item.fail("finalize_failed", err)
			}
			if !finalizeResult.OK {
				return item.fail("finalize_failed", fmt.Errorf("%s", finalizeResult.Error))
			}
		} else {
			// Fallback to old method
			mediaRec := &mediaregistry.MediaRecord{
				ID:           item.ID,
				Source:       "voiceover",
				Name:         truncateString(req.Text, 100),
				Filename:     item.Filename,
				FolderID:     dest.FolderID,
				FolderPath:   dest.FolderPath,
				MediaType:    "audio",
				DriveLink:    item.DriveLink,
				DownloadLink: item.DownloadLink,
				FileHash:     item.FileHash,
				LocalPath:    item.CleanedPath,
				Status:       item.Status,
				Metadata:     string(metaJSON),
			}

			finalizeOpts := mediaregistry.FinalizeOptions{
				RequireLocal: false,
				RequireHash:  false,
				RequireDrive: false,
				VerifyDB:    true,
			}

			finalizeResult, err := s.mediaFinalizer.Finalize(ctx, mediaRec, finalizeOpts)
			if err != nil {
				return item.fail("finalize_failed", err)
			}
			if !finalizeResult.OK {
				return item.fail("finalize_failed", fmt.Errorf("%s", finalizeResult.Error))
			}
		}
	}

	return item
}

func (s *Service) resolveDestination(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error) {
	if dest == nil {
		return &ResolvedDestination{}, nil
	}

	resolved, err := s.assetDestResolver.Resolve(ctx, &assetdestination.ResolveRequest{
		Source:         "voiceover",
		Group:          dest.Group,
		FolderID:       dest.FolderID,
		FolderPath:     dest.FolderPath,
		SubfolderName:  dest.SubfolderName,
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

func (s *Service) findExisting(ctx context.Context, textHash, language, folderID string) (*voiceovers.Record, error) {
	if s.repo == nil {
		return nil, nil
	}
	return s.repo.FindExisting(ctx, textHash, language, folderID)
}

func shouldSkipExisting(existing *voiceovers.Record, strategy string) bool {
	if existing == nil {
		return false
	}

	switch strings.ToLower(strategy) {
	case "replace":
		return false
	case "skip":
		// Check if any output exists
		if existing.DriveLink != "" {
			return true
		}
		if existing.CleanedPath != "" {
			if _, err := os.Stat(existing.CleanedPath); err == nil {
				return true
			}
		}
		if existing.LocalPath != "" {
			if _, err := os.Stat(existing.LocalPath); err == nil {
				return true
			}
		}
		return false
	case "verify", "":
		if existing.Status != "processed" {
			return false
		}
		// Verify at least one output exists
		if existing.DriveLink != "" {
			return true
		}
		if existing.CleanedPath != "" {
			if _, err := os.Stat(existing.CleanedPath); err == nil {
				return true
			}
		}
		if existing.LocalPath != "" {
			if _, err := os.Stat(existing.LocalPath); err == nil {
				return true
			}
		}
		return false
	default:
		return existing.Status == "processed"
	}
}

// boolDefault returns the value of the bool pointer, or the default value if nil
func boolDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// boolPtr returns a pointer to the bool value
func boolPtr(b bool) *bool {
	return &b
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



func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

