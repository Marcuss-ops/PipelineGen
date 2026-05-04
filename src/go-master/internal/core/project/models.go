package project

import (
	"context"
	"time"
)

type Project struct {
	ID          string
	WorkspaceID string
	Name        string
	Slug        string
	Kind        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Repository interface {
	Create(ctx context.Context, p *Project) error
	GetByID(ctx context.Context, id string) (*Project, error)
	GetBySlug(ctx context.Context, workspaceID, slug string) (*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id string) error
	ListByWorkspace(ctx context.Context, workspaceID string) ([]*Project, error)
}
