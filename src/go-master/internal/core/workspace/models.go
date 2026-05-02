package workspace

import (
	"context"
	"time"
)

type Workspace struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Repository interface {
	Create(ctx context.Context, ws *Workspace) error
	GetByID(ctx context.Context, id string) (*Workspace, error)
	GetBySlug(ctx context.Context, slug string) (*Workspace, error)
	Update(ctx context.Context, ws *Workspace) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*Workspace, error)
}
