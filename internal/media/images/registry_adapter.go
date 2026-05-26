package images

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/media/assetregistry"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/images"

	"go.uber.org/zap"
)

type registryAdapter struct {
	repo      *images.Repository
	imagesDir string
	log       *zap.Logger
}

func NewRegistryAdapter(repo *images.Repository, imagesDir string, log *zap.Logger) assetregistry.Registry {
	return &registryAdapter{repo: repo, imagesDir: imagesDir, log: log}
}

func (a *registryAdapter) UpsertMedia(ctx context.Context, rec *assetregistry.MediaRecord) error {
	if rec == nil {
		return nil
	}

	asset := &models.ImageAsset{
		Hash:        imageRecordHash(rec.ID, rec.FileHash),
		SubjectID:   firstNonEmpty(rec.Group, rec.SourceID, rec.Source),
		SourceURL:   firstNonEmpty(rec.ExternalURL, rec.DownloadLink),
		Description: rec.Name,
		DriveFileID: rec.DriveFileID,
		Status:      rec.Status,
		MetadataJSON: mergeImageMetadata(
			rec.Metadata,
			rec,
			a.relativePath(rec.LocalPath),
		),
		Tags:      append([]string(nil), rec.Tags...),
		CreatedAt: time.Now().UTC(),
	}

	if asset.Description == "" {
		asset.Description = filepath.Base(rec.Filename)
	}

	_, err := a.repo.AddImage(ctx, asset)
	return err
}

func (a *registryAdapter) GetMedia(ctx context.Context, id string) (*assetregistry.MediaRecord, error) {
	img, err := a.repo.GetImageByHash(ctx, imageRecordHash(id, ""))
	if err != nil || img == nil {
		return nil, err
	}
	return imageToMediaRecord(img, a.imagesDir), nil
}

func (a *registryAdapter) DeleteMedia(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, imageRecordHash(id, ""))
}

func (a *registryAdapter) GetAllWithDriveFileID(ctx context.Context) ([]*assetregistry.MediaRecord, error) {
	imagesList, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]*assetregistry.MediaRecord, 0, len(imagesList))
	for _, img := range imagesList {
		if strings.TrimSpace(img.DriveFileID) == "" {
			continue
		}
		records = append(records, imageToMediaRecord(img, a.imagesDir))
	}
	return records, nil
}

func (a *registryAdapter) FindByPHash(ctx context.Context, phash string) (string, error) {
	return "", nil
}

func imageRecordHash(id, fallback string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return strings.TrimSpace(fallback)
	}
	if idx := strings.IndexByte(id, ':'); idx >= 0 && idx+1 < len(id) {
		return id[idx+1:]
	}
	return id
}

func imageToMediaRecord(img *models.ImageAsset, imagesDir string) *assetregistry.MediaRecord {
	if img == nil {
		return nil
	}

	rec := &assetregistry.MediaRecord{
		ID:          imageRecordHash(img.Hash, img.Hash),
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

	return rec
}

func mergeImageMetadata(meta string, rec *assetregistry.MediaRecord, relPath string) string {
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
			payload["hash"] = rec.FileHash
		}
		if rec.Status != "" {
			payload["status"] = rec.Status
		}
	}
	if relPath != "" {
		payload["local_path"] = relPath
	}
	out, _ := json.Marshal(payload)
	return string(out)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *registryAdapter) relativePath(fullPath string) string {
	fullPath = strings.TrimSpace(fullPath)
	if fullPath == "" || a.imagesDir == "" {
		return fullPath
	}
	rel, err := filepath.Rel(a.imagesDir, fullPath)
	if err != nil {
		return fullPath
	}
	if strings.HasPrefix(rel, "..") {
		return fullPath
	}
	return rel
}

func imageFullPath(imagesDir, relPath string) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	if filepath.IsAbs(relPath) {
		return relPath
	}
	if imagesDir == "" {
		return relPath
	}
	return filepath.Join(imagesDir, relPath)
}
