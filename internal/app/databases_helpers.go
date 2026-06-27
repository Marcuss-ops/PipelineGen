// Package app — DB setup helpers (PG-006, June 2026).
//
// Extracted from bootstrap.go so the bootstrap.go entry-point file remains
// strictly free of `internal/infrastructure/*` imports. The `databases`
// struct + `initDatabases` + `runAllMigrations` helpers are pure concrete
// wiring (storage.OpenSet, connection pooling + WAL/busy_timeout config,
// schema migration); only the composition root is allowed to keep the
// infra imports.
//
// Context: AGENTS.md §13 — `internal/infrastructure/**` is the only file
// tree allowed to import concrete SDK / driver code; `internal/app/**` is
// the composition root that wires the infra into the application domain
// via typed ports. PG-006 narrows the rule: bootstrap.go specifically
// must stay free of infra imports so the API tree's dependency on app
// remains strictly typed.
package app

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"

	"go.uber.org/zap"
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/infrastructure/database/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.set.Primary` / `dbs.set.Observability`.
type databases struct {
	set  *storage.DatabaseSet
	main *storage.SQLiteDB
	logs *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.set != nil {
		_ = d.set.Close()
	}
}

// initDatabases opens BOTH the primary + observability DBs via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/infrastructure/database/**`.
func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		PrimaryDBPath:       cfg.Storage.PrimaryDBFullPath(),
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}
	set, err := storage.OpenSet(setCfg, log)
	if err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}
	return &databases{
		set:  set,
		main: set.Primary,
		logs: set.Observability,
	}, nil
}

func runAllMigrations(dbs *databases, log *zap.Logger) error {
	return dbs.set.Migrate(log)
}
