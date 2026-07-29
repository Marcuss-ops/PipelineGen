package images

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// NewRegistryAdapter returns an artifacts.Registry backed by an
// ImagesRepository. The returned *artifacts.SimpleRegistry delegates
// every Registry method to a repo-specific callback.
func NewRegistryAdapter(repo *imagesrepo.ImagesRepository, imagesDir string, log *zap.Logger) artifacts.Registry {
	return &artifacts.SimpleRegistry{
		UpsertFn: func(ctx context.Context, rec *artifacts.MediaRecord) error {
			if rec == nil {
				return nil
			}
			img := &asset.ImageAsset{
				Hash:         imageRecordHash(rec.ID, rec.FileHash),
				SubjectID:    textutil.FirstNonEmpty(rec.Group, rec.SourceID, rec.Source),
				SourceURL:    textutil.FirstNonEmpty(rec.ExternalURL, rec.DownloadLink),
				Description:  rec.Name,
				DriveFileID:  rec.DriveFileID,
				Status:       rec.Status,
				MetadataJSON: mergeImageMetadata(rec.Metadata, rec, relativePath(imagesDir, rec.LocalPath)),
				Tags:         append([]string(nil), rec.Tags...),
				CreatedAt:    time.Now().UTC(),
			}
			if img.Description == "" {
				img.Description = filepath.Base(rec.Filename)
			}
			_, err := repo.AddImage(ctx, img)
			return err
		},
		GetFn: func(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
			img, err := repo.GetImageByHash(ctx, imageRecordHash(id, ""))
			if err != nil || img == nil {
				return nil, err
			}
			return imageToMediaRecord(img, imagesDir), nil
		},
		DeleteFn: func(ctx context.Context, id string) error {
			return repo.Delete(ctx, imageRecordHash(id, ""))
		},
		ListFn: func(ctx context.Context) ([]*artifacts.MediaRecord, error) {
			return artifacts.GetAllWithDriveFileID(ctx, repo.ListAll,
				func(img *asset.ImageAsset) (*artifacts.MediaRecord, bool) {
					if strings.TrimSpace(img.DriveFileID) == "" {
						return nil, false
					}
					return imageToMediaRecord(img, imagesDir), true
				})
		},
		PHashFn: artifacts.NoopFindByPHash,
	}
}

// ── Image-specific helpers ─────────────────────────────────────────────

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

func imageToMediaRecord(img *asset.ImageAsset, imagesDir string) *artifacts.MediaRecord {
	if img == nil {
		return nil
	}
	return &artifacts.MediaRecord{
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
		SourceID:    textutil.FirstNonEmpty(img.SourceURL, img.Hash),
	}
}

func mergeImageMetadata(meta string, rec *artifacts.MediaRecord, relPath string) string {
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

func relativePath(imagesDir, fullPath string) string {
	fullPath = strings.TrimSpace(fullPath)
	if fullPath == "" || imagesDir == "" {
		return fullPath
	}
	rel, err := filepath.Rel(imagesDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
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
