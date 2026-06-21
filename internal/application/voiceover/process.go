package voiceover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

func (s *Service) Generate(ctx context.Context, text, language, filename string) (*VoiceoverResult, error) {
	req := &BatchRequest{
		Text:             text,
		Languages:        []string{language},
		FilenameTemplate: filename,
		RemoveSilence:    ptrutil.Bool(false),
		Strategy:         "replace",
	}
	if s.cfg.Drive.VoiceoverFolder() != "" {
		req.Destination = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
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
	if item.Error != "" {
		return nil, fmt.Errorf("%s (status: %s)", item.Error, item.Status)
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
	textHash := hashutil.SHA256String(req.Text)

	destinationReq := req.Destination
	if destinationReq == nil && s.cfg.Drive.VoiceoverFolder() != "" {
		destinationReq = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
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

	if dest.FolderID == "" && s.cfg.Drive.VoiceoverFolder() != "" {
		dest.FolderID = s.cfg.Drive.VoiceoverFolder()
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

	if req.Strategy == "replace" {
		s.log.Info("processLanguage: strategy is replace, deleting existing record", zap.String("id", id))
		if _, err := s.db.ExecContext(ctx, "DELETE FROM voiceovers WHERE id = ?", id); err != nil {
			s.log.Warn("failed to delete existing voiceover record", zap.String("id", id), zap.Error(err))
		}
	} else if folderID != "" {
		var existingDriveLink string
		err := s.db.QueryRowContext(ctx, "SELECT drive_link FROM voiceovers WHERE id = ?", id).Scan(&existingDriveLink)
		if err == nil && existingDriveLink == "" {
			s.log.Info("processLanguage: existing record has no drive link, deleting to force regeneration and upload", zap.String("id", id))
			if _, err := s.db.ExecContext(ctx, "DELETE FROM voiceovers WHERE id = ?", id); err != nil {
				s.log.Warn("failed to delete empty-drive-link voiceover record", zap.String("id", id), zap.Error(err))
			}
		}
	}

	item := BatchItem{
		ID:       id,
		Language: language,
		Filename: filename,
		Status:   "processing",
	}

	outputDir := s.outputDir
	if req.Destination != nil && req.Destination.CreateSubfolder && req.Destination.SubfolderName != "" {
		outputDir = filepath.Join(s.outputDir, req.Destination.SubfolderName)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			s.log.Warn("failed to create local subfolder for voiceover", zap.String("dir", outputDir), zap.Error(err))
			outputDir = s.outputDir
		}
	}

	// Build audio input for processor
	audioInput := &audioasset.AudioInput{
		Text:          req.Text,
		Language:      language,
		OutputDir:     outputDir,
		Filename:      filename,
		RemoveSilence: ptrutil.BoolDefault(req.RemoveSilence, false),
	}
	if dest != nil && dest.FolderID != "" {
		audioInput.Destination = &asset.ResolveRequest{
			Source:     "voiceover",
			FolderID:   dest.FolderID,
			FolderPath: dest.FolderPath,
			Group:      dest.Group,
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
	item.DriveFileID = result.DriveFileID
	item.Voice = language
	item.Status = result.Status

	if result.Status == "" {
		item.Status = "processed"
	}

	// Process through LifecycleService (dedupe + upload + persist)
	meta := map[string]any{
		"text_hash":    textHash,
		"text_preview": textutil.Truncate(req.Text, 100),
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

	// Trigger embedding generation + Qdrant upsert (async, non-blocking).
	// context.WithoutCancel(ctx) detaches from the caller's cancellation
	// (e.g. HTTP handler with defer cancel()) so the goroutine survives
	// the handler return. 2-min timeout prevents leaks.
	if s.clipIndexer != nil && item.ID != "" {
		concurrent.SafeGoFunc("voiceover-indexing", item.ID, func(voiceoverID string) {
			indexCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := s.clipIndexer(indexCtx, voiceoverID); err != nil {
				s.log.Warn("voiceover indexing failed (non-fatal)",
					zap.String("voiceover_id", voiceoverID), zap.Error(err))
			}
		})
	}

	localPath := item.CleanedPath
	if localPath == "" {
		localPath = item.LocalPath
	}

	// Create FinalizeInput for LifecycleService
	input := &lifecycle.FinalizeInput{
		ID:           item.ID,
		Name:         textutil.Truncate(req.Text, 100),
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

	resolved, err := s.assetDestResolver.Resolve(ctx, &asset.ResolveRequest{
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

// GeneratePromo translates text to multiple languages via Ollama then generates
// a voiceover for each. This replaces scripts/generate_promo_voiceovers.py.
