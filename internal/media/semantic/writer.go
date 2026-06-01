package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// WriteRequest is the unified input for creating metadata.json for any media asset.
// All media types (image, video, audio, voiceover, clip, stock) use this single struct.
// Media-type-specific fields go in Extensions.
type WriteRequest struct {
	// Core identity
	AssetID       string
	AssetType     string // image, video, audio, voiceover, clip, stock_clip, image_group
	MediaType     string // image, video, sound_effect, voiceover, clip
	Source        string // generated, artlist, stock, youtube, google_vids, voiceover
	Generator     string // flux-1-dev, google-flow, voiceover, artlist_scraper, stock-pipeline
	Style         string
	Prompt        string // original prompt or description
	SearchText    string // optional override; derived from Prompt if empty
	Confidence    float64

	// Asset-specific info
	Assets    []map[string]any // individual file info for batch/group metadata
	GroupID   string           // generation group ID (for batch metadata)

	// Type-specific extensions (merged into Extensions key in metadata.json)
	// Use ExtensionXxx helpers to build these.
	Extensions map[string]any

	// Output paths
	LocalPath string // local file path (metadata.json is written next to it)
	TempDir   string // temp dir for writing JSON before Drive upload
}

// WriteResult contains the result of writing metadata.
type WriteResult struct {
	Payload      *Payload
	MetadataJSON string
	LocalPath    string
}

// MetadataWriter handles writing metadata.json for ALL media types.
// Single entry point: Write(). Handles tagger invocation, fallback, local file write,
// and returns the Payload + JSON for caller to upload to Drive.
type MetadataWriter struct {
	scriptsDir  string
	tempDir     string
	ollamaURL   string
	ollamaModel string
	log         *zap.Logger
}

// NewMetadataWriter creates a unified metadata writer.
func NewMetadataWriter(scriptsDir, tempDir, ollamaURL, ollamaModel string, log *zap.Logger) *MetadataWriter {
	return &MetadataWriter{
		scriptsDir:  scriptsDir,
		tempDir:     tempDir,
		ollamaURL:   ollamaURL,
		ollamaModel: ollamaModel,
		log:         log,
	}
}

// GeneratePayload calls the tagger, applies overrides, and returns the Payload + JSON.
// Shared between Write() (which also writes to disk) and external callers.
// This is the SINGLE code path for semantic metadata generation across ALL media types.
func (w *MetadataWriter) GeneratePayload(ctx context.Context, req WriteRequest) (*Payload, string, error) {
	var payload *Payload
	var err error

	// Step 1: Call Python tagger
	if w.scriptsDir != "" {
		payload, err = Tagger(ctx, w.scriptsDir, req.Prompt, req.Style, req.MediaType, req.Generator, w.ollamaURL, w.ollamaModel)
	}

	// Step 2: Fallback on error
	if err != nil || payload == nil {
		if err != nil {
			w.log.Debug("semantic tagger failed, using fallback", zap.Error(err), zap.String("media_type", req.MediaType))
		}
		payload = NewFallbackPayload(req.MediaType, req.Prompt, req.Style, req.Generator)
	}

	// Step 3: Apply overrides from request
	if req.AssetID != "" {
		payload.AssetID = req.AssetID
	}
	if req.AssetType != "" {
		payload.AssetType = req.AssetType
	}
	if req.Source != "" {
		payload.Source = req.Source
	}
	if req.SearchText != "" {
		payload.SearchText = req.SearchText
	}
	if req.Confidence > 0 {
		payload.Confidence = req.Confidence
	}

	// Step 4: Apply type-specific extensions
	if req.Extensions != nil {
		if payload.Extensions == nil {
			payload.Extensions = make(map[string]any)
		}
		for k, v := range req.Extensions {
			if _, exists := payload.Extensions[k]; !exists {
				payload.Extensions[k] = v
			}
		}
	}

	// Step 5: Add asset info to payload
	if len(req.Assets) > 0 {
		if payload.Assets == nil {
			payload.Assets = make([]map[string]any, 0, len(req.Assets))
		}
		for _, a := range req.Assets {
			if a != nil {
				payload.Assets = append(payload.Assets, a)
			}
		}
	}

	// Step 6: Set timestamps
	if payload.CreatedAt == "" {
		payload.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Step 7: Marshal to JSON
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("marshal metadata: %w", err)
	}

	return payload, string(data), nil
}

// Write is the SINGLE entry point for creating metadata.json for any media asset.
// Uses GeneratePayload() internally to avoid duplicating the tagger+override logic.
func (w *MetadataWriter) Write(ctx context.Context, req WriteRequest) (*WriteResult, error) {
	payload, metadataJSON, err := w.GeneratePayload(ctx, req)
	if err != nil {
		return nil, err
	}

	// Write metadata.json to local disk
	metaPath := w.metadataPath(req)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		w.log.Warn("failed to create metadata dir", zap.String("path", filepath.Dir(metaPath)), zap.Error(err))
	}
	if err := os.WriteFile(metaPath, []byte(metadataJSON), 0644); err != nil {
		w.log.Warn("failed to write metadata.json", zap.String("path", metaPath), zap.Error(err))
	}

	result := &WriteResult{
		Payload:      payload,
		MetadataJSON: metadataJSON,
		LocalPath:    metaPath,
	}

	w.log.Info("metadata.json written",
		zap.String("asset_id", payload.AssetID),
		zap.String("asset_type", payload.AssetType),
		zap.String("media_type", payload.MediaType),
		zap.String("semantic_tier", payload.SemanticTier),
		zap.Float64("confidence", payload.Confidence),
		zap.Int("tags", len(payload.Tags)),
		zap.Int("extensions", len(payload.Extensions)),
	)

	return result, nil
}

// metadataPath determines where to write metadata.json.
// If LocalPath is set, writes next to the asset file.
// Otherwise writes in TempDir.
func (w *MetadataWriter) metadataPath(req WriteRequest) string {
	if req.LocalPath != "" {
		dir := filepath.Dir(req.LocalPath)
		return filepath.Join(dir, "metadata.json")
	}
	dir := req.TempDir
	if dir == "" {
		dir = w.tempDir
	}
	if dir == "" {
		dir = os.TempDir()
	}
	name := "metadata"
	if req.GroupID != "" {
		name = "metadata_" + req.GroupID
	}
	if req.AssetID != "" {
		name = "metadata_" + req.AssetID
	}
	return filepath.Join(dir, name+".json")
}

// ExtensionBuilders — helpers for building type-specific extension maps.

// BuildImageExtension creates extensions for image assets.
func BuildImageExtension(width, height int, visualEmbedding, phash string, visualDimensions int) map[string]any {
	ext := map[string]any{}
	if width > 0 {
		ext["width"] = width
	}
	if height > 0 {
		ext["height"] = height
	}
	if visualEmbedding != "" {
		ext["visual_embedding_json"] = visualEmbedding
	}
	if phash != "" {
		ext["phash"] = phash
	}
	if visualDimensions > 0 {
		ext["visual_dimensions"] = visualDimensions
	}
	return ext
}

// BuildVideoExtension creates extensions for video assets.
func BuildVideoExtension(durationSec int, fps int, codec string, hasAudio bool) map[string]any {
	ext := map[string]any{}
	if durationSec > 0 {
		ext["duration_sec"] = durationSec
	}
	if fps > 0 {
		ext["fps"] = fps
	}
	if codec != "" {
		ext["codec"] = codec
	}
	ext["has_audio"] = hasAudio
	return ext
}

// BuildAudioExtension creates extensions for audio/sound effect assets.
func BuildAudioExtension(durationSec int, sampleRate, channels int, isSFX bool, parentVideoID string) map[string]any {
	ext := map[string]any{}
	if durationSec > 0 {
		ext["duration_sec"] = durationSec
	}
	if sampleRate > 0 {
		ext["sample_rate"] = sampleRate
	}
	if channels > 0 {
		ext["channels"] = channels
	}
	ext["is_sfx"] = isSFX
	if parentVideoID != "" {
		ext["parent_video_id"] = parentVideoID
	}
	return ext
}

// BuildVoiceoverExtension creates extensions for voiceover assets.
func BuildVoiceoverExtension(language string, durationSec int, voiceModel, originalText, audioFormat string) map[string]any {
	ext := map[string]any{}
	if language != "" {
		ext["language"] = language
	}
	if durationSec > 0 {
		ext["duration_sec"] = durationSec
	}
	if voiceModel != "" {
		ext["voice_model"] = voiceModel
	}
	if originalText != "" {
		ext["original_text"] = originalText
	}
	if audioFormat != "" {
		ext["audio_format"] = audioFormat
	}
	return ext
}

// BuildClipExtension creates extensions for clip/stock/artlist assets.
func BuildClipExtension(durationSec int, sceneType string, qualityScore float64, usableFor, avoidFor []string, folderPath, externalURL string) map[string]any {
	ext := map[string]any{}
	if durationSec > 0 {
		ext["duration_sec"] = durationSec
	}
	if sceneType != "" {
		ext["scene_type"] = sceneType
	}
	if qualityScore > 0 {
		ext["quality_score"] = qualityScore
	}
	if len(usableFor) > 0 {
		ext["usable_for"] = usableFor
	}
	if len(avoidFor) > 0 {
		ext["avoid_for"] = avoidFor
	}
	if folderPath != "" {
		ext["folder_path"] = folderPath
	}
	if externalURL != "" {
		ext["external_url"] = externalURL
	}
	return ext
}

// UploadMetadataJSON uploads a metadata.json file to Drive via the provided uploader.
// uploadFn should be a function that takes (ctx, localPath, folderID, fileName) and returns (fileID, webLink, error).
func UploadMetadataJSON(ctx context.Context, localPath, folderID string, uploadFn func(ctx context.Context, localPath, folderID, fileName string) (string, string, error)) (string, string, error) {
	if uploadFn == nil {
		return "", "", fmt.Errorf("upload function not provided")
	}
	if localPath == "" {
		return "", "", fmt.Errorf("local path empty")
	}
	return uploadFn(ctx, localPath, folderID, "metadata.json")
}
