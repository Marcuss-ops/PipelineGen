// Package app — artifact_folder_resolver.go: the Sender-side resolver that
// pins a RenderingGen overlay below its parent video's already-resolved Drive
// folder (/video/.../overlay/).
//
// The overlay manifest carries the parent video_id (media_assets.id); the
// broker resolves that video's folder_id and threads it into the overlay's
// VerifiedArtifact as ResolvedFolderID + RootFolderResolved, so the canonical
// ArtifactPublisherAdapter publishes the overlay below the video folder (via
// the drive_subpath=["overlay"] child) instead of a synthetic run folder.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// sqliteArtifactFolderResolver reads media_assets.folder_id for a parent video.
type sqliteArtifactFolderResolver struct {
	db *sql.DB
}

var _ finalization.ArtifactFolderResolver = (*sqliteArtifactFolderResolver)(nil)

func newSQLiteArtifactFolderResolver(db *sql.DB) *sqliteArtifactFolderResolver {
	return &sqliteArtifactFolderResolver{db: db}
}

// ResolveArtifactFolder returns the parent video's already-resolved Drive
// folder ID. An empty return means "not resolved" (the caller keeps the
// legacy destination path builder); a missing row is not an error.
func (r *sqliteArtifactFolderResolver) ResolveArtifactFolder(ctx context.Context, parentVideoID string) (string, error) {
	if r == nil || r.db == nil {
		return "", nil
	}
	var folderID string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(folder_id, '') FROM media_assets WHERE id = ?`, parentVideoID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve artifact folder %q: %w", parentVideoID, err)
	}
	return folderID, nil
}
