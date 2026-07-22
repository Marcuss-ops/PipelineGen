// cmd/admin/dr_qdrant.go — QDRANT-005C PR3 (June 2026) admin CLI.
//
// Subcommands exposed under the `dr-qdrant` admin umbrella:
//
//	list-snapshots    list all snapshots for the active (or explicit)
//	                  collection. Output is one row per snapshot:
//	                  name, size_bytes, creation_time, checksum.
//	take-snapshot     create a new snapshot of the active (or explicit)
//	                  collection. Returns the snapshot descriptor.
//	restore-snapshot  verify-then-switch from a named snapshot into
//	                  the runtime alias. URL resolution + target
//	                  allocation + verify gate + alias switch live in
//	                  dr/RestoreService — this CLI is a thin wrapper.
//	apply-retention   drop old non-active collections matching the
//	                  schema prefix. Hard floor: keep_last_n=2.
//
// Wire-up pattern (mirrors reconcile_qdrant.go):
//  1. parseDrQdrantDispatcher — peel subcommand
//  2. appLogger / qdrant cfg — same heat path as reconcile_qdrant
//  3. Build canonical stack: transport.Client + CollectionManager +
//     SQLiteAssetStore + ReindexVerifier
//  4. Construct dr service via ServiceDeps struct (PR2-style)
//  5. Run + pretty-print; --json switches to JSON-only output
//
// Failure handling:
//   - infra / I/O errors         → exit 1 with non-empty stderr line
//   - verify-gate blocked (DR)   → exit 0, Applied=false printed;
//     operator inspects VerifyReport.Errors
package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/qdrant/dr"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/maintenance"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// drQdrantDispatcher is the parsed shape for `dr-qdrant <sub> ...`.
type drQdrantDispatcher struct {
	Sub string
	Raw []string
}

// parseDrQdrantDispatcher peels off the first positional arg as the
// subcommand. Rejects unknown subcommands.
func parseDrQdrantDispatcher(args []string) (drQdrantDispatcher, error) {
	if len(args) == 0 {
		return drQdrantDispatcher{}, errors.New(
			"dr-qdrant requires a subcommand: list-snapshots | take-snapshot | restore-snapshot | apply-retention",
		)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "list-snapshots", "take-snapshot", "restore-snapshot", "apply-retention":
		return drQdrantDispatcher{Sub: sub, Raw: args[1:]}, nil
	default:
		return drQdrantDispatcher{}, fmt.Errorf("dr-qdrant: unknown subcommand %q", sub)
	}
}

// RunDrQdrant is the cmd/admin/main.go entry point for dr-qdrant.
// The transport.Client is constructed here and shared by all 4 subcommands.
// SQLite is opened lazily in runDrRestore (the only subcommand that
// needs the asset store for the verifier).
//
// Failure mode for dr-qdrant itself: if qdrant.enabled=false in
// config, RunDrQdrant fails with a clear configuration-error line.
func RunDrQdrant(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	if !cfg.Qdrant.Enabled {
		return errors.New("dr-qdrant requires qdrant.enabled=true in config")
	}

	deps, err := parseDrQdrantDispatcher(args)
	if err != nil {
		return err
	}

	ctx := cli.CmdContext()
	log.Info("dr-qdrant starting",
		zap.String("subcommand", deps.Sub),
		zap.String("qdrant_url", cfg.Qdrant.BaseURL))

	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)
	schema := qdrantschema.DefaultV3Schema()

	switch deps.Sub {
	case "list-snapshots":
		return runDrListSnapshots(ctx, client, log, deps.Raw)
	case "take-snapshot":
		return runDrTakeSnapshot(ctx, client, log, deps.Raw)
	case "restore-snapshot":
		return runDrRestoreSnapshot(ctx, cfg, client, schema, log, deps.Raw)
	case "apply-retention":
		return runDrApplyRetention(ctx, client, schema, log, deps.Raw)
	}
	return fmt.Errorf("dr-qdrant: unknown subcommand %q", deps.Sub)
}

// resolveActiveCollection resolves the active alias target unless the
// operator passes --collection=NAME explicitly. Shared helper used by
// ListSnapshots + TakeSnapshot + RestoreSnapshot.
func resolveActiveCollection(ctx context.Context, client *transport.Client, override string, log *zap.Logger) (string, error) {
	if override != "" {
		return override, nil
	}
	alias := qdrantschema.DefaultV3Schema().RuntimeAlias
	target, err := client.GetAliasTarget(ctx, alias)
	if err != nil {
		return "", fmt.Errorf("resolve alias %q target: %w", alias, err)
	}
	if target == "" {
		return "", fmt.Errorf("alias %q has no target; pass --collection=NAME explicitly or attach an alias first", alias)
	}
	log.Info("resolved alias target", zap.String("alias", alias), zap.String("collection", target))
	return target, nil
}

// ── Subcommand: list-snapshots ────────────────────────────────────────

type listSnapshotsFlags struct {
	Collection string
	JSON       bool
}

func parseListSnapshotsFlags(args []string) (listSnapshotsFlags, error) {
	d := listSnapshotsFlags{}
	for _, a := range args {
		switch {
		case a == "--json":
			d.JSON = true
		case strings.HasPrefix(a, "--collection="):
			d.Collection = strings.TrimPrefix(a, "--collection=")
		default:
			if strings.HasPrefix(a, "-") {
				return d, fmt.Errorf("list-snapshots: unknown flag %s", a)
			}
		}
	}
	return d, nil
}

func runDrListSnapshots(ctx context.Context, client *transport.Client, log *zap.Logger, args []string) error {
	flags, err := parseListSnapshotsFlags(args)
	if err != nil {
		return err
	}
	collection, err := resolveActiveCollection(ctx, client, flags.Collection, log)
	if err != nil {
		return err
	}

	svc := dr.NewSnapshotServiceFromDeps(dr.SnapshotServiceDeps{
		Store: qdrantmaintenance.NewSnapshotStoreAdapter(client),
		Log:   log,
	})
	snaps, err := svc.List(ctx, collection)
	if err != nil {
		return err
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(snaps, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("=== dr-qdrant list-snapshots: %s ===\n", collection)
	if len(snaps) == 0 {
		fmt.Println("  (no snapshots — collection has never been snapshotted)")
		return nil
	}
	for _, s := range snaps {
		fmt.Printf("  - %s  (%d bytes, %s)\n", s.Name, s.Size,
			s.CreationTime.Format("2006-01-02T15:04:05Z"))
	}
	return nil
}

// ── Subcommand: take-snapshot ─────────────────────────────────────────

type takeSnapshotFlags struct {
	Collection string
	JSON       bool
}

func parseTakeSnapshotFlags(args []string) (takeSnapshotFlags, error) {
	d := takeSnapshotFlags{}
	for _, a := range args {
		switch {
		case a == "--json":
			d.JSON = true
		case strings.HasPrefix(a, "--collection="):
			d.Collection = strings.TrimPrefix(a, "--collection=")
		default:
			if strings.HasPrefix(a, "-") {
				return d, fmt.Errorf("take-snapshot: unknown flag %s", a)
			}
		}
	}
	return d, nil
}

func runDrTakeSnapshot(ctx context.Context, client *transport.Client, log *zap.Logger, args []string) error {
	flags, err := parseTakeSnapshotFlags(args)
	if err != nil {
		return err
	}
	collection, err := resolveActiveCollection(ctx, client, flags.Collection, log)
	if err != nil {
		return err
	}

	svc := dr.NewSnapshotServiceFromDeps(dr.SnapshotServiceDeps{
		Store: qdrantmaintenance.NewSnapshotStoreAdapter(client),
		Log:   log,
	})
	snap, err := svc.Take(ctx, collection)
	if err != nil {
		return err
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(snap, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("=== dr-qdrant take-snapshot: %s ===\n", collection)
	fmt.Printf("  Name:         %s\n", snap.Name)
	fmt.Printf("  Size:         %d bytes\n", snap.Size)
	fmt.Printf("  Created:      %s\n", snap.CreationTime.Format("2006-01-02T15:04:05Z"))
	fmt.Printf("  Pass to:      dr-qdrant restore-snapshot --name=%s\n", snap.Name)
	return nil
}

// ── Subcommand: restore-snapshot ──────────────────────────────────────

type restoreSnapshotFlags struct {
	Name           string
	Collection     string
	Alias          string
	ExpectedPoints int
	JSON           bool
}

func parseRestoreSnapshotFlags(args []string) (restoreSnapshotFlags, error) {
	d := restoreSnapshotFlags{}
	for _, a := range args {
		switch {
		case a == "--json":
			d.JSON = true
		case strings.HasPrefix(a, "--name="):
			d.Name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "--collection="):
			d.Collection = strings.TrimPrefix(a, "--collection=")
		case strings.HasPrefix(a, "--alias="):
			d.Alias = strings.TrimPrefix(a, "--alias=")
		case strings.HasPrefix(a, "--expected-points="):
			v, err := strconv.Atoi(strings.TrimPrefix(a, "--expected-points="))
			if err != nil {
				return d, fmt.Errorf("restore-snapshot: --expected-points must be int: %w", err)
			}
			d.ExpectedPoints = v
		default:
			if strings.HasPrefix(a, "-") {
				return d, fmt.Errorf("restore-snapshot: unknown flag %s", a)
			}
		}
	}
	if d.Name == "" {
		return d, errors.New("restore-snapshot: --name=<snapshot_name> is required")
	}
	return d, nil
}

// runDrRestoreSnapshot performs the verify-then-switch pipeline through
// dr.RestoreService. This is the SAFETY-CRITICAL subcommand — the
// alias flip is BLOCKED unless ReindexVerifier.VerifyReindex reports
// Ready=true on the restore-target collection.
//
// Failure mode contract:
//   - Ready=true   → alias flipped, exit 0
//   - Ready=false  → candidate kept; Applied=false; exit 0
//     (operator inspects VerifyReport.Errors to diagnose)
//   - infra failure → exit 1 with the error message
//
// The cfg parameter is the same shape appLogger returns in this
// package; RunDrQdrant forwards it. Sub-restore needs cfg.Storage
// to open the SQLite DB for the verifier's asset store.
func runDrRestoreSnapshot(ctx context.Context, cfg *config.Config, client *transport.Client, schema *qdrantschema.IndexSchema, log *zap.Logger, args []string) error {
	flags, err := parseRestoreSnapshotFlags(args)
	if err != nil {
		return err
	}
	alias := flags.Alias
	if alias == "" {
		alias = schema.RuntimeAlias
	}
	collection, err := resolveActiveCollection(ctx, client, flags.Collection, log)
	if err != nil {
		return err
	}
	expectedPoints := flags.ExpectedPoints
	if expectedPoints <= 0 {
		n, cntErr := client.CountPoints(ctx, collection)
		if cntErr != nil {
			return fmt.Errorf("auto-compute expected-points failed; pass --expected-points=N explicitly: %w", cntErr)
		}
		expectedPoints = n
		log.Info("auto-computed expected-points", zap.String("source", collection), zap.Int("n", expectedPoints))
	}

	sqliteDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media DB: %w", err)
	}
	defer sqliteDB.Close()

	cm := collections.NewCollectionManager(client, schema, log)
	assetStore := indexing.NewSQLiteAssetStore(sqliteDB.DB)

	svc := dr.NewRestoreServiceFromDeps(dr.RestoreServiceDeps{
		Store:    qdrantmaintenance.NewSnapshotStoreAdapter(client),
		Switcher: qdrantmaintenance.NewAliasSwitcherAdapter(client),
		Creator:  qdrantmaintenance.NewCollectionCreatorAdapter(cm),
		Verifier: qdrantmaintenance.NewVerifierAdapter(client, assetStore, schema, log),
		Metrics:  qdrantmaintenance.NewPromDRMetricsAdapter(),
		Log:      log,
	})

	report, err := svc.Restore(ctx, dr.RestoreOptions{
		Collection:     collection,
		SnapshotName:   flags.Name,
		ExpectedPoints: expectedPoints,
		Alias:          alias,
	})
	if err != nil {
		return err
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	}

	if !report.Applied {
		fmt.Printf("=== dr-qdrant restore-snapshot: VERIFY BLOCKED ===\n")
		if !flags.JSON {
			fmt.Printf("  Source:      %s\n", report.Source)
			fmt.Printf("  Snapshot:    %s\n", report.SnapshotName)
			fmt.Printf("  Target:      %s (KEPT for inspection)\n", report.Target)
			fmt.Printf("  Alias:       %s (UNCHANGED)\n", report.Alias)
			fmt.Printf("  Verify:      Ready=false (%d errors)\n", len(report.Verify.Errors))
			for i, e := range report.Verify.Errors {
				fmt.Printf("               [%d] %s\n", i, e)
			}
			fmt.Printf("  Duration:    %dms\n", report.DurationMs)
		}
		return nil // gate-blocked is exit 0 by design
	}
	fmt.Printf("=== dr-qdrant restore-snapshot: APPLIED ===\n")
	if !flags.JSON {
		fmt.Printf("  Source:      %s\n", report.Source)
		fmt.Printf("  Snapshot:    %s\n", report.SnapshotName)
		fmt.Printf("  Target:      %s\n", report.Target)
		fmt.Printf("  Alias:       %s -> %s\n", report.Alias, report.Target)
		fmt.Printf("  Verify:      Ready=true (points=%d missing=%d orphan=%d)\n",
			report.Verify.ActualPoints,
			report.Verify.MissingCount, report.Verify.OrphanCount)
		fmt.Printf("  Duration:    %dms\n", report.DurationMs)
	}
	return nil
}

// ── Subcommand: apply-retention ──────────────────────────────────────

type applyRetentionFlags struct {
	RetentionDays int
	KeepLastN     int
	Protect       string
	JSON          bool
}

func parseApplyRetentionFlags(args []string) (applyRetentionFlags, error) {
	d := applyRetentionFlags{}
	for _, a := range args {
		switch {
		case a == "--json":
			d.JSON = true
		case strings.HasPrefix(a, "--retention-days="):
			v, err := strconv.Atoi(strings.TrimPrefix(a, "--retention-days="))
			if err != nil {
				return d, fmt.Errorf("apply-retention: --retention-days must be int: %w", err)
			}
			d.RetentionDays = v
		case strings.HasPrefix(a, "--keep-last-n="):
			v, err := strconv.Atoi(strings.TrimPrefix(a, "--keep-last-n="))
			if err != nil {
				return d, fmt.Errorf("apply-retention: --keep-last-n must be int: %w", err)
			}
			d.KeepLastN = v
		case strings.HasPrefix(a, "--protect="):
			d.Protect = strings.TrimPrefix(a, "--protect=")
		default:
			if strings.HasPrefix(a, "-") {
				return d, fmt.Errorf("apply-retention: unknown flag %s", a)
			}
		}
	}
	if d.RetentionDays <= 0 {
		return d, errors.New("apply-retention: --retention-days=N is required (set N=1 for a keep_last_n-only sweep)")
	}
	return d, nil
}

func runDrApplyRetention(ctx context.Context, client *transport.Client, schema *qdrantschema.IndexSchema, log *zap.Logger, args []string) error {
	flags, err := parseApplyRetentionFlags(args)
	if err != nil {
		return err
	}
	cm := collections.NewCollectionManager(client, schema, log)
	// Cycle break (June 2026): the executor port takes dr-local
	// RetentionConfig/RetentionResult; cm.CleanupWithConfig takes
	// qdrant-local types. RetentionExecutorAdapter bridges the two
	// without exposing the qdrant types to the dr application layer.
	svc := dr.NewRetentionServiceFromDeps(dr.RetentionServiceDeps{
		Executor: qdrantmaintenance.NewRetentionExecutorAdapter(cm),
		Log:      log,
	})

	res, err := svc.Apply(ctx, dr.RetentionConfig{
		RetentionDays:           flags.RetentionDays,
		KeepLastN:               flags.KeepLastN,
		ProtectedRollbackTarget: flags.Protect,
	})
	if err != nil {
		return err
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("=== dr-qdrant apply-retention ===\n")
	fmt.Printf("  Dropped:    %d\n", res.CollectionsDropped)
	fmt.Printf("  Kept:       %d (active + protected target + keep-last-n tail)\n", res.CollectionsKept)
	if len(res.DroppedNames) > 0 {
		fmt.Printf("  Names:      %s\n", strings.Join(res.DroppedNames, ", "))
	}
	if len(res.Errors) > 0 {
		fmt.Printf("  Errors:     %d\n", len(res.Errors))
		for i, e := range res.Errors {
			fmt.Printf("               [%d] %s\n", i, e)
		}
	}
	return nil
}
