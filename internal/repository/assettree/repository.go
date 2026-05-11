package assettree

import (
	"context"
	"database/sql"
	"time"

	"go.uber.org/zap"
)

// AssetNode represents a node in the asset tree hierarchy
type AssetNode struct {
	ID          string    `json:"id"`
	Source      string    `json:"source"`
	AssetID     string    `json:"asset_id"` // ID from the original table if applicable
	Name        string    `json:"name"`
	Type        string    `json:"type"` // folder, video, audio, image, file
	ParentID    string    `json:"parent_id"`
	RootID      string    `json:"root_id"`
	Path        string    `json:"path"`
	Depth       int       `json:"depth"`
	IsFolder    bool      `json:"is_folder"`
	DriveFileID string    `json:"drive_file_id"`
	DriveLink   string    `json:"drive_link"`
	Metadata    string    `json:"metadata"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ChildCount  int       `json:"child_count,omitempty"`
}

// Repository manages the asset tree nodes in the database.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewRepository creates a new repository for asset trees.
func NewRepository(db *sql.DB, log *zap.Logger) (*Repository, error) {
	return &Repository{
		db:  db,
		log: log,
	}, nil
}

// UpsertNode inserts or updates an asset node.
func (r *Repository) UpsertNode(ctx context.Context, node *AssetNode) error {
	now := time.Now().UTC()
	if node.CreatedAt.IsZero() {
		node.CreatedAt = now
	}
	node.UpdatedAt = now

	isFolderInt := 0
	if node.IsFolder {
		isFolderInt = 1
	}

	query := `
		INSERT INTO asset_tree_nodes (
			id, source, asset_id, name, type, parent_id, root_id, path, depth, is_folder,
			drive_file_id, drive_link, metadata, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			asset_id=excluded.asset_id,
			name=excluded.name,
			type=excluded.type,
			parent_id=excluded.parent_id,
			root_id=excluded.root_id,
			path=excluded.path,
			depth=excluded.depth,
			is_folder=excluded.is_folder,
			drive_file_id=excluded.drive_file_id,
			drive_link=excluded.drive_link,
			metadata=excluded.metadata,
			updated_at=excluded.updated_at
	`
	_, err := r.db.ExecContext(ctx, query,
		node.ID, node.Source, node.AssetID, node.Name, node.Type, node.ParentID, node.RootID, node.Path,
		node.Depth, isFolderInt, node.DriveFileID, node.DriveLink, node.Metadata,
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// GetChildren returns the direct children of a given parent node within a source.
// If parentID is empty, it returns the root nodes for the source.
func (r *Repository) GetChildren(ctx context.Context, source, parentID string) ([]*AssetNode, error) {
	return r.GetChildrenPaged(ctx, source, parentID, 10000, 0)
}

// GetChildrenPaged returns the direct children of a given parent node within a source with pagination.
func (r *Repository) GetChildrenPaged(ctx context.Context, source, parentID string, limit, offset int) ([]*AssetNode, error) {
	query := `SELECT id, source, asset_id, name, type, parent_id, root_id, path, depth, is_folder, drive_file_id, drive_link, metadata, created_at, updated_at,
		(SELECT COUNT(*) FROM asset_tree_nodes c WHERE c.parent_id = asset_tree_nodes.id) AS child_count
		FROM asset_tree_nodes
		WHERE source = ? AND parent_id = ?
		ORDER BY is_folder DESC, name ASC
		LIMIT ? OFFSET ?`
	rows, err := r.db.QueryContext(ctx, query, source, parentID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*AssetNode
	for rows.Next() {
		node, err := r.scanNode(rows)
		if err != nil {
			r.log.Error("failed to scan asset tree node", zap.Error(err))
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

// GetNode returns a single node by its ID.
func (r *Repository) GetNode(ctx context.Context, id string) (*AssetNode, error) {
	query := `
		SELECT id, source, asset_id, name, type, parent_id, root_id, path, depth, is_folder,
		       drive_file_id, drive_link, metadata, created_at, updated_at,
		       (SELECT COUNT(*) FROM asset_tree_nodes c WHERE c.parent_id = asset_tree_nodes.id) AS child_count
		FROM asset_tree_nodes
		WHERE id = ?
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return r.scanNode(row)
}

// DeleteNode deletes a node by its ID.
func (r *Repository) DeleteNode(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM asset_tree_nodes WHERE id = ?", id)
	return err
}

// DeleteByAssetID deletes a node by its source and original asset ID.
func (r *Repository) DeleteByAssetID(ctx context.Context, source, assetID string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM asset_tree_nodes WHERE source = ? AND asset_id = ?", source, assetID)
	return err
}

func (r *Repository) scanNode(scanner interface{ Scan(dest ...any) error }) (*AssetNode, error) {
	var node AssetNode
	var createdAt, updatedAt string

	err := scanner.Scan(
		&node.ID, &node.Source, &node.AssetID, &node.Name, &node.Type, &node.ParentID,
		&node.RootID, &node.Path, &node.Depth, &node.IsFolder, &node.DriveFileID,
		&node.DriveLink, &node.Metadata, &createdAt, &updatedAt, &node.ChildCount,
	)
	if err != nil {
		return nil, err
	}

	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		node.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		node.UpdatedAt = t
	}

	return &node, nil
}

