package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"go.uber.org/zap"
)

type Finalizer struct {
	registry      Registry
	driveVerifier DriveVerifier
	assetIndex    AssetIndexPort
	metadata      MetadataPort
	log           *zap.Logger
}

func NewFinalizer(registry Registry, driveVerifier DriveVerifier, log *zap.Logger) *Finalizer {
	return &Finalizer{
		registry:      registry,
		driveVerifier: driveVerifier,
		metadata:      defaultMetadata(),
		log:           log,
	}
}

func NewFinalizerWithAssetIndex(registry Registry, driveVerifier DriveVerifier, assetIndex AssetIndexPort, log *zap.Logger) *Finalizer {
	return &Finalizer{
		registry:      registry,
		driveVerifier: driveVerifier,
		assetIndex:    assetIndex,
		metadata:      defaultMetadata(),
		log:           log,
	}
}

// NewFinalizerWithPorts constructs a finalizer with explicit projection and
// metadata ports. The composition root uses this when concrete adapters are
// available; tests can inject deterministic fakes.
func NewFinalizerWithPorts(registry Registry, driveVerifier DriveVerifier, assetIndex AssetIndexPort, metadata MetadataPort, log *zap.Logger) *Finalizer {
	if metadata == nil {
		metadata = defaultMetadata()
	}
	return &Finalizer{registry: registry, driveVerifier: driveVerifier, assetIndex: assetIndex, metadata: metadata, log: log}
}

func (f *Finalizer) Finalize(ctx context.Context, rec *MediaRecord, opts FinalizeOptions) (*FinalizeResult, error) {
	if rec == nil {
		return nil, fmt.Errorf("finalize: media record is required")
	}
	if f == nil || f.registry == nil {
		return nil, fmt.Errorf("finalize: registry is unavailable")
	}
	if f.log == nil {
		f.log = zap.NewNop()
	}
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

	if rec.LegacyFileMD5 == "" && opts.RequireHash {
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
			// BLOC5.3 P0.6 no-fake-availability (I2-followup, June 2026):
			// the previous code logged the verify error and then fell
			// through to `result.DriveUploaded = exists` — a transport
			// failure on the Drive SDK (network blip, auth refresh race)
			// was silently indistinguishable from "verify said the file
			// is accessible" when the underlying verifier returned
			// (true, err). Tighten: on err, DriveUploaded is explicitly
			// false (we do NOT claim accessibility) and the error is
			// surfaced on result.Error so the API caller can distinguish
			// "verify failed" from "verify said file is not in Drive"
			// (both previously looked identical via the DriveUploaded
			// field alone). The overall operation can still complete
			// OK (registry write may succeed) — the verify error is
			// informational + visible, not a hard failure.
			f.log.Warn("drive verification error", zap.String("id", rec.ID), zap.Error(err))
			result.DriveUploaded = false
			result.Error = err.Error()
		} else {
			result.DriveUploaded = exists
		}
	}

	if rec.PublishStatus != "" {
		var metadata map[string]any
		if strings.TrimSpace(rec.Metadata) != "" {
			_ = json.Unmarshal([]byte(rec.Metadata), &metadata)
		}
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["delivery_status"] = string(rec.PublishStatus)
		if rec.Error != "" {
			metadata["delivery_error"] = rec.Error
		}
		if data, err := json.Marshal(metadata); err == nil {
			rec.Metadata = string(data)
		}
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
		assetRec := &AssetIndexRecord{
			AssetID:       rec.ID,
			AssetType:     rec.MediaType,
			Source:        rec.Source,
			SourceID:      rec.SourceID,
			GroupName:     rec.Group,
			Subfolder:     rec.Subfolder,
			LocalPath:     rec.LocalPath,
			DriveLink:     rec.DriveLink,
			DownloadLink:  rec.DownloadLink,
			LegacyFileMD5: rec.LegacyFileMD5,
			ContentHash:   rec.ContentHash,
			Status:        assetIndexStatus(rec.PublishStatus),
			Metadata:      rec.Metadata,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
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

func assetIndexStatus(status asset.AssetPublishStatus) string {
	switch status {
	case asset.AssetPublishPending, asset.AssetPublishPublishing:
		return "pending"
	case asset.AssetPublishFailed:
		return "failed"
	default:
		return "ready"
	}
}

func (f *Finalizer) writeMetadataJSON(rec *MediaRecord) {
	dir := filepath.Dir(rec.LocalPath)
	metaPath := filepath.Join(dir, "metadata.json")

	existingMeta := f.metadata.MetadataMapFromJSON(readFileAsString(metaPath))
	if rec.Metadata != "" {
		for k, v := range f.metadata.MetadataMapFromJSON(rec.Metadata) {
			existingMeta[k] = v
		}
	}

	filename := filepath.Base(rec.LocalPath)
	assets := existingAssetList(existingMeta)
	if filename != "" {
		assets = appendAssetFile(assets, filename)
	}

	subjects := textutil.UniqueStringsVar(append(existingStringSlice(existingMeta, "subjects"), rec.Group, rec.Category, rec.Source)...)
	tags := textutil.UniqueStringsVar(append(existingStringSlice(existingMeta, "tags"), rec.Tags...)...)
	categories := textutil.UniqueStringsVar(append(existingStringSlice(existingMeta, "categories"), rec.Category, rec.Group, rec.MediaType)...)
	style := existingStringSlice(existingMeta, "style")
	mood := existingStringSlice(existingMeta, "mood")
	searchText := firstString(existingMeta, "search_text", f.metadata.MergeMetadataSearchText(rec.Name, rec.Filename, rec.Source, rec.Category, rec.Group, rec.FolderPath, strings.Join(rec.Tags, " ")))
	semanticDesc := firstString(existingMeta, "semantic_description", rec.Name, rec.Filename, rec.Category, rec.Group)
	generator := firstString(existingMeta, "generator", rec.Source, rec.Category, rec.MediaType)
	assetType := firstString(existingMeta, "asset_type", f.metadata.AssetTypeForMediaType(rec.MediaType))
	if assetType == "" {
		assetType = f.metadata.AssetTypeForMediaType(rec.MediaType)
	}

	// Supersede-gate fix: content_hash MUST be in metadata_json so
	// SourceVersionFor() reads Tier 1 (highest priority) instead of
	// falling back to stale Tier 2 (file_hash from a previous ingest).
	contentHash := rec.ContentHash
	if contentHash == "" {
		contentHash = rec.LegacyFileMD5
	}

	metadata := f.metadata.BuildAssetMetadata(MetadataInput{
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
			"timestamp":       timeutil.FormatRFC3339(time.Now()),
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
			"legacy_file_md5": rec.LegacyFileMD5,
			"file_hash":       rec.LegacyFileMD5,
			"content_hash":    contentHash,
			"source_id":       rec.SourceID,
			"subfolder":       rec.Subfolder,
			"embedding_ready": rec.PHash != "" || rec.VisualEmbeddingJSON != "" || firstString(existingMeta, "embedding_status", "") == "ready",
		},
	}, existingMeta)

	// Canonical image metadata: origin and provider are derived from the
	// source/generator pair using the single domain classification
	// functions. This prevents the "origin=generated" drift for
	// retrieved images when enrichment falls back or is bypassed.
	if assetType == "image" {
		metadata["origin"] = string(asset.ClassifyImageOrigin(rec.Source, generator))
		metadata["provider"] = string(asset.ClassifyImageProvider(rec.Source, generator))
	}
	metadataJSON := f.metadata.MetadataMapToJSON(metadata)
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

func defaultConfidence(rec *MediaRecord) float64 {
	if rec.PHash != "" || rec.VisualEmbeddingJSON != "" {
		return 0.9
	}
	if strings.TrimSpace(rec.LegacyFileMD5) != "" {
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
