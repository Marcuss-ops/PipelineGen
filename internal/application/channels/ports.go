// Package channels — typed ports for the CategoryChannel domain.
//
// PG-002 (June 2026): the API layer (internal/api/channels) consumes this
// interface; the concrete SQLite-backed repository in
// internal/infrastructure/database/sqlite/assets is wrapped by an adapter
// in internal/app/adapters_channels.go. This file creates the channels
// application package — there is no application logic in this slice yet
// (channels is pure CRUD surfaced to the API), but the port lives here
// per AGENTS.md Pattern 0 so any future orchestration/business rule
// finds the canonical home without re-shuffling.
package channels

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Repository is the typed port consumed by internal/api/channels.
// Every method mirrors the operations the API actually performs — zero
// extra surface area. New operations must be added here first, then
// implemented in the SQLite adapter in internal/app.
//
// Slice-shape convention: ListAll returns a pointer slice ([]*CategoryChannel)
// to match the concrete SQLite-backed repository; nil elements are filtered
// upstream. Value slices would require a defensive copy in the adapter
// and break the zero-extra-surface rule of Pattern 0.
type Repository interface {
	// ListAll returns every category↔channel association in the store.
	ListAll(ctx context.Context) ([]*asset.CategoryChannel, error)
	// ListCategories returns the distinct set of categories that have
	// at least one channel assigned.
	ListCategories(ctx context.Context) ([]string, error)
	// GetByID returns a single channel association by its primary key.
	// Returns a wrapped not-found error when the id is unknown.
	GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error)
	// Upsert creates or updates a channel association keyed by ID.
	Upsert(ctx context.Context, ch *asset.CategoryChannel) error
	// Delete removes a channel association by ID. Idempotent: deleting
	// a missing id returns nil (matches the existing infra behaviour).
	Delete(ctx context.Context, id string) error
}
