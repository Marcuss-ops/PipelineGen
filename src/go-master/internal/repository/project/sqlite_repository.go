package project

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	project "velox/go-master/internal/core/project"
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

func (r *SQLiteRepository) Create(ctx context.Context, p *project.Project) error {
	if p.ID == "" {
		p.ID = generateID()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}

	query := `INSERT INTO projects (id, workspace_id, name, slug, kind, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.ExecContext(ctx, query, p.ID, p.WorkspaceID, p.Name, p.Slug, p.Kind, p.Status, p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *SQLiteRepository) GetByID(ctx context.Context, id string) (*project.Project, error) {
	query := `SELECT id, workspace_id, name, slug, kind, status, created_at, updated_at FROM projects WHERE id=?`
	row := r.db.QueryRowContext(ctx, query, id)
	p := &project.Project{}
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Slug, &p.Kind, &p.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return p, nil
}

func (r *SQLiteRepository) GetBySlug(ctx context.Context, workspaceID, slug string) (*project.Project, error) {
	query := `SELECT id, workspace_id, name, slug, kind, status, created_at, updated_at FROM projects WHERE workspace_id=? AND slug=?`
	row := r.db.QueryRowContext(ctx, query, workspaceID, slug)
	p := &project.Project{}
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Slug, &p.Kind, &p.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return p, nil
}

func (r *SQLiteRepository) Update(ctx context.Context, p *project.Project) error {
	p.UpdatedAt = time.Now()
	query := `UPDATE projects SET workspace_id=?, name=?, slug=?, kind=?, status=?, updated_at=? WHERE id=?`
	_, err := r.db.ExecContext(ctx, query, p.WorkspaceID, p.Name, p.Slug, p.Kind, p.Status, p.UpdatedAt.Format(time.RFC3339), p.ID)
	return err
}

func (r *SQLiteRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	return err
}

func (r *SQLiteRepository) ListByWorkspace(ctx context.Context, workspaceID string) ([]*project.Project, error) {
	query := `SELECT id, workspace_id, name, slug, kind, status, created_at, updated_at FROM projects WHERE workspace_id=? ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []*project.Project
	for rows.Next() {
		p := &project.Project{}
		var createdAt, updatedAt string
		err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Slug, &p.Kind, &p.Status, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
