// cmd/admin/identity_audit.go — the identity audit (item 12).
//
// The audit composes the two canonical identity invariants that must be ZERO
// before the Qdrant reindex:
//
//   - source identity (SQLite): a (source_type, source_ref) tuple resolving
//     to more than one canonical asset (media_asset_sources). Owned by
//     CanonicalIdentityResolver.AuditIdentity.
//   - point identity (Qdrant): a canonical asset appearing in more than one
//     Qdrant point (payload.asset_id). Owned by
//     verification.CountDuplicateAssetPoints.
//
// Both halves are merged into one capregistry.IdentityAuditReport. The command
// returns a non-nil error (and the admin binary exits non-zero) when either
// counter is non-zero, so it can gate scripts/CI. godlike/07 fail-closed: an
// unresolvable Qdrant collection or a scroll error aborts, never a partial
// "clean" verdict.
package audit

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"encoding/json"
	"flag"
	"fmt"

	"go.uber.org/zap"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

func RunIdentityAudit(args []string) error {
	flags := flag.NewFlagSet("identity-audit", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit the report as JSON")
	collection := flags.String("collection", "", "Qdrant collection to audit (default: resolve the runtime alias)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	db, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open media database: %w", err)
	}
	defer db.Close()

	resolver, err := sqlitemediaregistry.NewCanonicalIdentityResolver(db.DB)
	if err != nil {
		return err
	}

	ctx := cli.CmdContext()

	// ── SQLite half: source identity ─────────────────────────────
	sourceReport, err := resolver.AuditIdentity(ctx)
	if err != nil {
		return fmt.Errorf("identity audit: source half: %w", err)
	}

	// ── Qdrant half: point identity ──────────────────────────────
	if !cfg.Qdrant.Enabled {
		return fmt.Errorf("identity audit: qdrant is disabled in config; the point-identity half requires qdrant.enabled=true")
	}
	client := transport.NewClient(&qdrantschema.Config{
		BaseURL: cfg.Qdrant.BaseURL,
		APIKey:  cfg.Qdrant.APIKey,
		Timeout: cfg.Qdrant.Timeout,
	}, log)

	target := *collection
	if target == "" {
		target = qdrantschema.DefaultV3Schema().RuntimeAlias
		if resolved, resolveErr := client.GetAliasTarget(ctx, target); resolveErr == nil && resolved != "" {
			target = resolved
		}
	}

	dupPoints, err := verification.CountDuplicateAssetPoints(ctx, client, target)
	if err != nil {
		return fmt.Errorf("identity audit: qdrant half: %w", err)
	}

	report := capregistry.IdentityAuditReport{
		DuplicateSourceIdentity: sourceReport.DuplicateSourceIdentity,
		DuplicateQdrantPoints:   dupPoints,
	}

	if *jsonOutput {
		encoded, marshalErr := json.Marshal(map[string]any{
			"report":     report,
			"collection": target,
		})
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Println(string(encoded))
	} else {
		log.Info("identity audit complete",
			zap.String("collection", target),
			zap.Int("duplicate_source_identity", report.DuplicateSourceIdentity),
			zap.Int("duplicate_qdrant_points", report.DuplicateQdrantPoints))
	}

	if report.DuplicateSourceIdentity != 0 || report.DuplicateQdrantPoints != 0 {
		return fmt.Errorf("identity audit FAILED: duplicate_source_identity=%d duplicate_qdrant_points=%d (both must be 0)",
			report.DuplicateSourceIdentity, report.DuplicateQdrantPoints)
	}
	return nil
}
