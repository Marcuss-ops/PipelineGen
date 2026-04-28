package clips

import (
	"context"
	"database/sql"
)

// Repository handles persistence for clips
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new clips repository
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// BeginTx starts a new transaction
func (r *Repository) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, opts)
}
