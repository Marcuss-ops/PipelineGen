// Package assets — images_repository.go: canonical ImagesRepository surface.
//
// The repository owns image reads and typed detail-table persistence. The
// media_assets row itself is written only through the canonical asset writer.
package imagesrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ImagesRepository is the canonical SQLite-backed repository for image assets.
type ImagesRepository struct {
	db              *sql.DB
	canonicalCommit ImageCommitFunc
	canonicalMutate persistence.AssetMutator
}

// ImageCommitFunc is the composition-root supplied canonical write path for a
// new/updated image asset. It ultimately delegates to the one production
// CanonicalAssetWriter.
type ImageCommitFunc func(context.Context, *detail.ImageAsset) (int64, error)

// NewImagesRepository constructs the canonical repository.
func NewImagesRepository(db *sql.DB) *ImagesRepository {
	return &ImagesRepository{db: db}
}

func (r *ImagesRepository) SetCanonicalCommitter(commit ImageCommitFunc) {
	if r != nil {
		r.canonicalCommit = commit
	}
}

// SetCanonicalMutator supplies the mutation view of the same production
// canonical writer used by SetCanonicalCommitter. Repository methods fail
// closed when it is absent; no direct media_assets SQL fallback remains.
func (r *ImagesRepository) SetCanonicalMutator(mutator persistence.AssetMutator) {
	if r != nil {
		r.canonicalMutate = mutator
	}
}

// DB returns the underlying database connection. It is retained for the
// image-specific detail tables and read queries, not for media_assets writes.
func (r *ImagesRepository) DB() *sql.DB {
	return r.db
}

// normalizeTags converte una lista di tag in una stringa normalizzata per ricerca full-text.
func normalizeTags(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		low = strings.NewReplacer(
			"Ã ", "a", "Ã¨", "e", "Ã©", "e", "Ã¬", "i", "Ã²", "o", "Ã¹", "u",
		).Replace(low)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(low)
	}
	return b.String()
}

// scanImageAssetFromRow is the canonical helper that scans a single image row
// into *detail.ImageAsset. Both *sql.Row and *sql.Rows satisfy its Scan shape.
func scanImageAssetFromRow(s interface {
	Scan(dest ...any) error
}) (*detail.ImageAsset, error) {
	var img detail.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name, origin, provider sql.NullString
	var url, driveLink sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := s.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID, &driveLink, &origin, &provider)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = canonicalImageSourceURL(url.String, driveLink.String, driveFileID.String)
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	img.Origin = detail.ImageOrigin(origin.String)
	img.Provider = detail.ImageProvider(provider.String)
	if driveFileID.String != "" && !strings.Contains(img.SourceURL, "drive.google.com/") {
		img.SourceURL = fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID.String)
	}

	if createdAtStr.Valid {
		img.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	}

	if tagsJSON.Valid && tagsJSON.String != "" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &img.Tags)
	}

	if metaJSON.Valid && metaJSON.String != "" {
		img.MetadataJSON = metaJSON.String
		var metaMap map[string]any
		_ = json.Unmarshal([]byte(metaJSON.String), &metaMap)
		if v, ok := metaMap["subject_id"].(string); ok {
			img.SubjectID = v
		}
		if v, ok := metaMap["status"].(string); ok {
			img.Status = v
		}
	}

	return &img, nil
}

func canonicalImageSourceURL(url, driveLink, driveFileID string) string {
	if trimmed := strings.TrimSpace(url); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(driveLink); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(driveFileID); trimmed != "" {
		return fmt.Sprintf("https://drive.google.com/file/d/%s/view", trimmed)
	}
	return ""
}
