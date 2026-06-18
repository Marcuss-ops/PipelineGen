// Package assettags provides the read/write layer for the asset_tags table,
// which stores user-defined and auto-generated tags for media assets.
package assettags

import (
	"database/sql"
)

// Repository wraps SQL access to the asset_tags table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}
