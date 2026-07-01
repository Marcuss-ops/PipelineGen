package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// uploadAndIndexChunk uploads a rendered chunk to Drive, saves metadata to
// asset_index and media_assets, and triggers vector indexing via the
// canonical outbox dispatcher (atomic upsert + outbox enqueue).
//
// Blocco 1b (July 2026): now returns error instead of void. The caller
// MUST NOT append the chunk to PipelineResult.Chunks when error != nil.
// On success the chunk's Uploaded is set to true; Indexed tracks whether
// the asset_index + outbox dispatch completed without error (best-effort
// — a false Indexed does not block the chunk from the result).
func (s *Service) uploadAndIndexChunk(ctx context.Context, chunkIdx int, chunkPath, chunkTitle, folderID string, chunkRes *ChunkResult, input *RunInput, videoCfg *config.VideoConfig) error {
	s.log.Info("uploading chunk to drive",
		zap.Int("chunk", chunkIdx),
		zap.String("chunk_title", chunkTitle),
		zap.String("folder_id", folderID),
		zap.String("local_path", chunkPath),
	)

	var fileID, webViewLink, downloadLink string
	// F2.10: upload via canonical Publisher ALWAYS (override brutal —
	// legacy driveAdmin.UploadFile fallback is gone). When subfolder
	// and folderName are both empty, the Publisher.Publish falls back
	// to the resolved folderID passed into this function via the
	// RootFolderOverride, ensuring the chunk lands in the right
	// folder regardless of which path the caller used.
	pubReq := delivery.PublishRequest{
		Destination: delivery.DestinationStock,
		LocalPath:   chunkPath,
		Filename:    chunkTitle + ".mp4",
		Group:       input.Subfolder,
		Subject:     input.FolderName,
	}
	if input.FolderID != "" {
		pubReq.RootFolderOverride = input.FolderID
	}
	if pubReq.Group == "" && pubReq.Subject == "" && pubReq.RootFolderOverride == "" && folderID != "" {
		pubReq.RootFolderOverride = folderID
	}
	pubResult, err := s.publisher.Publish(ctx, pubReq)
	if err != nil {
		s.log.Error("failed to publish chunk to drive", zap.Int("chunk", chunkIdx), zap.Error(err))
		return fmt.Errorf("publish chunk %d: %w", chunkIdx, err)
	}
	chunkRes.Uploaded = true
	fileID = pubResult.FileID
	webViewLink = pubResult.WebViewLink
	// F1.5 (P0 #9): read DownloadLink from the canonical
	// PublishResult instead of reconstructing via string
	// interpolation. Stock pipeline consumers downstream read
	// this into chunkRes.DownloadLink → assetindex.AssetRecord.DownloadLink
	// → Qdrant projection, so centralising the URL here closes
	// the format-drift failure surface end-to-end.
	downloadLink = pubResult.DownloadLink

	chunkRes.DriveLink = webViewLink
	chunkRes.DownloadLink = downloadLink
	chunkRes.DriveFileID = fileID
	chunkRes.Title = chunkTitle

	s.log.Info("stock pipeline upload completed",
		zap.String("file", chunkTitle+".mp4"),
		zap.String("drive_link", webViewLink),
	)

	// Track indexing outcome. Failures in asset_index / outbox dispatch
	// are best-effort (logged but not returned as errors — the chunk is
	// already on Drive and the operator can backfill). Indexed=false is
	// an operator-visible signal, not a hard failure.
	indexed := true
	if s.assetIndex != nil {
		if ok := s.indexChunkToAssetIndex(ctx, chunkIdx, chunkTitle, folderID, chunkPath, chunkRes, input, fileID, webViewLink, downloadLink, videoCfg); !ok {
			indexed = false
		}
	}
	chunkRes.Indexed = indexed
	return nil

// indexChunkToAssetIndex saves the chunk record to the asset_index table.
// Returns true when all indexing steps (asset_index upsert + clips DB
// upsert + outbox dispatch) completed without error.
func (s *Service) indexChunkToAssetIndex(ctx context.Context, chunkIdx int, chunkTitle, folderID, chunkPath string, chunkRes *ChunkResult, input *RunInput, driveFileID, driveLink, downloadLink string, videoCfg *config.VideoConfig) bool {
	assetID := "stock_" + driveFileID
	s.log.Info("upserting chunk into asset_index",
		zap.Int("chunk", chunkIdx),
		zap.String("asset_id", assetID),
		zap.String("group_name", input.FolderName),
	)

	meta := semantic.BuildAssetMetadata(semantic.AssetSemanticInput{
		AssetID:             assetID,
		AssetType:           "stock_clip",
		Source:              "stock",
		MediaType:           "video",
		Generator:           "stock-pipeline",
		PromptOriginal:      strings.Join(append(append([]string{}, input.SearchQueries...), input.DirectURLs...), " | "),
		SemanticDescription: semantic.MergeMetadataSearchText(chunkTitle, input.FolderName, input.Subfolder),
		SearchText:          semantic.MergeMetadataSearchText(chunkTitle, input.FolderName, input.Subfolder, strings.Join(input.SearchQueries, " ")),
		Subjects:            semantic.AppendUniqueStrings(nil, input.FolderName, input.Subfolder),
		Tags:                semantic.AppendUniqueStrings(nil, chunkTitle, input.FolderName, input.Subfolder, "stock", "clip"),
		Categories:          semantic.AppendUniqueStrings(nil, "file", "stock", "clip"),
		Confidence:          0.75,
		EmbeddingStatus:     "ready",
		Extra: map[string]any{
			"filename":         chunkTitle + ".mp4",
			"folder_id":        folderID,
			"folder_path":      input.Subfolder + "/" + input.FolderName + "/" + chunkTitle + ".mp4",
			"media_type":       "stock_clip",
			"category":         "file",
			"source_video_ids": textutil.UniqueStringsVar(chunkRes.SourceIDs...),
		},
	}, nil)
	metaJSON, _ := json.Marshal(meta)
	rec := &assetindex.AssetRecord{
		AssetID:      assetID,
		AssetType:    "stock_clip",
		Source:       "stock",
		SourceID:     driveFileID,
		GroupName:    input.FolderName,
		Subfolder:    input.Subfolder,
		LocalPath:    chunkPath,
		DriveLink:    driveLink,
		DownloadLink: downloadLink,
		Status:       "ready",
		Metadata:     string(metaJSON),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.assetIndex.Upsert(ctx, rec); err != nil {
		s.log.Warn("failed to save chunk to asset_index", zap.Int("chunk", chunkIdx), zap.Error(err))
		return false
	} else {
		s.log.Info("chunk saved to asset_index", zap.String("asset_id", assetID))
	}

	// Save to media_assets (clips DB)
	if s.clipsRepo != nil {
		if !s.indexChunkToClipsDB(ctx, chunkIdx, chunkTitle, folderID, chunkPath, chunkRes, input, driveFileID, driveLink, downloadLink, videoCfg) {
			return false
		}
	}
	return true
}

// indexChunkToClipsDB saves the chunk to the media_assets table for semantic search.
// Returns true when the upsert + outbox dispatch completed without error.
func (s *Service) indexChunkToClipsDB(ctx context.Context, chunkIdx int, chunkTitle, folderID, chunkPath string, chunkRes *ChunkResult, input *RunInput, driveFileID, driveLink, downloadLink string, videoCfg *config.VideoConfig) bool {
	tags := []string{"stock", "clip"}
	tags = append(tags, input.FolderName)
	if input.Subfolder != "" {
		tags = append(tags, input.Subfolder)
	}
	for _, q := range input.SearchQueries {
		tags = append(tags, q)
	}

	clip := &asset.Asset{
		ID:         driveFileID,
		Name:       chunkTitle + ".mp4",
		Filename:   chunkTitle + ".mp4",
		Group:      "stock",
		MediaType:  asset.MediaType("video"),
		Tags:       tags,
		Source:     asset.Source("stock"),
		Category:   "file",
		Duration:   time.Duration(videoCfg.ChunkDuration) * time.Second,
		SearchText: semantic.MergeMetadataSearchText(chunkTitle, input.FolderName, input.Subfolder, strings.Join(input.SearchQueries, " "), strings.Join(textutil.UniqueStringsVar(chunkRes.SourceIDs...), " ")),
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	clip.SetFolderID(folderID)
	clip.SetFolderPath(input.Subfolder + "/" + input.FolderName)
	clip.SetDriveLink(driveLink)
	clip.SetDriveFileID(driveFileID)
	clip.SetDownloadLink(downloadLink)
	clip.SetLocalPath(chunkPath)
	clip.SetMetadataString("status", "ready")
	clip.SetMetadataString("search_text", clip.SearchText)
	clip.SetMetadataString("chunk_index", fmt.Sprintf("%d", chunkIdx))
	clip.SetMetadataString("timeline_start", formatDuration(chunkRes.TimelineStart))
	clip.SetMetadataString("timeline_end", formatDuration(chunkRes.TimelineEnd))
	if len(chunkRes.SourceIDs) > 0 {
		chunkMeta := map[string]any{}
		chunkMeta["source_video_ids"] = textutil.UniqueStringsVar(chunkRes.SourceIDs...)
		b, _ := json.Marshal(chunkMeta)
		clip.SetMetadataString("stock_chunk_metadata", string(b))
	}

	if err := s.upsertChunkAndDispatch(ctx, clip); err != nil {
		s.log.Warn("failed to upsert/dispatch chunk", zap.String("clip_id", clip.ID), zap.Error(err))
		return false
	}
	return true
}

// upsertChunkAndDispatch writes the chunk to media_assets and routes it
// through the canonical outbox dispatcher (atomic upsert + outbox
// enqueue, picked up by outbox.Worker for Qdrant indexing).
// UpdateSearchTerms runs BEFORE the upsert+dispatch so a
// failure aborts the dispatch rather than leaving a stale-tags row that
// only the worker would ever repair (the worker doesn't backfill tags).
func (s *Service) upsertChunkAndDispatch(ctx context.Context, clip *asset.Asset) error {
	// Always update search terms FIRST against the canonical row so the
	// tags_norm / search_text columns never lag the embeddable record.
	// Skipped for folder rows because UpdateSearchTerms rejects them in
	// assets.ClipsRepository.
	if clip.SearchText != "" && !clip.IsFolder() && s.clipsRepo != nil {
		if err := s.clipsRepo.UpdateSearchTerms(ctx, clip.ID, string(clip.Source), clip.Name, clip.Tags, clip.SearchText); err != nil {
			s.log.Warn("failed to update search terms for stock clip", zap.String("clip_id", clip.ID), zap.Error(err))
		}
	}

	if s.dispatcher == nil {
		return fmt.Errorf("upsertChunkAndDispatch: dispatcher is nil — production wiring required")
	}

	// Canonical PR3-5b path: atomic upsert + outbox enqueue + idempotent
	// worker re-index. The outbox row drives the same IndexClip worker
	// as the legacy SafeGoFunc but in a crash-safe tx. Folder rows are
	// excluded — folders are containers, not embeddable assets, and the
	// worker would otherwise have to detect-and-skip them.
	if !clip.IsFolder() {
		if err := s.dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
			return fmt.Errorf("dispatcher.EnqueueAndIndex: %w", err)
		}
	}
	return nil
}

// buildPipelineMetadata constructs the pipeline metadata JSON for the run.
func (s *Service) buildPipelineMetadata(input *RunInput, chunkDur, clipDur int, result *PipelineResult, clipTitles []string) PipelineMetadata {
	meta := PipelineMetadata{
		Title:       input.FolderName,
		Description: input.FolderName,
		Source: SourceInfo{
			URL:   "",
			Title: input.FolderName,
		},
		Pipeline: PipelineInfo{
			ChunkDuration: chunkDur,
			ClipDuration:  clipDur,
			NoAudio:       input.NoAudio,
			NoEffects:     input.NoEffects,
			NoTransitions: input.NoTransitions,
		},
		Tags:     []string{"stock", "clip"},
		Category: "stock",
		Chunks:   make([]ChunkMeta, 0, len(result.Chunks)),
	}
	if len(input.DirectURLs) > 0 {
		meta.Source.URL = input.DirectURLs[0]
	}
	if len(input.SearchQueries) > 0 {
		meta.Source.URL = strings.Join(input.SearchQueries, " | ")
	}
	if input.Metadata != nil {
		if input.Metadata.Title != "" {
			meta.Title = input.Metadata.Title
		}
		if input.Metadata.Description != "" {
			meta.Description = input.Metadata.Description
		}
		if input.Metadata.Category != "" {
			meta.Category = input.Metadata.Category
		}
		if input.Metadata.Author != "" {
			meta.Author = input.Metadata.Author
		}
		if len(input.Metadata.Tags) > 0 {
			meta.Tags = append(meta.Tags, input.Metadata.Tags...)
		}
		if len(input.Metadata.Extra) != 0 {
			meta.Extra = input.Metadata.Extra
		}
	}
	for _, ch := range result.Chunks {
		startClipIdx := int(ch.TimelineStart / float64(clipDur))
		endClipIdx := int(ch.TimelineEnd / float64(clipDur))
		if endClipIdx > len(clipTitles) {
			endClipIdx = len(clipTitles)
		}
		clips := make([]ClipInfo, 0, endClipIdx-startClipIdx)
		for ci := startClipIdx; ci < endClipIdx; ci++ {
			clips = append(clips, ClipInfo{
				Index: ci,
				Start: formatDuration(float64(ci * clipDur)),
				End:   formatDuration(float64((ci + 1) * clipDur)),
				Title: clipTitles[ci],
			})
		}
		meta.Chunks = append(meta.Chunks, ChunkMeta{
			Index:         ch.Index,
			TimelineStart: ch.TimelineStart,
			TimelineEnd:   ch.TimelineEnd,
			DriveLink:     ch.DriveLink,
			DownloadLink:  ch.DownloadLink,
			Clips:         clips,
		})
	}
	return meta
}
