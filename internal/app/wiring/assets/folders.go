package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ArtifactFolderResolver reads the canonical folder id for a parent video,
// falling back to the legacy drive_folder_id projection while older rows are
// still being migrated.
type ArtifactFolderResolver struct {
	db *sql.DB
}

// NewArtifactFolderResolver constructs the SQLite-backed finalization resolver.
func NewArtifactFolderResolver(db *sql.DB) *ArtifactFolderResolver {
	return &ArtifactFolderResolver{db: db}
}

// ResolveArtifactFolder implements finalization.ArtifactFolderResolver.
func (r *ArtifactFolderResolver) ResolveArtifactFolder(ctx context.Context, parentVideoID string) (string, error) {
	if r == nil || r.db == nil {
		return "", nil
	}
	var folderID string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(folder_id, ''), drive_folder_id, '') FROM media_assets WHERE id = ?`, parentVideoID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve artifact folder %q: %w", parentVideoID, err)
	}
	return folderID, nil
}

var _ finalization.ArtifactFolderResolver = (*ArtifactFolderResolver)(nil)
