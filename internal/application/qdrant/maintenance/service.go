// internal/application/qdrant/maintenance/service.go — Service struct +
// ports + NewService constructor + Run dispatcher.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): the application-layer
// Service is the canonical use-case orchestration surface for the
// `qdrant-maintenance` admin command. Per godlike/06 SSOT (one canonical
// owner per fact): the application layer owns the dispatch + per-mode
// string-formatting + JSON serialization + outbox-enqueue loop semantics;
// internal/infrastructure/qdrant/maintenance/ continues to own the wire
// adapters (dr_adapter.go + locator_cleaner.go + reaper.go) per
// PR-QDRANT-FINAL-DECISION. cmd/admin now imports ONLY this package
// (no internal/infrastructure/qdrant direct import — godlike/07
// minimum-blast-radius on the boundary).
//
// Ports (typed interfaces defined here per AGENTS.md Pattern 0):
//   - QdrantCleaner     (drive_link/local_path key stripping w/ report)
//   - OutboxDispatcher  (canonical EnqueueAndDelete path)
//   - QdrantClient      (GetAliasTarget for active collection resolve)
//
// The Service struct holds concrete adapters (QdrantScannerAdapter for
// the application-layer audit path, infrastructure/qdrant.NewClient
// instance for the repair-locators fast path, and the OutboxDispatcher
// port injected at composition time).
package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// QdrantCleaner is the godlike/06 SSOT port for the drive_link/local_path
// key stripping pipeline. Concrete: internal/infrastructure/qdrant.LocatorCleaner.
type QdrantCleaner interface {
	CleanLocators(ctx context.Context, apply bool) (*LocatorCleanupReport, error)
}

// OutboxDispatcher is the godlike/06 SSOT port for the canonical
// EnqueueAndDelete path. Concrete: application.outbox.Dispatcher (wrapped
// from internal/app.ComposeRoot.Outbox.Dispatcher at composition time).
type OutboxDispatcher interface {
	EnqueueAndDelete(ctx context.Context, assetID string) error
}

// qdrantClient is the internal/infrastructure/qdrant.Client structural
// surface that the Service needs (GetAliasTarget). The concrete adapter
// is injected at NewService time.
type qdrantClient interface {
	GetAliasTarget(ctx context.Context, alias string) (string, error)
}

// Service is the canonical qdrant-maintenance use-case orchestrator for
// the 3 mode set (audit / repair-locators / delete-invalid). Per
// godlike/06 SSOT, this struct + the NewService constructor are the SOLE
// owner of mode dispatch. cmd/admin imports ONLY this struct.
//
// Field access pattern per mode (godlike/07 honest-disclosure):
//
//   - Repair (repair-locators mode): reads s.cleaner (QdrantCleaner port).
//     The heavy-init fields (sqliteDB, root, scanner) remain nil — this
//     mode handler does NOT touch them.
//   - Audit: reads s.scanner + s.activeCol (via classifyForMaintenance).
//     The orchestrator-internal fields (client, sqliteDB) are NOT read by
//     this mode handler.
//   - Delete (delete-invalid mode): reads s.scanner + s.activeCol (via
//     classifyForMaintenance) + s.dispatcher (for EnqueueAndDelete).
//     The orchestrator-internal fields (client, sqliteDB) are NOT read by
//     this mode handler.
type Service struct {
	cfg *config.Config
	log *zap.Logger

	// Heavy-init fields (Audit + Delete modes only).
	sqliteDB  *sql.DB
	root      *app.ComposeRoot
	client    qdrantClient
	activeCol string
	scanner   *QdrantScannerAdapter

	// Ports typed-injected at composition time.
	cleaner    QdrantCleaner
	dispatcher OutboxDispatcher
}

// Deps is the canonical constructor-input envelope for NewService.
// godlike/06 SSOT: this struct is the canonical SOLE owner of the
// dependency-contract shape for the maintenance package.
type Deps struct {
	Cfg        *config.Config
	Log        *zap.Logger
	Cleaner    QdrantCleaner
	Dispatcher OutboxDispatcher
}

// NewService is the canonical fail-closed constructor for Service.
// Pre-conditions:
//   - cfg.Qdrant.Enabled = true (validated by caller; qdrant-maintenance
//     requires qdrant.enabled=true)
//   - cleaner is non-nil (Repair mode requires it)
//   - dispatcher is non-nil (Delete mode requires it)
//
// Lazy fields (sqliteDB, root, client, activeCol, scanner) are NOT
// populated at construction time — Service.initHeavy populates them
// only when Audit or Delete mode is requested. This avoids the heavy
// composition-root initialize cost when only Repair mode is in play
// (the fast path that doesn't require SQLite or app.InitComposition).
func NewService(d Deps) (*Service, error) {
	if d.Cfg == nil {
		return nil, errors.New("maintenance.NewService: cfg is nil")
	}
	if d.Log == nil {
		return nil, errors.New("maintenance.NewService: log is nil")
	}
	if !d.Cfg.Qdrant.Enabled {
		return nil, errors.New(
			"qdrant is disabled in config (qdrant.enabled=false); " +
				"qdrant-maintenance requires qdrant.enabled=true",
		)
	}
	if d.Cleaner == nil {
		return nil, errors.New("maintenance.NewService: cleaner is nil (composition root missing QdrantCleaner port)")
	}
	if d.Dispatcher == nil {
		return nil, errors.New("maintenance.NewService: dispatcher is nil (composition root missing OutboxDispatcher port)")
	}
	return &Service{
		cfg:        d.Cfg,
		log:        d.Log,
		cleaner:    d.Cleaner,
		dispatcher: d.Dispatcher,
	}, nil
}

// Mode is the canonical typed enum for the 3-mode set that the
// `qdrant-maintenance` admin command accepts (audit / repair-locators /
// delete-invalid). Per the canonical policy on origin/main.
//
// FASE 1.2 PR-GODOBJ-12 honest scope-lock: the user spec referenced a 4th
// "rebuild" mode that does NOT exist on origin/main. Per godlike/07
// no-fake-availability, the 4th mode is NOT implemented; the closure
// documents this in the wave-tracker notes (see architecture/current.yaml
// PR-GODOBJ-12 linked_issues field).
type Mode string

const (
	ModeAudit          Mode = "audit"
	ModeRepairLocators Mode = "repair-locators"
	ModeDeleteInvalid  Mode = "delete-invalid"
)

// IsValid returns true for canonical 3-mode set.
func (m Mode) IsValid() bool {
	switch m {
	case ModeAudit, ModeRepairLocators, ModeDeleteInvalid:
		return true
	}
	return false
}

// Run is the canonical main entry point for the Service. Per mode it
// populates the lazy fields (if needed) and dispatches to the typed
// per-mode handler.
func (s *Service) Run(ctx context.Context, mode Mode, opts RunOptions) error {
	switch mode {
	case ModeRepairLocators:
		return s.Repair(ctx, RepairOptions{JSON: opts.JSON})

	case ModeAudit:
		if err := s.initHeavy(ctx, opts.Limit); err != nil {
			return err
		}
		return s.Audit(ctx, AuditOptions{JSON: opts.JSON, Limit: opts.Limit})

	case ModeDeleteInvalid:
		if err := s.initHeavy(ctx, opts.Limit); err != nil {
			return err
		}
		return s.Delete(ctx, DeleteOptions{JSON: opts.JSON, Limit: opts.Limit})
	}
	return fmt.Errorf("maintenance.Run: unknown mode %q (valid: audit, repair-locators, delete-invalid)", mode)
}

// RunOptions is the typed-input envelope for Service.Run — combines the
// 3 per-mode option envelopes into one facade so cmd/admin can call a
// single method.
type RunOptions struct {
	JSON  bool
	Limit int
}

// initHeavy populates the lazy fields on the Service: opens the
// primary SQLite DB, initializes the production composition root, and
// constructs the QdrantClient + Scanner. Used by Audit + Delete modes;
// Repair mode invokes the Cleaner port directly without these fields.
//
// Deferred cleanups (sqliteDB.Close + rootCleanup) fire AFTER Run
// returns — the defers are scoped to Run, not the closure handler.
//
// godlike/07 minimum-blast-radius: the heavy-init path mirrors the
// pre-split cmd/admin runQdrantMaintenanceHeavy verbatim; no logic
// drift is introduced (the only change is where the per-mode handlers
// read the lazy fields: pre-split they read mctx.scan/mctx.activeCol/
// mctx.root directly; post-split they read s.scanner/s.activeCol/
// s.dispatcher via the typed ports).
func (s *Service) initHeavy(ctx context.Context, limit int) error {
	sqliteDB, err := storage.OpenSQLiteDB(s.cfg.Storage.PrimaryDBFullPath(), s.log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	root, _, rootCleanup, err := app.InitComposition(s.cfg, s.log)
	if err != nil {
		return fmt.Errorf("production composition root init failed: %w", err)
	}
	defer rootCleanup()

	client := qdrant.NewClient(&qdrant.Config{
		BaseURL: s.cfg.Qdrant.BaseURL,
		APIKey:  s.cfg.Qdrant.APIKey,
		Timeout: s.cfg.Qdrant.Timeout,
	}, s.log)
	schema := qdrant.DefaultV3Schema()
	active, err := client.GetAliasTarget(ctx, schema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	if active == "" {
		return fmt.Errorf("runtime alias %q has no target; run EnsureSchema first", schema.RuntimeAlias)
	}

	s.sqliteDB = sqliteDB.DB
	s.root = root
	s.client = client
	s.activeCol = active
	s.scanner = NewQdrantScannerAdapter(client, active, limit)
	return nil
}
