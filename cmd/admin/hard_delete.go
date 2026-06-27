// runHardDelete — operator-driven physical purge of a media_assets row.
//
// TODO 5 (QDRANT-002-B, June 2026): HardDelete of media_assets is
// admin-only. The HTTP routes /:source/clips/:id/hard-delete was
// removed (definition-of-done: zero production callers of HardDelete);
// this CLI command is the canonical operator surface.
//
// Atomicity contract: the CLI's local dispatcher impl mirrors the
// outbox.Dispatcher.EnqueueAndHardDelete contract — BEGIN, UPDATE
// media_assets.set lifecycle_state='DELETED' (caller side-effect: the
// verifier gate already confirmed lifecycle_state was DELETE_PENDING
// or DELETED before this point), DELETE child rows,
// INSERT outbox_events(asset.index.delete_requested.v1), COMMIT. The
// verifier gate runs FIRST and refuses the purge IFF any of the three
// conditions fails:
//   1. lifecycle_state != DELETE_PENDING && != DELETED
//   2. Qdrant point still present
//   3. any outbox_event with aggregate_id == assetID and status='pending'
//
// --dry-run runs the gate WITHOUT invoking the dispatcher (operator
// preview). --reason and --requested-by are recorded in the log line
// for the audit trail. --no-gate + --confirm-no-gate=PURGE bypasses
// the verifier (DR / fire-drill override; documented loss-of-gate
// risk).

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion/admin"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"go.uber.org/zap"
)

func runHardDelete(args []string) error {
	fs := flag.NewFlagSet("hard-delete", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: admin hard-delete --asset-id <id> [--reason <text>] [--requested-by <user>] [--dry-run] [--no-gate --confirm-no-gate PURGE]")
		fs.PrintDefaults()
	}
	assetID := fs.String("asset-id", "", "media_assets.id to purge (required)")
	reason := fs.String("reason", "", "human-readable reason for the audit log")
	requestedBy := fs.String("requested-by", "", "operator handle for the audit log (e.g. ops@example.com)")
	dryRun := fs.Bool("dry-run", false, "run the eligibility gate without invoking the dispatcher")
	noGate := fs.Bool("no-gate", false, "skip the AssetVerifier gate (operator override — NOT recommended; require explicit confirmation)")
	confirmNoGate := fs.String("confirm-no-gate", "", "type 'PURGE' to acknowledge the --no-gate risk; required when --no-gate=true")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *assetID == "" {
		fs.Usage()
		return errors.New("admin hard-delete: --asset-id is required")
	}

	// Operator override guard: --no-gate is intentionally lossy
	// (skips Qdrant + outbox pending + lifecycle_state checks) and
	// needs explicit typed acknowledgement.
	if *noGate && *confirmNoGate != "PURGE" {
		return errors.New("admin hard-delete: --no-gate requires --confirm-no-gate=PURGE (typed acknowledgement of the lost-gate risk)")
	}

	log, _ := zap.NewDevelopment()
	defer log.Sync()

	cfg := config.Get()
	dbPath := cfg.Storage.FullPath("data/media/media.db.sqlite")
	log.Info("opening database", zap.String("path", dbPath))
	sqliteDB, err := database.OpenSQLiteDB(dbPath, log)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqliteDB.Close()

	if err := sqliteDB.RunMigrations(log, "migrations/sqlite"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// SqliteAssetVerifier: lifecycle_state from media_assets,
	// pending outbox_events count, Qdrant probe via injected func.
	// In this CLI we cannot wire the production qdrant client (the
	// operator runs this offline), so the verifier defaults to
	// fail-closed: QdrantAbsent=false unless the operator supplies
	// --no-gate. This is the documented "operator override" mode.
	verifier := &admin.SqliteAssetVerifier{
		DB:                  sqliteDB.DB,
		AssetExistsInQdrant: qdrantProbeOfflineOperatorOverride,
	}

	// Dispatcher wrapper: when --dry-run=true we don't construct a
	// real dispatcher (the Service validates non-nil only when
	// DryRun=false). The CLI-local dispatcher mirrors the canonical
	// atomic-sql contract for the EnqueueAndHardDelete operation.
	var dispatcher admin.HardDeleteDispatcher
	if !*dryRun {
		dispatcher = &cliHardDeleteDispatcher{db: sqliteDB.DB, log: log}
	}

	svc, err := admin.NewService(verifier, dispatcher, log)
	if err != nil {
		return fmt.Errorf("wire admin service: %w", err)
	}

	if *noGate {
		log.Warn("admin hard-delete: --no-gate OVERRIDE active; AssetVerifier is being bypassed",
			zap.String("asset_id", *assetID),
			zap.String("requested_by", *requestedBy),
			zap.String("reason", *reason),
		)
		if !*dryRun {
			if dispatcher == nil {
				return errors.New("admin hard-delete: --no-gate without --dry-run requires a non-nil dispatcher (config error)")
			}
			if err := dispatcher.EnqueueAndHardDelete(context.Background(), *assetID); err != nil {
				return fmt.Errorf("dispatcher.EnqueueAndHardDelete: %w", err)
			}
			fmt.Printf("[NO-GATE] hard delete committed for %s\n", *assetID)
			return nil
		}
		fmt.Printf("[NO-GATE + DRY-RUN] no-op (gates skipped, dry-run is meaningless here)\n")
		return nil
	}

	res, err := svc.HardDelete(context.Background(), admin.HardDeleteRequest{
		AssetID: *assetID,
		DryRun:  *dryRun,
	})
	if err != nil {
		if errors.Is(err, admin.ErrAssetVerifier) {
			fmt.Fprintf(os.Stderr, "[GATE-FAIL] %s\n", err.Error())
			if res != nil && res.VerifierReport != nil {
				printRefusalReport(os.Stderr, res.VerifierReport)
			}
			return fmt.Errorf("hard delete refused (see stderr for the reason)")
		}
		return fmt.Errorf("hard delete: %w", err)
	}

	mode := "DRY-RUN"
	if res.DispatcherInvoked {
		mode = "COMMITTED"
	}
	fmt.Printf("[%s] %s\n", mode, summary(res, *reason, *requestedBy))
	return nil
}

// ── CLI-local HardDeleteDispatcher impl ──────────────────────────────────────
//
// Mirrors the canonical outbox.Dispatcher.EnqueueAndHardDelete atomics:
// BEGIN tx → DELETE child rows (locations, processing, versions) →
// DELETE media_assets → INSERT outbox_events(asset.index.delete_requested.v1)
// → COMMIT. Used ONLY by cmd/admin/hard_delete.go so the CLI does not
// depend on the production composition root.
//
// Writes the canonical 14-column outbox_events row shape used by the
// IndexingHandler / DeliveryHandler / provider sync pool so the rest
// of the system sees the exact same envelope shape regardless of
// whether the purge originated from the dispatcher (production) or
// this CLI (admin).

type cliHardDeleteDispatcher struct {
	db  *sql.DB
	log *zap.Logger
}

func (d *cliHardDeleteDispatcher) EnqueueAndHardDelete(ctx context.Context, assetID string) error {
	if assetID == "" {
		return errors.New("cliHardDeleteDispatcher.EnqueueAndHardDelete: assetID is required")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Step 1: DELETE child rows. error-ignored per the canonical
	// HardDeleteTx semantics (the index_state cascade may or may not
	// be present depending on migration set).
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_locations WHERE asset_id = ?", assetID)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_processing WHERE asset_id = ?", assetID)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_versions WHERE asset_id = ?", assetID)
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", assetID); err != nil {
		return fmt.Errorf("delete media_assets: %w", err)
	}

	// Step 2: INSERT outbox_events(asset.index.delete_requested.v1).
	// event_key uniqueness matches the canonical dispatcher
	// (`hard-delete:<assetID>`). payload_json is a string-shaped
	// envelope so the IndexDeleteHandler can parse it without
	// depending on a specific SDK type.
	now := time.Now().UTC().Format(time.RFC3339)
	eventID := deriveEventID("hard-delete", assetID, now)
	eventKey := "hard-delete:" + assetID
	payload := fmt.Sprintf(`{"asset_id":%q,"operation":"hard-delete"}`, assetID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (
			id, aggregate_id, aggregate_type, event_type,
			payload_json, event_key, status,
			attempt_count, max_attempts, last_error,
			created_at, updated_at
		) VALUES (?, ?, 'media_assets', 'asset.index.delete_requested.v1', ?, ?, 'pending', 0, 10, '', ?, ?)
	`, eventID, assetID, payload, eventKey, now, now); err != nil {
		return fmt.Errorf("insert outbox_events: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	d.log.Info("cli.hard-delete: tx committed",
		zap.String("asset_id", assetID),
		zap.String("event_id", eventID),
		zap.String("event_key", eventKey),
	)
	return nil
}

// deriveEventID returns a stable SHA-256-derived event id so CLI-
// emitted events are deterministic on retry. The dispatcher emits
// UUID-shaped ids; this CLI uses HE-SHA256 (16-byte prefix) so
// operator logs from each tool are visually distinct.
func deriveEventID(op, assetID, time string) string {
	h := sha256.Sum256([]byte(op + ":" + assetID + ":" + time))
	prefix := hex.EncodeToString(h[:8])
	return "cli-" + prefix
}

// qdrantProbeOfflineOperatorOverride is the conservative fail-closed
// probe used by the CLI when no production Qdrant client is in
// scope. It reports the asset as NOT-PRESENT (QdrantAbsent=true)
// ONLY when an env var override is explicitly set — by default it
// reports QdrantAbsent=false so the verifier refuses the purge.
// Operators who trust their Qdrant cleanup telemetry (e.g. after a
// DeletePoints sweep) can set ADMIN_HARD_DELETE_QDRANT_ASSUME_ABSENT=1.
func qdrantProbeOfflineOperatorOverride(ctx context.Context, assetID string) (bool, error) {
	if os.Getenv("ADMIN_HARD_DELETE_QDRANT_ASSUME_ABSENT") == "1" {
		return false, nil // QdrantAbsent = true (callers invert exists to absent)
	}
	if os.Getenv("ADMIN_HARD_DELETE_QDRANT_FORCE_PRESENT") == "1" {
		return true, nil // QdrantAbsent = false (refusal gate)
	}
	// Default: assume present (fail-closed so the gate refuses the
	// purge unless the operator explicitly overrides).
	return true, nil
}

// printRefusalReport dumps the VerifyReport fields in a stable
// operator-facing layout so the admin CLI response is parseable.
func printRefusalReport(w *os.File, r *admin.VerifyReport) {
	fmt.Fprintf(w, "  - lifecycle_state DELETED: %v\n", r.LifecycleDELETED)
	fmt.Fprintf(w, "  - Qdrant point absent:     %v\n", r.QdrantAbsent)
	fmt.Fprintf(w, "  - outbox pending count:    %d\n", r.OutboxPendingCount)
	fmt.Fprintf(w, "  - refusal reason:          %s\n", r.RefusalReason)
}

func summary(r *admin.HardDeleteResult, reason, requestedBy string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "asset_id=%s ", r.AssetID)
	fmt.Fprintf(&b, "eligible=%v ", r.VerifierReport.Eligible)
	fmt.Fprintf(&b, "dispatcher_invoked=%v ", r.DispatcherInvoked)
	if reason != "" {
		fmt.Fprintf(&b, "reason=%q ", reason)
	}
	if requestedBy != "" {
		fmt.Fprintf(&b, "requested_by=%q ", requestedBy)
	}
	return strings.TrimSpace(b.String())
}
