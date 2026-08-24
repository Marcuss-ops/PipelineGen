// Package assets — asset_rendition_repository.go
//
// SQLite concrete for the asset rendition repository.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AssetRenditionRepository is the SQLite-backed implementation of
// asset.RenditionRepository.
type AssetRenditionRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetRenditionRepository builds a SQLite-backed rendition repository.
func NewAssetRenditionRepository(db *sql.DB, log *zap.Logger) (*AssetRenditionRepository, error) {
	if db == nil {
		return nil, errors.New("asset_rendition_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetRenditionRepository{db: db, log: log}, nil
}

var _ asset.RenditionRepository = (*AssetRenditionRepository)(nil)

// Create persists a new AssetRendition and returns its ID.
func (r *AssetRenditionRepository) Create(ctx context.Context, rendition *asset.AssetRendition) (string, error) {
	if rendition == nil {
		return "", errors.New("asset_rendition_repository.Create: rendition is nil")
	}
	if rendition.AssetID == "" {
		return "", errors.New("asset_rendition_repository.Create: AssetID is required")
	}
	if rendition.Kind == "" {
		rendition.Kind = asset.RenditionKindMaster
	}
	if !isValidRenditionKind(rendition.Kind) {
		return "", fmt.Errorf("asset_rendition_repository.Create: invalid Kind %q", rendition.Kind)
	}

	id := uuid.New().String()
	now := time.Now()
	rendition.ID = id
	rendition.CreatedAt = now
	rendition.UpdatedAt = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO asset_renditions (
			id, asset_id, location_id, kind, container, codec, width, height,
			fps, bitrate, color_space, sha256, size_bytes, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, rendition.AssetID, rendition.LocationID, string(rendition.Kind),
		rendition.Container, rendition.Codec, rendition.Width, rendition.Height,
		rendition.FPS, rendition.Bitrate, rendition.ColorSpace, rendition.SHA256,
		rendition.SizeBytes, timeutil.FormatRFC3339(rendition.CreatedAt),
		timeutil.FormatRFC3339(rendition.UpdatedAt),
	)
	if err != nil {
		r.log.Error("asset_rendition_repository.Create failed",
			zap.String("asset_id", rendition.AssetID),
			zap.Error(err),
		)
		return "", fmt.Errorf("asset_rendition_repository.Create: %w", err)
	}
	return id, nil
}

// Get retrieves an AssetRendition by ID.
func (r *AssetRenditionRepository) Get(ctx context.Context, id string) (*asset.AssetRendition, error) {
	if id == "" {
		return nil, errors.New("asset_rendition_repository.Get: id is required")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, asset_id, location_id, kind, container, codec, width, height,
			fps, bitrate, color_space, sha256, size_bytes, created_at, updated_at
		 FROM asset_renditions WHERE id = ?`,
		id,
	)
	return scanAssetRendition(row)
}

// ListByAsset lists all renditions for a given asset.
func (r *AssetRenditionRepository) ListByAsset(ctx context.Context, assetID string) ([]*asset.AssetRendition, error) {
	if assetID == "" {
		return nil, errors.New("asset_rendition_repository.ListByAsset: assetID is required")
	}
	const query = `SELECT id, asset_id, location_id, kind, container, codec, width, height,
		fps, bitrate, color_space, sha256, size_bytes, created_at, updated_at
		FROM asset_renditions WHERE asset_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, assetID)
}

// ListByLocation lists all renditions for a given location.
func (r *AssetRenditionRepository) ListByLocation(ctx context.Context, locationID int64) ([]*asset.AssetRendition, error) {
	if locationID <= 0 {
		return nil, errors.New("asset_rendition_repository.ListByLocation: locationID must be positive")
	}
	const query = `SELECT id, asset_id, location_id, kind, container, codec, width, height,
		fps, bitrate, color_space, sha256, size_bytes, created_at, updated_at
		FROM asset_renditions WHERE location_id = ? ORDER BY created_at DESC`
	return r.scanRows(ctx, query, locationID)
}

func (r *AssetRenditionRepository) scanRows(ctx context.Context, query string, value any) ([]*asset.AssetRendition, error) {
	rows, err := r.db.QueryContext(ctx, query, value)
	if err != nil {
		return nil, fmt.Errorf("asset_rendition_repository.scanRows: %w", err)
	}
	defer rows.Close()

	var out []*asset.AssetRendition
	for rows.Next() {
		rend, err := scanAssetRendition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rend)
	}
	return out, rows.Err()
}

// Update modifies an existing AssetRendition.
func (r *AssetRenditionRepository) Update(ctx context.Context, rendition *asset.AssetRendition) error {
	if rendition == nil || rendition.ID == "" {
		return errors.New("asset_rendition_repository.Update: rendition and ID are required")
	}
	if !isValidRenditionKind(rendition.Kind) {
		return fmt.Errorf("asset_rendition_repository.Update: invalid Kind %q", rendition.Kind)
	}
	rendition.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE asset_renditions SET
			asset_id = ?, location_id = ?, kind = ?, container = ?, codec = ?,
			width = ?, height = ?, fps = ?, bitrate = ?, color_space = ?,
			sha256 = ?, size_bytes = ?, updated_at = ?
		 WHERE id = ?`,
		rendition.AssetID, rendition.LocationID, string(rendition.Kind),
		rendition.Container, rendition.Codec, rendition.Width, rendition.Height,
		rendition.FPS, rendition.Bitrate, rendition.ColorSpace, rendition.SHA256,
		rendition.SizeBytes, timeutil.FormatRFC3339(rendition.UpdatedAt), rendition.ID,
	)
	if err != nil {
		r.log.Error("asset_rendition_repository.Update failed",
			zap.String("id", rendition.ID),
			zap.Error(err),
		)
		return fmt.Errorf("asset_rendition_repository.Update: %w", err)
	}
	return nil
}

// Delete removes an AssetRendition by ID.
func (r *AssetRenditionRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("asset_rendition_repository.Delete: id is required")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_renditions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("asset_rendition_repository.Delete: %w", err)
	}
	return nil
}

func isValidRenditionKind(kind asset.RenditionKind) bool {
	switch kind {
	case asset.RenditionKindMaster, asset.RenditionKindMezzanine, asset.RenditionKindProxy,
		asset.RenditionKindThumbnail, asset.RenditionKindStoryboard, asset.RenditionKindAudio,
		asset.RenditionKindSubtitle:
		return true
	}
	return false
}

func scanAssetRendition(row interface{ Scan(dest ...any) error }) (*asset.AssetRendition, error) {
	var rend asset.AssetRendition
	var locationID sql.NullInt64
	var createdAt, updatedAt string

	err := row.Scan(
		&rend.ID, &rend.AssetID, &locationID, &rend.Kind, &rend.Container,
		&rend.Codec, &rend.Width, &rend.Height, &rend.FPS, &rend.Bitrate,
		&rend.ColorSpace, &rend.SHA256, &rend.SizeBytes, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scansAssetRendition: %w", err)
	}

	if locationID.Valid {
		rend.LocationID = &locationID.Int64
	}
	rend.CreatedAt = timeutil.ParseRFC3339(createdAt)
	rend.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &rend, nil
}
