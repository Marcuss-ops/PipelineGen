package maintenance

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
)

// runPerformanceColdWarmReport generates the comparative cold #1 vs warm #2-N
// report from performance_operations: the first measured attempt of the scope
// is the cold bucket, the following attempts (up to --max-attempts) are the
// warm bucket, and each bucket is aggregated GROUP BY operation with
// AVG/MIN/MAX elapsed_ms. It is the verifier read of the same canonical
// registry the Chronon Metrics Adapter writes — a pure read, no migrations,
// no mutation.
//
//	admin performance-cold-warm [--job-id job_x] [--max-attempts 5] [--since 2026-08-01T00:00:00Z] [--format json|text]
func RunPerformanceColdWarmReport(args []string) error {
	fs := flag.NewFlagSet("performance-cold-warm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job-id", "", "Restrict the attempt sequence to one job (default: all jobs)")
	maxAttempts := fs.Int("max-attempts", 5, "Attempts considered: #1 = cold, #2..N = warm")
	since := fs.String("since", "", "Only attempts created at/after this RFC3339 timestamp (default: all)")
	format := fs.String("format", "json", "Output format: json (default) or text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *format {
	case "json", "text":
	default:
		return fmt.Errorf("--format must be \"json\" or \"text\"")
	}
	if *maxAttempts <= 0 {
		return fmt.Errorf("--max-attempts must be positive")
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

	store, err := perfstore.NewOperationStore(dbSet.Primary.DB)
	if err != nil {
		return err
	}

	ctx := context.Background()
	report, err := store.ColdWarmComparison(ctx, capperformance.ColdWarmOptions{
		JobID:       *jobID,
		MaxAttempts: *maxAttempts,
		Since:       *since,
	})
	if err != nil {
		return err
	}

	if *format == "text" {
		_, err = fmt.Fprint(os.Stdout, capperformance.RenderColdWarmText(report))
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
