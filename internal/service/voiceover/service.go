package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/service/assetdestination"
	"velox/go-master/internal/service/assetpipeline"
	"velox/go-master/internal/service/audioasset"
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
	assetDestResolver destination.Resolver
	audioProcessor    *audioasset.Processor
	lifecycleService  *assetpipeline.LifecycleService
}

func NewService(
	cfg *config.Config,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveClient *gdrive.Service,
	lifecycleService *assetpipeline.LifecycleService,
) *Service {
	// Create asset destination resolver
	assetDestResolver := assetdestination.NewResolver(cfg, log, driveClient)

	// Create audio asset processor
	audioProcessor := audioasset.NewProcessor(
		pythonScriptsDir,
		driveClient,
		assetdestination.ToCoreResolver(assetDestResolver),
		log,
	)

	return &Service{
		cfg:               cfg,
		pythonScriptsDir:  pythonScriptsDir,
		outputDir:         outputDir,
		log:               log,
		driveClient:       driveClient,
		assetDestResolver: assetdestination.ToCoreResolver(assetDestResolver),
		audioProcessor:    audioProcessor,
		lifecycleService:  lifecycleService,
	}
}



func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	resp, err := s.GenerateBatch(ctx, &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    boolPtr(false),
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
	if req.Destination != nil {
		var err error
		dest, err = s.resolveDestination(ctx, req.Destination)
		if err != nil {
			return nil, err
		}
	}

	// Ensure dest is not nil to avoid panics when accessing fields
	if dest == nil {
		dest = &ResolvedDestination{}
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

	// Build audio input for processor
	audioInput := &audioasset.AudioInput{
		Text:          req.Text,
		Language:      language,
		OutputDir:     s.outputDir,
		Filename:      filename,
		RemoveSilence: boolDefault(req.RemoveSilence, false),
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
	item.DriveFileID = result.DriveFileID
	item.Voice = language
	item.Status = result.Status

	if result.Status == "" {
		item.Status = "processed"
	}

	// Process through LifecycleService (dedupe + upload + persist)
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

	// Create FinalizeInput for LifecycleService
	input := &assetpipeline.FinalizeInput{
		ID:           item.ID,
		Name:         truncateString(req.Text, 100),
		Filename:     item.Filename,
		Kind:         assetpipeline.AssetKindAudio,
		Source:       "voiceover",
		Group:        dest.Group,
		Subfolder:    "",
		LocalPath:    item.CleanedPath,
		FolderID:     dest.FolderID,
		FolderPath:   dest.FolderPath,
		DriveLink:    item.DriveLink,
		DriveFileID:  item.DriveFileID,
		DownloadLink: item.DownloadLink,
		FileHash:     item.FileHash,
		Metadata:     string(metaJSON),
		RequireLocal: false,
		RequireHash:  false,
		RequireDrive: item.DriveLink != "",
		VerifyDB:     true,
	}

	// Process through lifecycle (dedupe + upload + persist)
	lifecycleResult, err := s.lifecycleService.ProcessAsset(ctx, input, item.FileHash)
	if err != nil {
		return item.fail("lifecycle_failed", err)
	}
	if !lifecycleResult.OK {
		return item.fail("lifecycle_failed", fmt.Errorf("%s", lifecycleResult.Error))
	}

	// Update item with results
	item.DriveLink = lifecycleResult.DriveLink
	item.DriveFileID = lifecycleResult.DriveFileID
	item.DownloadLink = lifecycleResult.DownloadLink
	item.Status = "processed"
	return item
}

func (s *Service) resolveDestination(ctx context.Context, dest *DestinationRequest) (*ResolvedDestination, error) {
	if dest == nil {
		return &ResolvedDestination{}, nil
	}

	resolved, err := s.assetDestResolver.Resolve(ctx, &destination.ResolveRequest{
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
		FolderID:   resolved.FolderID,
		FolderPath: resolved.FolderPath,
		DriveLink:  resolved.DriveLink,
	}, nil
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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}


