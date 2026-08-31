package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/performance"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

// runPerformanceReport projects one or more jobs into the canonical read-only
// performance report. It is the CLI consumer of the single shared registry
// (performance.Phases) + resolver (performance.DefaultPhaseResolver) + source
// (perfstore.Source) — it never re-derives phase names, mappings, or queries.
//
// It opens the primary and observability SQLite databases directly (no full
// composition, no migrations) so the report is a pure read of existing state.
//
//	admin performance-report --job-ids job_a,job_b,job_c
func RunPerformanceReport(args []string) error {
	fs := flag.NewFlagSet("performance-report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobIDs := fs.String("job-ids", "", "Comma-separated job IDs to project (required)")
	format := fs.String("format", "json", "Output format: json (default) or text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := cli.SplitBackfillCSV(*jobIDs)
	if len(ids) == 0 {
		return fmt.Errorf("--job-ids is required")
	}
	switch *format {
	case "json", "text":
	default:
		return fmt.Errorf("--format must be \"json\" or \"text\"")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()

	src, err := perfstore.NewSource(dbSet.Primary.DB, dbSet.Observability.DB)
	if err != nil {
		return err
	}
	agg := performance.NewAggregator(src, performance.DefaultPhaseResolver{})

	ctx := context.Background()

	if *format == "text" {
		aggregate, err := agg.Compare(ctx, ids)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(os.Stdout, performance.RenderText(aggregate))
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	if len(ids) == 1 {
		report, err := agg.BuildJobReport(ctx, ids[0])
		if err != nil {
			return err
		}
		return enc.Encode(report)
	}
	aggregate, err := agg.Compare(ctx, ids)
	if err != nil {
		return err
	}
	return enc.Encode(aggregate)
}
