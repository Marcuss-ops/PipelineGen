package assets

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// ErrNotFound is re-exported from the domain package for backward compatibility.
var ErrNotFound = asset.ErrNotFound

// ErrAlreadyExists is re-exported from the domain package for backward compatibility.
var ErrAlreadyExists = asset.ErrAlreadyExists

// ErrInvalidID is re-exported from the domain package for backward compatibility.
var ErrInvalidID = asset.ErrInvalidID

// ErrSoftDeleted is re-exported from the domain package for backward compatibility.
var ErrSoftDeleted = asset.ErrSoftDeleted

// ── AssetStoreSQLite ────────────────────────────────────────────────

// AssetStoreSQLite is the SQLite-backed implementation of the Store interface.
// It also provides folder, location, processing, and version repositories.
type AssetStoreSQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetStoreSQLite creates a new AssetStoreSQLite with the given database and logger.
func NewAssetStoreSQLite(db *sql.DB, log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{db: db, log: log}
}

// ── Store Interface (assets-specific, not in domain) ────────────────

// Store represents the unified CRUD repository for assets with nested entities.
type Store interface {
	Get(ctx context.Context, id string) (*Details, error)
	List(ctx context.Context, filter Filter) ([]*Summary, error)
	Save(ctx context.Context, details *Details) error
	Delete(ctx context.Context, id string) error
}

// ── Service Class ───────────────────────────────────────────────────

type Service struct {
	store Store
	log   *zap.Logger
}

func NewService(store Store, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{store: store, log: log}
}

func (s *Service) Get(ctx context.Context, id string) (*Details, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, filter Filter) ([]*Summary, error) {
	return s.store.List(ctx, filter)
}

func (s *Service) Save(ctx context.Context, details *Details) error {
	return s.store.Save(ctx, details)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func (s *Service) Repository() Repository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.AssetRepository()
	}
	return nil
}

func (s *Service) LocationRepository() LocationRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.LocationRepository()
	}
	return nil
}

func (s *Service) ProcessingRepository() ProcessingRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.ProcessingRepository()
	}
	return nil
}

func (s *Service) VersionRepository() VersionRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.VersionRepository()
	}
	return nil
}



