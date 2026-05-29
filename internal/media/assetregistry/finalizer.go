package assetregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/semantic"

	"go.uber.org/zap"
)

type Finalizer struct {
	registry      Registry
	driveVerifier DriveVerifier
	assetIndex    *assetindex.Service
	log           *zap.Logger
}

func NewFinalizer(registry Registry, driveVerifier DriveVerifier, log *zap.Logger) *Finalizer {
	return &Finalizer{
		registry:      registry,
		driveVerifier: driveVerifier,
		log:           log,
	}
}

func NewFinalizerWithAssetIndex(registry Registry, driveVerifier DriveVerifier, assetIndex *assetindex.Service, log *zap.Logger) *Finalizer {
	return &Finalizer{
		registry:      registry,
		driveVerifier: driveVerifier,
		assetIndex:    assetIndex,
		log:           log,
	}
}

func (f *Finalizer) Finalize(ctx context.Context, rec *MediaRecord, opts FinalizeOptions) (*FinalizeResult, error) {
	result := &FinalizeResult{
		Record: rec,
		Status: rec.Status,
	}

	if rec.LocalPath == "" && opts.RequireLocal {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing local path"
		f.log.Warn("finalize failed: missing local path", zap.String("id", rec.ID))
		return result, nil
	}

	if rec.LocalPath != "" {
		if _, err := os.Stat(rec.LocalPath); os.IsNotExist(err) {
			result.OK = false
			result.Status = "failed"
			result.Error = "local file does not exist"
			result.LocalExists = false
			f.log.Warn("finalize failed: local file missing", zap.String("id", rec.ID), zap.String("path", rec.LocalPath))
			return result, nil
		}
		result.LocalExists = true
	}

	if rec.FileHash == "" && opts.RequireHash {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing file hash"
		f.log.Warn("finalize failed: missing file hash", zap.String("id", rec.ID))
		return result, nil
	}

	if opts.RequireDrive && rec.DriveLink == "" {
		result.OK = false
		result.Status = "failed"
		result.Error = "missing drive link after upload"
		f.log.Warn("finalize failed: missing drive link", zap.String("id", rec.ID))
		return result, nil
	}

	if rec.DriveLink != "" && f.driveVerifier != nil {
		exists, err := f.driveVerifier.VerifyDriveLink(ctx, rec.DriveLink)
		if err != nil {
			f.log.Warn("drive verification error", zap.String("id", rec.ID), zap.Error(err))
		}
		result.DriveUploaded = exists
	}

	if err := f.registry.UpsertMedia(ctx, rec); err != nil {
		result.OK = false
		result.Status = "failed"
		result.Error = "db update failed: " + err.Error()
		f.log.Error("finalize failed: db update", zap.String("id", rec.ID), zap.Error(err))
		return result, nil
	}
	result.DBSaved = true

	// Write metadata.json in the same folder as the local file
	if rec.LocalPath != "" {
		f.writeMetadataJSON(rec)
	}

	// Write to asset_index if enabled
	if f.assetIndex != nil {
		assetRec := &assetindex.AssetRecord{
			AssetID:      rec.ID,
			AssetType:    rec.MediaType,
			Source:       rec.Source,
			SourceID:     rec.SourceID,
			GroupName:    rec.Group,
			Subfolder:    rec.Subfolder,
			LocalPath:    rec.LocalPath,
			DriveLink:    rec.DriveLink,
			DownloadLink: rec.DownloadLink,
			FileHash:     rec.FileHash,
			ContentHash:  rec.ContentHash,
			Status:       "ready",
			Metadata:     rec.Metadata,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		if err := f.assetIndex.Upsert(ctx, assetRec); err != nil {
			f.log.Warn("failed to write to asset_index", zap.String("id", rec.ID), zap.Error(err))
		}
	}

	if opts.VerifyDB {
		saved, err := f.registry.GetMedia(ctx, rec.ID)
		if err != nil {
			result.OK = false
			result.Status = "failed"
			result.Error = "db verify failed: " + err.Error()
			f.log.Error("finalize failed: db verify", zap.String("id", rec.ID), zap.Error(err))
			return result, nil
		}
		if saved == nil {
			result.OK = false
			result.Status = "failed"
			result.Error = "db verify failed: record not found after save"
			f.log.Error("finalize failed: record not found", zap.String("id", rec.ID))
			return result, nil
		}
		if opts.RequireDrive && saved.DriveLink == "" {
			result.OK = false
			result.Status = "partial"
			result.Error = "db saved without drive link"
			f.log.Warn("finalize partial: db saved without drive link", zap.String("id", rec.ID))
			return result, nil
		}
	}

	result.OK = true
	if result.Status == "" {
		result.Status = "processed"
	}

	f.log.Info("finalize complete",
		zap.String("id", rec.ID),
		zap.String("status", result.Status),
		zap.Bool("db_saved", result.DBSaved),
		zap.Bool("local_exists", result.LocalExists),
		zap.Bool("drive_uploaded", result.DriveUploaded))

	return result, nil
}

func (f *Finalizer) writeMetadataJSON(rec *MediaRecord) {
	dir := filepath.Dir(rec.LocalPath)
	metaPath := filepath.Join(dir, "metadata.json")

	existingMeta := semantic.MetadataMapFromJSON(readFileAsString(metaPath))
	if rec.Metadata != "" {
		for k, v := range semantic.MetadataMapFromJSON(rec.Metadata) {
			existingMeta[k] = v
		}
	}

	filename := filepath.Base(rec.LocalPath)
	assets := existingAssetList(existingMeta)
	if filename != "" {
		assets = appendAssetFile(assets, filename)
	}

	subjects := uniqueStrings(append(existingStringSlice(existingMeta, "subjects"), rec.Group, rec.Category, rec.Source)...)
	tags := uniqueStrings(append(existingStringSlice(existingMeta, "tags"), rec.Tags...)...)
	categories := uniqueStrings(append(existingStringSlice(existingMeta, "categories"), rec.Category, rec.Group, rec.MediaType)...)
	style := existingStringSlice(existingMeta, "style")
	mood := existingStringSlice(existingMeta, "mood")
	searchText := firstString(existingMeta, "search_text", semantic.MergeMetadataSearchText(rec.Name, rec.Filename, rec.Source, rec.Category, rec.Group, rec.FolderPath, strings.Join(rec.Tags, " ")))
	semanticDesc := firstString(existingMeta, "semantic_description", rec.Name, rec.Filename, rec.Category, rec.Group)
	generator := firstString(existingMeta, "generator", rec.Source, rec.Category, rec.MediaType)
	assetType := firstString(existingMeta, "asset_type", semantic.AssetTypeForMediaType(rec.MediaType))
	if assetType == "" {
		assetType = semantic.AssetTypeForMediaType(rec.MediaType)
	}

	metadata := semantic.BuildAssetMetadata(semantic.AssetSemanticInput{
		AssetID:             rec.ID,
		AssetType:           assetType,
		Source:              rec.Source,
		MediaType:           rec.MediaType,
		Generator:           generator,
		PromptOriginal:      firstString(existingMeta, "prompt_original", rec.Name, rec.Filename),
		SemanticDescription: semanticDesc,
		SearchText:          searchText,
		Subjects:            subjects,
		SubjectSlugs:        existingStringSlice(existingMeta, "subject_slugs"),
		Tags:                tags,
		Categories:          categories,
		Mood:                mood,
		Style:               style,
		Confidence:          floatOrDefault(existingMeta, "confidence", defaultConfidence(rec)),
		EmbeddingStatus:     firstString(existingMeta, "embedding_status", embeddingStatus(rec)),
		VisualEmbeddingJSON: firstString(existingMeta, "visual_embedding_json", rec.VisualEmbeddingJSON),
		PHash:               firstString(existingMeta, "phash", rec.PHash),
		VisualDimensions:    intOrDefault(existingMeta, "visual_dimensions", 0),
		Assets:              assets,
		Extra: map[string]any{
			"generation_id":   filepath.Base(dir),
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
			"source":          rec.Source,
			"media_type":      rec.MediaType,
			"filename":        filename,
			"folder_id":       rec.FolderID,
			"folder_path":     rec.FolderPath,
			"group_name":      rec.Group,
			"external_url":    rec.ExternalURL,
			"duration":        rec.Duration,
			"drive_link":      rec.DriveLink,
			"drive_file_id":   rec.DriveFileID,
			"download_link":   rec.DownloadLink,
			"file_hash":       rec.FileHash,
			"source_id":       rec.SourceID,
			"subfolder":       rec.Subfolder,
			"embedding_ready": rec.PHash != "" || rec.VisualEmbeddingJSON != "" || firstString(existingMeta, "embedding_status", "") == "ready",
		},
	}, existingMeta)
	metadataJSON := semantic.MetadataMapToJSON(metadata)
	rec.Metadata = metadataJSON

	if data, err := json.MarshalIndent(metadata, "", "  "); err == nil {
		_ = os.WriteFile(metaPath, data, 0644)
	}
}

func readFileAsString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func firstString(meta map[string]any, key string, fallbacks ...string) string {
	if meta != nil {
		if v, ok := meta[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	for _, fallback := range fallbacks {
		if strings.TrimSpace(fallback) != "" {
			return strings.TrimSpace(fallback)
		}
	}
	return ""
}

func existingStringSlice(meta map[string]any, key string) []string {
	if meta == nil {
		return nil
	}
	v, ok := meta[key]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out
	default:
		return nil
	}
}

func floatOrDefault(meta map[string]any, key string, fallback float64) float64 {
	if meta == nil {
		return fallback
	}
	if v, ok := meta[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case float32:
			return float64(n)
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case json.Number:
			if f, err := n.Float64(); err == nil {
				return f
			}
		}
	}
	return fallback
}

func intOrDefault(meta map[string]any, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	if v, ok := meta[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case float32:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return int(i)
			}
		}
	}
	return fallback
}

func existingAssetList(meta map[string]any) []map[string]any {
	if meta == nil {
		return nil
	}
	v, ok := meta["assets"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []map[string]any:
		return append([]map[string]any{}, arr...)
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func appendAssetFile(existing []map[string]any, filename string) []map[string]any {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return existing
	}
	for _, asset := range existing {
		if s, ok := asset["filename"].(string); ok && strings.TrimSpace(s) == filename {
			return existing
		}
		if s, ok := asset["path"].(string); ok && filepath.Base(strings.TrimSpace(s)) == filename {
			return existing
		}
	}
	return append(existing, map[string]any{"filename": filename})
}

func uniqueStrings(items ...string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func defaultConfidence(rec *MediaRecord) float64 {
	if rec.PHash != "" || rec.VisualEmbeddingJSON != "" {
		return 0.9
	}
	if strings.TrimSpace(rec.FileHash) != "" {
		return 0.7
	}
	return 0.5
}

func embeddingStatus(rec *MediaRecord) string {
	if rec.PHash != "" || rec.VisualEmbeddingJSON != "" {
		return "ready"
	}
	return "pending"
}
