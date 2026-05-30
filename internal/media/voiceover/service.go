package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/audioasset"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/ptrutil"
	"velox/go-master/internal/upload/drive"

	"go.uber.org/zap"
)

// SemanticTaggerFunc is a function that calls the Python semantic tagger.
// Defined as a callback to avoid circular imports with the images package.
type SemanticTaggerFunc func(ctx context.Context, prompt, style, mediaType, generator string) (*SemanticTaggerResult, error)

// SemanticTaggerResult mirrors the semantic tagger output for voiceover use.
type SemanticTaggerResult struct {
	SearchText string   `json:"search_text"`
	Tags       []string `json:"tags"`
	Subjects   []string `json:"subjects"`
	Mood       []string `json:"mood"`
}

type Service struct {
	cfg               *config.Config
	pythonScriptsDir  string
	outputDir         string
	log               *zap.Logger
	driveUploader     *drive.Uploader
	assetDestResolver destination.Resolver
	audioProcessor    *audioasset.Processor
	lifecycleService  *lifecycle.Service
	semanticTagger    SemanticTaggerFunc
}

func NewService(
	cfg *config.Config,
	pythonScriptsDir string,
	outputDir string,
	log *zap.Logger,
	driveUploader *drive.Uploader,
	lifecycleService *lifecycle.Service,
	assetDestResolver destination.Resolver,
) *Service {
	// Create audio asset processor
	audioProcessor := audioasset.NewProcessor(
		pythonScriptsDir,
		driveUploader,
		assetDestResolver,
		log,
	)

	return &Service{
		cfg:               cfg,
		pythonScriptsDir:  pythonScriptsDir,
		outputDir:         outputDir,
		log:               log,
		driveUploader:     driveUploader,
		assetDestResolver: assetDestResolver,
		audioProcessor:    audioProcessor,
		lifecycleService:  lifecycleService,
	}
}

// RegisterHandler registers this service as a handler for voiceover jobs
func (s *Service) RegisterHandler(jobsSvc *jobservice.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(models.JobTypeVoiceoverBatch, s.HandleJob)
		s.log.Info("registered voiceover job handler")
	}
}

// SetSemanticTagger sets the callback function for semantic metadata enrichment.
// Must be called after construction to enable search_text/tags on voiceovers.
func (s *Service) SetSemanticTagger(fn SemanticTaggerFunc) {
	s.semanticTagger = fn
}

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	req := &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    boolPtr(false),
		Strategy:         "replace",
	}
	if s.cfg.Drive.RootFolder() != "" {
		req.Destination = &DestinationRequest{
			FolderID: s.cfg.Drive.RootFolder(),
		}
	}
	resp, err := s.GenerateBatch(ctx, req)
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
		OK:          true,
		Voice:       item.Voice,
		Path:        item.LocalPath,
		DriveLink:   item.DriveLink,
		DriveFileID: item.DriveFileID,
	}, nil
}

func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	req = normalizeBatchRequest(req)

	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("text is required")
	}

	requestID := buildRequestID()
	textHash := textToHash(req.Text)

	destinationReq := req.Destination
	if destinationReq == nil && strings.TrimSpace(s.cfg.Drive.RootFolder()) != "" {
		destinationReq = &DestinationRequest{
			FolderID: s.cfg.Drive.RootFolder(),
		}
	}

	var dest *ResolvedDestination
	if destinationReq != nil {
		var err error
		dest, err = s.resolveDestination(ctx, destinationReq)
		if err != nil {
			return nil, err
		}
	}

	// Ensure dest is not nil to avoid panics when accessing fields
	if dest == nil {
		dest = &ResolvedDestination{}
	}

	if dest.FolderID == "" && s.cfg.Drive.RootFolder() != "" {
		dest.FolderID = s.cfg.Drive.RootFolder()
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

	id := buildVoiceoverID(textHash, language, folderID)

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
		RemoveSilence: ptrutil.BoolDefault(req.RemoveSilence, false),
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
		"text_hash":    textHash,
		"text_preview": truncateString(req.Text, 100),
		"language":     item.Language,
		"voice":        item.Voice,
		"strategy":     req.Strategy,
		"request_id":   requestID,
		"cleaned_path": item.CleanedPath,
	}

	// Call semantic tagger for rich metadata (search_text, tags)
	if s.semanticTagger != nil {
		semResult, err := s.semanticTagger(ctx, req.Text, "", "voiceover", "voiceover")
		if err != nil {
			s.log.Warn("processLanguage: semantic tagger failed", zap.Error(err))
		} else {
			meta["search_text"] = semResult.SearchText
			meta["semantic_tags"] = semResult.Tags
			meta["semantic_subjects"] = semResult.Subjects
			meta["semantic_mood"] = semResult.Mood
			item.SearchText = semResult.SearchText
		}
	}
	metaJSON, _ := json.Marshal(meta)

	localPath := item.CleanedPath
	if localPath == "" {
		localPath = item.LocalPath
	}

	// Create FinalizeInput for LifecycleService
	input := &lifecycle.FinalizeInput{
		ID:           item.ID,
		Name:         truncateString(req.Text, 100),
		Filename:     item.Filename,
		Kind:         lifecycle.AssetKindAudio,
		Source:       "voiceover",
		Group:        dest.Group,
		Subfolder:    "",
		LocalPath:    localPath,
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
		Source:          "voiceover",
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
		FolderID:   resolved.FolderID,
		FolderPath: resolved.FolderPath,
		DriveLink:  resolved.DriveLink,
	}, nil
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
