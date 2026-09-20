// internal/platform/qdrant/maintenance/service.go — Service struct +
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
	"io"

	"go.uber.org/zap"

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
// internal/capabilities/jobs/queue or any other concrete type. The composition root
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
	ResolveRuntimeCollection(ctx context.Context, alias string) (string, error)
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
type Deps struct {
	Cfg        *config.Config
	Log        *zap.Logger
	CliWriter  io.Writer // optional; NewService defaults to os.Stdout when nil
	Cleaner    QdrantCleaner
	Dispatcher DispatcherPort
	SQLiteDB   *sql.DB
}

// NewService is the canonical fail-closed constructor for Service.

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

// RunOptions is the typed-input envelope for Service.Run — combines the
// 3 per-mode option envelopes into one facade so cmd/admin can call a
// single method.
type RunOptions struct {
	JSON  bool
	Limit int
}

// ErrDispatcherNil is the godlike/07 fail-closed typed sentinel used
// when the outbox dispatcher cannot be wired during Service.initHeavy
// for Delete mode (per DL-006 fail-closed-at-boot contract).
var ErrDispatcherNil = errors.New("maintenance: outbox dispatcher is nil after composition root init (delete-invalid mode requires root.Outbox.Dispatcher; verify composition root initialization per DL-006 fail-closed-at-boot)")
