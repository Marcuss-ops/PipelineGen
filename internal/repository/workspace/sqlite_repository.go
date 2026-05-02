package workspace

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	workspace "velox/go-master/internal/core/workspace"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func generateID() string {
	return uuid.New().String()
}

func (r *SQLiteRepository) Create(ctx context.Context, ws *workspace.Workspace) error {
	if ws.ID == "" {
		ws.ID = generateID()
	}
	now := time.Now()
	if ws.CreatedAt.IsZero() {
		ws.CreatedAt = now
	}
	if ws.UpdatedAt.IsZero() {
		ws.UpdatedAt = now
	}

	query := `INSERT INTO workspaces (id, name, slug, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	_, err := r.db.ExecContext(ctx, query, ws.ID, ws.Name, ws.Slug, ws.CreatedAt.Format(time.RFC3339), ws.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) GetByID(ctx context.Context, id string) (*workspace.Workspace, error) {
	query := `SELECT id, name, slug, created_at, updated_at FROM workspaces WHERE id=?`
	row := r.db.QueryRowContext(ctx, query, id)
	ws := &workspace.Workspace{}
	var createdAt, updatedAt string
	err := row.Scan(&ws.ID, &ws.Name, &ws.Slug, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ws.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ws.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return ws, nil
}

func (r *SQLiteRepository) GetBySlug(ctx context.Context, slug string) (*workspace.Workspace, error) {
	query := `SELECT id, name, slug, created_at, updated_at FROM workspaces WHERE slug=?`
	row := r.db.QueryRowContext(ctx, query, slug)
	ws := &workspace.Workspace{}
	var createdAt, updatedAt string
	err := row.Scan(&ws.ID, &ws.Name, &ws.Slug, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ws.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ws.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return ws, nil
}

func (r *SQLiteRepository) Update(ctx context.Context, ws *workspace.Workspace) error {
	ws.UpdatedAt = time.Now()
	query := `UPDATE workspaces SET name=?, slug=?, updated_at=? WHERE id=?`
	_, err := r.db.ExecContext(ctx, query, ws.Name, ws.Slug, ws.UpdatedAt.Format(time.RFC3339), ws.ID)
	return err
}

func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id=?`, id)
	return err
}

func (r *SQLiteRepository) List(ctx context.Context) ([]*workspace.Workspace, error) {
	query := `SELECT id, name, slug, created_at, updated_at FROM workspaces ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workspaces []*workspace.Workspace
	for rows.Next() {
		ws := &workspace.Workspace{}
		var createdAt, updatedAt string
		err := rows.Scan(&ws.ID, &ws.Name, &ws.Slug, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		ws.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		ws.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		workspaces = append(workspaces, ws)
	}
	return workspaces, rows.Err()
}
