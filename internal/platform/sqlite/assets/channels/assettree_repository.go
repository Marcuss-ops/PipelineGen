package channels

import (
	"context"
	"database/sql"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
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

// AssetTreeRepository manages the asset tree nodes in the database.
type AssetTreeRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetTreeRepository creates a new AssetTreeRepository for asset trees.
func NewAssetTreeRepository(db *sql.DB, log *zap.Logger) (*AssetTreeRepository, error) {
	return &AssetTreeRepository{
		db:  db,
		log: log,
	}, nil
}

// UpsertNode inserts or updates an asset node.
func (r *AssetTreeRepository) UpsertNode(ctx context.Context, node *AssetNode) error {
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
		timeutil.FormatRFC3339(node.CreatedAt), timeutil.FormatRFC3339(node.UpdatedAt),
	)
	return err
}

// GetChildren returns the direct children of a given parent node within a source.
// If parentID is empty, it returns the root nodes for the source.
func (r *AssetTreeRepository) GetChildren(ctx context.Context, source, parentID string) ([]*AssetNode, error) {
	return r.GetChildrenPaged(ctx, source, parentID, 10000, 0)
}

// GetChildrenPaged returns the direct children of a given parent node within a source with pagination.
func (r *AssetTreeRepository) GetChildrenPaged(ctx context.Context, source, parentID string, limit, offset int) ([]*AssetNode, error) {
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
func (r *AssetTreeRepository) GetNode(ctx context.Context, id string) (*AssetNode, error) {
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
func (r *AssetTreeRepository) DeleteNode(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM asset_tree_nodes WHERE id = ?", id)
	return err
}

// DeleteByAssetID deletes a node by its source and original asset ID.
func (r *AssetTreeRepository) DeleteByAssetID(ctx context.Context, source, assetID string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM asset_tree_nodes WHERE source = ? AND asset_id = ?", source, assetID)
	return err
}

// FindByName returns the first node matching the given name within a source and root.
// If parentID is non-empty, it filters by parent_id as well.
func (r *AssetTreeRepository) FindByName(ctx context.Context, source, rootID, parentID, name string) (*AssetNode, error) {
	query := `
		SELECT id, source, asset_id, name, type, parent_id, root_id, path, depth, is_folder,
		       drive_file_id, drive_link, metadata, created_at, updated_at,
		       (SELECT COUNT(*) FROM asset_tree_nodes c WHERE c.parent_id = asset_tree_nodes.id) AS child_count
		FROM asset_tree_nodes
		WHERE source = ? AND root_id = ? AND name = ? AND parent_id = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, source, rootID, name, parentID)
	return r.scanNode(row)
}

// ListByRoot returns all nodes belonging to a given root within a source.
func (r *AssetTreeRepository) ListByRoot(ctx context.Context, source, rootID string) ([]*AssetNode, error) {
	query := `
		SELECT id, source, asset_id, name, type, parent_id, root_id, path, depth, is_folder,
		       drive_file_id, drive_link, metadata, created_at, updated_at,
		       (SELECT COUNT(*) FROM asset_tree_nodes c WHERE c.parent_id = asset_tree_nodes.id) AS child_count
		FROM asset_tree_nodes
		WHERE source = ? AND root_id = ?
		ORDER BY depth ASC, name ASC
	`
	rows, err := r.db.QueryContext(ctx, query, source, rootID)
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

func (r *AssetTreeRepository) scanNode(scanner interface{ Scan(dest ...any) error }) (*AssetNode, error) {
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

	if t := timeutil.ParseRFC3339(createdAt); !t.IsZero() {
		node.CreatedAt = t
	}
	if t := timeutil.ParseRFC3339(updatedAt); !t.IsZero() {
		node.UpdatedAt = t
	}

	return &node, nil
}
