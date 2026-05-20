package ingest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/core/assetop"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/media/assetregistry"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
	imagerepo "velox/go-master/internal/repository/images"
	vorepo "velox/go-master/internal/repository/voiceovers"
)

type imageStoreAdapter struct {
	repo      *imagerepo.Repository
	imagesDir string
}

func NewImageStoreAdapter(repo *imagerepo.Repository, imagesDir string) lifecycle.AssetRecordStore {
	return &imageStoreAdapter{repo: repo, imagesDir: imagesDir}
}

func (a *imageStoreAdapter) Upsert(ctx context.Context, rec *assetregistry.MediaRecord) error {
	asset := &models.ImageAsset{
		Hash:         stripKindPrefix(rec.ID),
		SubjectID:    firstNonEmpty(rec.Group, rec.SourceID, rec.Source),
		PathRel:      relImagePath(a.imagesDir, rec.LocalPath),
		SourceURL:    firstNonEmpty(rec.ExternalURL, rec.DownloadLink),
		Description:  rec.Name,
		DriveFileID:  rec.DriveFileID,
		Status:       rec.Status,
		MetadataJSON: mergeImageMetadataJSON(rec.Metadata, rec, relImagePath(a.imagesDir, rec.LocalPath)),
		Tags:         append([]string(nil), rec.Tags...),
		CreatedAt:    time.Now().UTC(),
	}
	if asset.Description == "" {
		asset.Description = rec.Filename
	}
	_, err := a.repo.AddImage(asset)
	return err
}

func (a *imageStoreAdapter) Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	img, err := a.repo.GetImageByHash(stripKindPrefix(id))
	if err != nil || img == nil {
		return nil, err
	}
	return imageAssetToMediaRecord(img, a.imagesDir), nil
}

func (a *imageStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.Get(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.FileHash != "" {
		img, err := a.repo.GetImageByHash(query.FileHash)
		if err != nil {
			return nil, err
		}
		if img != nil {
			return mediaRecordToAssetRecord(imageAssetToMediaRecord(img, a.imagesDir)), nil
		}
	}

	if query.DriveFileID != "" {
		img, err := a.repo.GetByDriveFileID(ctx, query.DriveFileID)
		if err != nil {
			return nil, err
		}
		if img != nil {
			return mediaRecordToAssetRecord(imageAssetToMediaRecord(img, a.imagesDir)), nil
		}
	}

	return nil, nil
}

func (a *imageStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	imagesList, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []*assetop.AssetRecord
	for _, img := range imagesList {
		if strings.TrimSpace(img.DriveFileID) == "" {
			continue
		}
		if source != "" && source != "image" {
			continue
		}
		out = append(out, mediaRecordToAssetRecord(imageAssetToMediaRecord(img, a.imagesDir)))
	}
	return out, nil
}

func (a *imageStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	rec.Status = "drive_missing"
	return a.Upsert(ctx, rec)
}

func (a *imageStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, stripKindPrefix(id))
}

type voiceoverStoreAdapter struct {
	repo *vorepo.Repository
}

func NewVoiceoverStoreAdapter(repo *vorepo.Repository) lifecycle.AssetRecordStore {
	return &voiceoverStoreAdapter{repo: repo}
}

func (a *voiceoverStoreAdapter) Upsert(ctx context.Context, rec *assetregistry.MediaRecord) error {
	return a.repo.Upsert(ctx, mediaRecordToVoiceover(rec))
}

func (a *voiceoverStoreAdapter) Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	vRec, err := a.repo.GetByID(ctx, id)
	if err != nil || vRec == nil {
		return nil, err
	}
	return voiceoverToMediaRecord(vRec), nil
}

func (a *voiceoverStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.Get(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.DriveFileID != "" {
		vRec, err := a.repo.GetByDriveFileID(ctx, query.DriveFileID)
		if err != nil {
			return nil, err
		}
		if vRec != nil {
			return mediaRecordToAssetRecord(voiceoverToMediaRecord(vRec)), nil
		}
	}

	if query.FileHash != "" {
		recs, err := a.repo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if strings.EqualFold(strings.TrimSpace(rec.FileHash), strings.TrimSpace(query.FileHash)) {
				return mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)), nil
			}
		}
	}

	if query.Filename != "" {
		recs, err := a.repo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if strings.EqualFold(strings.TrimSpace(rec.Filename), strings.TrimSpace(query.Filename)) {
				return mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)), nil
			}
		}
	}

	return nil, nil
}

func (a *voiceoverStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	recs, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []*assetop.AssetRecord
	for _, rec := range recs {
		if strings.TrimSpace(rec.DriveFileID) == "" {
			continue
		}
		out = append(out, mediaRecordToAssetRecord(voiceoverToMediaRecord(rec)))
	}
	return out, nil
}

func (a *voiceoverStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	rec.Status = "drive_missing"
	return a.Upsert(ctx, rec)
}

func (a *voiceoverStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

type clipStoreAdapter struct {
	repo *clips.Repository
}

func NewClipStoreAdapter(repo *clips.Repository) lifecycle.AssetRecordStore {
	return &clipStoreAdapter{repo: repo}
}

func (a *clipStoreAdapter) Upsert(ctx context.Context, rec *assetregistry.MediaRecord) error {
	clip := &models.MediaAsset{
		ID:                  rec.ID,
		Name:                rec.Name,
		Filename:            rec.Filename,
		FolderID:            rec.FolderID,
		FolderPath:          rec.FolderPath,
		Group:               rec.Group,
		MediaType:           rec.MediaType,
		DriveLink:           rec.DriveLink,
		DownloadLink:        rec.DownloadLink,
		DriveFileID:         rec.DriveFileID,
		Tags:                append([]string(nil), rec.Tags...),
		Source:              rec.Source,
		Category:            rec.Category,
		ExternalURL:         rec.ExternalURL,
		Duration:            rec.Duration,
		FileHash:            rec.FileHash,
		LocalPath:           rec.LocalPath,
		Status:              rec.Status,
		Error:               rec.Error,
		PHash:               rec.PHash,
		VisualEmbeddingJSON: rec.VisualEmbeddingJSON,
		UpdatedAt:           time.Now().UTC(),
	}
	clip.SetMetadataJSON(rec.Metadata)
	return a.repo.UpsertClip(ctx, clip)
}

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return clipAssetToMediaRecord(clip), nil
}

func (a *clipStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.Get(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.DriveFileID != "" {
		clip, err := a.repo.GetClipByDriveFileID(ctx, query.DriveFileID)
		if err != nil {
			return nil, err
		}
		if clip != nil {
			return mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)), nil
		}
	}

	if query.FileHash != "" {
		clipsList, err := a.repo.FindClipsByHash(ctx, query.FileHash)
		if err != nil {
			return nil, err
		}
		for _, clip := range clipsList {
			if clip != nil {
				return mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)), nil
			}
		}
	}

	return nil, nil
}

func (a *clipStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	clipsList, err := a.repo.GetAllWithDriveFileID(ctx)
	if err != nil {
		return nil, err
	}
	var out []*assetop.AssetRecord
	for _, clip := range clipsList {
		if source != "" && !strings.EqualFold(strings.TrimSpace(clip.Source), strings.TrimSpace(source)) {
			continue
		}
		out = append(out, mediaRecordToAssetRecord(clipAssetToMediaRecord(clip)))
	}
	return out, nil
}

func (a *clipStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	rec.Status = "drive_missing"
	return a.Upsert(ctx, rec)
}

func (a *clipStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.DeleteClip(ctx, id)
}

func mediaRecordToAssetRecord(rec *assetregistry.MediaRecord) *assetop.AssetRecord {
	if rec == nil {
		return nil
	}
	return &assetop.AssetRecord{
		ID:           rec.ID,
		Name:         rec.Name,
		Filename:     rec.Filename,
		Source:       rec.Source,
		MediaType:    rec.MediaType,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func clipAssetToMediaRecord(clip *models.MediaAsset) *assetregistry.MediaRecord {
	if clip == nil {
		return nil
	}
	return &assetregistry.MediaRecord{
		ID:                  clip.ID,
		Name:                clip.Name,
		Filename:            clip.Filename,
		Source:              clip.Source,
		Category:            clip.Category,
		MediaType:           clip.MediaType,
		ExternalURL:         clip.ExternalURL,
		FolderID:            clip.FolderID,
		FolderPath:          clip.FolderPath,
		Group:               clip.Group,
		LocalPath:           clip.LocalPath,
		DriveLink:           clip.DriveLink,
		DriveFileID:         clip.DriveFileID,
		DownloadLink:        clip.DownloadLink,
		Tags:                append([]string(nil), clip.Tags...),
		Duration:            clip.Duration,
		FileHash:            clip.FileHash,
		Status:              clip.Status,
		Error:               clip.Error,
		PHash:               clip.PHash,
		VisualEmbeddingJSON: clip.VisualEmbeddingJSON,
		Metadata:            clip.MetadataJSON(),
		SourceID:            firstNonEmpty(clip.ExternalURL, clip.Filename, clip.ID),
	}
}

func stripKindPrefix(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if idx := strings.IndexByte(id, ':'); idx >= 0 && idx+1 < len(id) {
		return id[idx+1:]
	}
	return id
}

func relImagePath(imagesDir, fullPath string) string {
	imagesDir = strings.TrimSpace(imagesDir)
	fullPath = strings.TrimSpace(fullPath)
	if imagesDir == "" || fullPath == "" {
		return fullPath
	}
	rel, err := filepath.Rel(imagesDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fullPath
	}
	return rel
}

func mergeImageMetadataJSON(meta string, rec *assetregistry.MediaRecord, relPath string) string {
	payload := map[string]any{}
	if strings.TrimSpace(meta) != "" && meta != "{}" {
		_ = json.Unmarshal([]byte(meta), &payload)
	}
	if rec != nil {
		if rec.Source != "" {
			payload["source"] = rec.Source
		}
		if rec.SourceID != "" {
			payload["source_id"] = rec.SourceID
		}
		if rec.Group != "" {
			payload["subject_id"] = rec.Group
		}
		if rec.Filename != "" {
			payload["filename"] = rec.Filename
		}
		if rec.DriveLink != "" {
			payload["drive_link"] = rec.DriveLink
		}
		if rec.DriveFileID != "" {
			payload["drive_file_id"] = rec.DriveFileID
		}
		if rec.DownloadLink != "" {
			payload["download_link"] = rec.DownloadLink
		}
		if rec.FileHash != "" {
			payload["hash"] = stripKindPrefix(rec.ID)
		}
		if rec.Status != "" {
			payload["status"] = rec.Status
		}
	}
	if relPath != "" {
		payload["local_path"] = relPath
	}
	if out, err := json.Marshal(payload); err == nil {
		return string(out)
	}
	return "{}"
}

func imageAssetToMediaRecord(img *models.ImageAsset, imagesDir string) *assetregistry.MediaRecord {
	if img == nil {
		return nil
	}

	return &assetregistry.MediaRecord{
		ID:          img.Hash,
		Name:        img.Description,
		Filename:    filepath.Base(img.PathRel),
		Source:      "image",
		Category:    img.SubjectID,
		Group:       img.SubjectID,
		MediaType:   "image",
		ExternalURL: img.SourceURL,
		LocalPath:   imageFullPath(imagesDir, img.PathRel),
		DriveFileID: img.DriveFileID,
		FileHash:    img.Hash,
		Status:      img.Status,
		Metadata:    img.MetadataJSON,
		Tags:        append([]string(nil), img.Tags...),
		SourceID:    firstNonEmpty(img.SourceURL, img.Hash),
	}
}

func voiceoverToMediaRecord(rec *vorepo.Record) *assetregistry.MediaRecord {
	if rec == nil {
		return nil
	}
	return &assetregistry.MediaRecord{
		ID:           rec.ID,
		Name:         rec.TextPreview,
		Filename:     rec.Filename,
		Source:       "voiceover",
		MediaType:    "audio",
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		LocalPath:    rec.LocalPath,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		SourceID:     rec.RequestID,
	}
}

func mediaRecordToVoiceover(rec *assetregistry.MediaRecord) *vorepo.Record {
	meta := map[string]any{}
	if strings.TrimSpace(rec.Metadata) != "" && rec.Metadata != "{}" {
		_ = json.Unmarshal([]byte(rec.Metadata), &meta)
	}

	requestID := getString(meta, "request_id")
	textHash := getString(meta, "text_hash")
	textPreview := getString(meta, "text_preview")
	language := getString(meta, "language")
	voice := getString(meta, "voice")
	cleanedPath := getString(meta, "cleaned_path")
	strategy := getString(meta, "strategy")

	now := time.Now().UTC()
	return &vorepo.Record{
		ID:           rec.ID,
		RequestID:    requestID,
		TextHash:     textHash,
		TextPreview:  textPreview,
		Language:     language,
		Voice:        voice,
		Filename:     rec.Filename,
		LocalPath:    rec.LocalPath,
		CleanedPath:  cleanedPath,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		Status:       rec.Status,
		Error:        rec.Error,
		Strategy:     strategy,
		Metadata:     rec.Metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func imageFullPath(imagesDir, relPath string) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	if filepath.IsAbs(relPath) {
		return relPath
	}
	if strings.TrimSpace(imagesDir) == "" {
		return relPath
	}
	return filepath.Join(imagesDir, relPath)
}
