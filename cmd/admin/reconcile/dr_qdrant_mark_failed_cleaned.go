package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"go.uber.org/zap"
)

type markFailedCleanedFlags struct {
	Collection string
	JSON       bool
}

func parseMarkFailedCleanedFlags(args []string) (markFailedCleanedFlags, error) {
	d := markFailedCleanedFlags{}
	for _, a := range args {
		switch {
		case a == "--json":
			d.JSON = true
		case strings.HasPrefix(a, "--collection="):
			d.Collection = strings.TrimPrefix(a, "--collection=")
		default:
			if strings.HasPrefix(a, "-") {
				return d, fmt.Errorf("mark-failed-cleaned: unknown flag %s", a)
			}
		}
	}
	if err := schema.ValidateEmergencyCollection(d.Collection); err != nil {
		return d, fmt.Errorf("mark-failed-cleaned: invalid --collection: %w", err)
	}
	return d, nil
}

// runDrMarkFailedCleaned marks a verified-cleaned FAILED projection as
// FAILED_CLEANED while preserving its attempt history.
func runDrMarkFailedCleaned(ctx context.Context, cfg *config.Config, client *transport.Client, schema *schema.IndexSchema, log *zap.Logger, args []string) error {
	flags, err := parseMarkFailedCleanedFlags(args)
	if err != nil {
		return err
	}
	cm := collections.NewCollectionManager(client, schema, log)

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	sqliteDB := dbSet.Primary
	registryLedger, err := regsql.NewLedger(sqliteDB.DB)
	if err != nil {
		return fmt.Errorf("create media registry ledger: %w", err)
	}
	if err := cm.SetRegistryLedger(ctx, registryLedger); err != nil {
		return fmt.Errorf("hydrate media registry projection ledger: %w", err)
	}

	projections, err := registryLedger.ListProjections(ctx)
	if err != nil {
		return fmt.Errorf("list projections: %w", err)
	}
	projectionID := ""
	for _, p := range projections {
		if p.CollectionName == flags.Collection {
			projectionID = p.ProjectionID
			break
		}
	}
	if projectionID == "" {
		return fmt.Errorf("mark-failed-cleaned: no projection registered for collection %q", flags.Collection)
	}

	if err := cm.MarkFailedCleaned(ctx, projectionID); err != nil {
		return err
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(map[string]any{
			"projection_id": projectionID,
			"collection":    flags.Collection,
			"status":        "FAILED_CLEANED",
		}, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("=== dr-qdrant mark-failed-cleaned: %s ===\n", flags.Collection)
	fmt.Printf("  Projection: %s\n", projectionID)
	fmt.Printf("  Status:     FAILED_CLEANED (attempt history preserved)\n")
	return nil
}

var _ = qdrantmaintenance.NewRetentionExecutorAdapter
