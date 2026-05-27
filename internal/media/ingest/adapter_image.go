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
	imagerepo "velox/go-master/internal/repository/images"
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
	_, err := a.repo.AddImage(ctx, asset)
	return err
}

func (a *imageStoreAdapter) Get(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	img, err := a.repo.GetImageByHash(ctx, stripKindPrefix(id))
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
		img, err := a.repo.GetImageByHash(ctx, query.FileHash)
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
