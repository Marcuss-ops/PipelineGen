// Package assets — images_repository.go: canonical ImagesRepository surface.
//
// PR-IMAGES-REPO-SPLIT (July 2026): decomposed the original 738-LoC
// monolithic images_repository.go into 4 single-purpose files per
// AGENTS.md Pattern 5:
//
//   - images_repository.go           — slim orchestrator: struct +
//     constructor + DB() +
//     normalizeTags + scanImageAssetFromRow
//   - images_repository_crud.go      — CRUD operations: AddImage, GetByID,
//     GetByHash, Delete, Upsert*, Update*,
//     GetSubjectBySlugOrAlias, etc.
//   - images_repository_search.go    — search/list: ListImagesBySubject
//     (deprecated) + ListImages (FASE 6)
//   - images_repository_aggregate.go — aggregate: ListImagesByOrigin +
//     ListAll + limit constants
package imagesrepo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ImagesRepository is the canonical SQLite-backed repository for image assets.
type ImagesRepository struct {
	db *sql.DB
}

// NewImagesRepository constructs the canonical repository.
func NewImagesRepository(db *sql.DB) *ImagesRepository {
	return &ImagesRepository{db: db}
}

// DB returns the underlying database connection.
func (r *ImagesRepository) DB() *sql.DB {
	return r.db
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// normalizeTags converte una lista di tag in una stringa normalizzata per ricerca full-text.
func normalizeTags(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		// rimuovi accenti/base
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

// scanImageAssetFromRow is the canonical (godlike/06 SSOT) helper that
// scans a single image row into *asset.ImageAsset. Replaces the
// pre-B6 byte-equivalent duplication between scanImageAsset
// (*sql.Row-shaped) and scanImageAssetRows (Rows-shaped). Both old
// helpers are gone; this single typed-(structural-interface) helper
// covers every caller because both *sql.Row.Scan(...) and
// *sql.Rows.Scan(...) satisfy `interface{ Scan(dest ...any) error }`.
//
// FASE 1B reads origin and provider as first-class columns (added by
// migration 115), surfacing them on ImageAsset.Origin / .Provider for
// downstream ImageSearchResolver routing (FASE 6).
//
// Column projection MUST match the SELECT in:
//   - GetImageByHash, GetByID, GetByDriveFileID (Row path, images_repository_crud.go)
//   - ListImagesBySubject, ListAll (Rows path, images_repository_search.go / _aggregate.go)
//
// B6 SSOT refactor (PR-IMAGES-AI-VS-NORMAL-PLAN, July 2026). Property
// tests in images_repository_test.go assert byte-equivalence across
// *sql.Row and *sql.Rows paths.
func scanImageAssetFromRow(s interface {
	Scan(dest ...any) error
}) (*asset.ImageAsset, error) {
	var img asset.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name, origin, provider sql.NullString
	var url, driveLink sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := s.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID, &driveLink, &origin, &provider)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = canonicalImageSourceURL(url.String, driveLink.String, driveFileID.String)
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	img.Origin = asset.ImageOrigin(origin.String)
	img.Provider = asset.ImageProvider(provider.String)
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
