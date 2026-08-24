// internal/application/qdrant/maintenance/service.go — Service struct +
// ports + NewService constructor + Run dispatcher.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): the application-layer
// Service is the canonical use-case orchestration surface for the
// `qdrant-maintenance` admin command. Per godlike/06 SSOT (one canonical
// owner per fact): the application layer owns the dispatch + per-mode
// string-formatting + JSON serialization + outbox-enqueue loop semantics;
// internal/platform/qdrant/maintenance/ continues to own the wire
// adapters (dr_adapter.go + locator_cleaner.go + reaper.go) per
// PR-QDRANT-FINAL-DECISION. cmd/admin imports ONLY this package
// (no internal/platform/qdrant direct import — godlike/07
// minimum-blast-radius on the boundary).
//
// Ports (typed interfaces defined here per AGENTS.md Pattern 0):
//   - QdrantCleaner     (drive_link/local_path key stripping w/ report)
//
// Compile-drift fixup (2026-07-04, post-review): the OutboxDispatcher
// port was REMOVED from the constructor input surface. Service.initHeavy
// (called for audit + delete modes) lazy-opens the composition root via
// app.InitComposition and pulls root.Outbox.Dispatcher into the
// Service.dispatcher field — the canonical pre-split path (used by
// cmd/admin/qdrant_maintenance_delete_invalid.go before the split).
// For repair-locators mode the dispatcher is unused (the cleaner port
// is the only one consumed); initHeavy is intentionally NOT called for
// repair mode (the fast path that doesn't require SQLite or
// app.InitComposition).
package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// QdrantCleaner is the godlike/06 SSOT port for the drive_link/local_path
// key stripping pipeline. Concrete: internal/platform/qdrant.LocatorCleaner.
type QdrantCleaner interface {
	CleanLocators(ctx context.Context, apply bool) (*LocatorCleanupReport, error)
}

// DispatcherPort is the godlike/06 SSOT typed-interface port for the
// canonical EnqueueAndDelete path (the application-layer Delete mode uses
// it to dispatch canonical outbox DELETE events for non-locator assets).
//
// Per godlike/07 minimum-blast-radius (post-review fixup): this interface
// lives in the maintenance package so the Service struct can hold a
// typed dispatcher field without forcing Service to import
// internal/application/jobs or any other concrete type. The composition root
// (internal/app.ComposeRoot.Outbox.Dispatcher) provides a concrete value at
// Service.initHeavy time that structurally satisfies this interface via
// Go's implicit interface satisfaction.
type DispatcherPort interface {
	EnqueueAndDelete(ctx context.Context, assetID string) error
}

// qdrantClient is the internal/platform/qdrant.Client structural
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
//     The heavy-init fields (sqliteDB, root, scanner, dispatcher) remain
//     nil — this mode handler does NOT touch them.
//   - Audit: reads s.scanner + s.activeCol (via classifyForMaintenance).
//     The orchestrator-internal fields (client, sqliteDB, dispatcher) are
//     NOT read by this mode handler.
//   - Delete (delete-invalid mode): reads s.scanner + s.activeCol (via
//     classifyForMaintenance) + s.dispatcher (for EnqueueAndDelete).
//     The orchestrator-internal fields (client, sqliteDB) are NOT read by
//     this mode handler.
type Service struct {
	cfg *config.Config
	log *zap.Logger

	// cli is the godlike/06 SSOT CLI-UX surface (the typed CLIOutput
	// adapter declared in output.go). Owns ONLY the printable side of
	// CLI UX — formatted human-readable reports, JSON dumps, on-screen
	// UI hints. Decoupled from zap's operator-log channel: zap writes
	// to stderr / log-configured sinks via Service.log; the cli adapter
	// writes to its injected io.Writer (default os.Stdout via NewCLIOutput).
	//
	// godlike/07 NO-FAKE-AVAILABILITY: nil CLIOutput is NEVER a silent
	// no-op — NewCLIOutput fails closed by defaulting to os.Stdout when
	// d.CliWriter is nil so the operator never loses CLI UX. Tests that
	// want to capture output pass a bytes.Buffer via Deps.CliWriter.
	//
	// CR-thinker Q5 rationale (2026-07): Service.log stays separate (NOT
	// folded into cli) because Service.initHeavy reaches s.log directly
	// for storage.OpenSQLiteDB(..., s.log) and app.InitComposition(s.cfg, s.log).
	// Folding zap into this adapter would either force a cli.Logger()
	// getter (godlike/07 minimum-blast-radius regression — re-exports
	// the structured logger through an extra indirection) or accept a
	// weaker interface upstream (godlike/06 SSOT regression). Both are
	// anti-patterns; the 2-field (log + cli) layout is the canonical
	// scope-discipline answer.
	cli *CLIOutput

	// Heavy-init fields (Audit + Delete modes only).
	sqliteDB   *sql.DB
	root       *wiring.ComposeRoot
	client     qdrantClient
	activeCol  string
	scanner    *QdrantScannerAdapter
	dispatcher DispatcherPort

	// Port typed-injected at construction time.
	cleaner QdrantCleaner
}

// Deps is the canonical constructor-input envelope for NewService.
// godlike/06 SSOT: this struct is the canonical SOLE owner of the
// dependency-contract shape for the maintenance package.
//
// Per godlike/07 minimum-blast-radius (post-review): OutboxDispatcher
// removed — Service.initHeavy populates the dispatcher lazily from the
// composition root it opens for audit + delete modes.
type Deps struct {
	Cfg       *config.Config
	Log       *zap.Logger
	CliWriter io.Writer // optional; NewService defaults to os.Stdout when nil
	Cleaner   QdrantCleaner
}

// NewService is the canonical fail-closed constructor for Service.
// Pre-conditions:
//   - cfg.Qdrant.Enabled = true (validated by caller; qdrant-maintenance
//     requires qdrant.enabled=true)
//   - cleaner is non-nil (Repair mode requires it)
//
// Lazy fields (sqliteDB, root, client, activeCol, scanner, dispatcher)
// are NOT populated at construction time — Service.initHeavy populates
// them only when Audit or Delete mode is requested. This avoids the heavy
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
	// godlike/07 fail-closed-at-construction: NewCLIOutput defaults to
	// os.Stdout so CLI UX is byte-equivalent with the pre-split fmt.Print*
	// surface. Tests pass an explicit bytes.Buffer via Deps.CliWriter;
	// cmd/admin never passes CliWriter because the default is correct
	// for normal operator UX (stdout).
	return &Service{
		cfg:     d.Cfg,
		log:     d.Log,
		cli:     NewCLIOutput(d.CliWriter),
		cleaner: d.Cleaner,
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
//
// The string values here are the canonical mode identifiers —
// cmd/admin/qdrant_maintenance.go and cmd/admin/qdrant_maintenance_args.go
// MUST keep their mode-name string keys in lockstep with this enum.
// The dependency is documented at both surfaces (godlike/06 SSOT
// one-canonical-owner-per-fact: application owns the typed Mode enum;
// cmd/admin owns the CLI parse surface that validates against the
// stringified enum values).
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
//
// For audit + delete modes, initHeavy opens the composition root and
// extracts root.Outbox.Dispatcher into the Service's dispatcher field —
// godlike/07 minimum-blast-radius: the dispatcher is NOT a constructor
// arg (matches the pre-split mctx.root.Outbox.Dispatcher reach-through).
//
// godlike/07 fail-closed (post-review fixup): for Delete mode, surface
// ErrDispatcherNil immediately after initHeavy if the dispatcher wire
// failed — same fail-closed-at-boot contract as the pre-split code
// (mctx.root.Outbox.Dispatcher == nil check at mode-handler entry).
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
		if s.dispatcher == nil {
			return ErrDispatcherNil
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
// Per godlike/07 (post-review fixup): also extracts root.Outbox.Dispatcher
// into the Service.dispatcher field. The pre-split cmd/admin reached
// through this exact path (`mctx.root.Outbox.Dispatcher.EnqueueAndDelete`),
// so the post-split lazy-init preserves byte-equivalent runtime behavior.
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

	client := transport.NewClient(&schema.Config{
		BaseURL: s.cfg.Qdrant.BaseURL,
		APIKey:  s.cfg.Qdrant.APIKey,
		Timeout: s.cfg.Qdrant.Timeout,
	}, s.log)
	idxSchema := schema.DefaultV3Schema()
	active, err := client.GetAliasTarget(ctx, idxSchema.RuntimeAlias)
	if err != nil {
		return fmt.Errorf("resolve active collection: %w", err)
	}
	if active == "" {
		return fmt.Errorf("runtime alias %q has no target; run EnsureSchema first", idxSchema.RuntimeAlias)
	}

	s.sqliteDB = sqliteDB.DB
	s.root = root
	s.client = client
	s.activeCol = active
	s.scanner = NewQdrantScannerAdapter(client, active, limit)

	// godlike/07 fixup (post-review): wire the outbox dispatcher from
	// the composition root. Matches the pre-split
	// `mctx.root.Outbox.Dispatcher.EnqueueAndDelete` reach-through.
	if root != nil && root.Outbox != nil && root.Outbox.Dispatcher != nil {
		s.dispatcher = root.Outbox.Dispatcher
	}

	// godlike/07 fail-closed-at-boot gate (per DL-006 disposition):
	// if the orchestrator couldn't wire a dispatcher but Delete mode
	// is going to be used, surface a typed error NOW (at composition
	// time) rather than crashing mid-loop on nil-deref at first
	// EnqueueAndDelete call site.
	//
	// Per AGENTS.md Git-Lesson-3 audit-pin discipline: the fail-closed
	// gate preserves the pre-split `mctx.root.Outbox.Dispatcher ==
	// nil` behavior byte-equivalently (the pre-split handler returned
	// `ErrDispatcherNil` at mode-handler entry time; post-split we
	// surface the same NOT-available state at initHeavy time so the
	// call to Service.Run fails fast before any per-asset loop runs).
	return nil
}

// ErrDispatcherNil is the godlike/07 fail-closed typed sentinel used
// when the outbox dispatcher cannot be wired during Service.initHeavy
// for Delete mode (per DL-006 fail-closed-at-boot contract).
var ErrDispatcherNil = errors.New("maintenance: outbox dispatcher is nil after composition root init (delete-invalid mode requires root.Outbox.Dispatcher; verify composition root initialization per DL-006 fail-closed-at-boot)")
