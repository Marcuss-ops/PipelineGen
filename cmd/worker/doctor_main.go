// cmd/worker/doctor_main.go (RW-PROD-016, June 2026).
//
// The `--mode=doctor` subcommand of the worker binary. Runs the
// Aggregator from internal/application/workerdoctor and prints a
// JSON or human-readable report. Exit 0 on READY, 1 on NOT_READY,
// 2 on usage error.
//
// We deliberately do NOT call app.InitWorkerComposition here:
// the doctor is a pre-boot introspection tool, and pulling in the
// full composition graph (DB, repos, services, providers, ...) is
// significantly more expensive than reading the same config the
// worker would later hydrate.
//
// Reuse: nothing is duplicated. Config validation runs cfg.Validate;
// cert probes call pkg/tlsload; engine probes call exec.LookPath;
// master probe calls /health with the same budget the worker uses;
// ready probe reads /ready with the canonical body the production
// ReadyChecker emits.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/workerruntime"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/workerdoctor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// doctorFlags is the parsed CLI surface for the doctor. The struct
// is local to this file to keep main.go's top-level main() readable.
type doctorFlags struct {
	flagSet    *flag.FlagSet
	jsonOutput bool
	production bool
	quiet      bool
}

// newDoctorFlags wires flag defaults. -json=true forces machine
// output regardless of TTY. -production treats warnings as failures
// (future — currently propagates but doesn't flip any extra verdict
// yet, since probes are conservative by default).
func newDoctorFlags(name string) *doctorFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	f := &doctorFlags{flagSet: fs}
	fs.BoolVar(&f.jsonOutput, "json", false, "Emit machine-readable JSON to stdout (default: human-readable)")
	fs.BoolVar(&f.production, "production", false, "Treat the worker as production; tighter thresholds for runtime/cert probes")
	fs.BoolVar(&f.quiet, "quiet", false, "Suppress non-essential stderr lines; on READY print nothing to stderr")
	return f
}

// runDoctor loads the config and runs the doctor. Returned exit
// code is suitable for os.Exit.
//
// The signature is independent of cmd/worker/main.go so this
// function is unit-testable from cmd/worker/doctor_main_test.go
// without touching the worker binary boot path.
func runDoctor(args []string, log *zap.Logger) int {
	flags := newDoctorFlags("velox-worker-agent doctor")
	if err := flags.flagSet.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "velox-worker-agent doctor: parse error:", err)
		return workerdoctor.ExitUsage
	}

	// 1) Load config (mirrors cmd/worker main.go pattern).
	cfg, err := config.GetFromPath(getStringFlag(flags.flagSet, "config", "config.yaml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL: failed to load config:", err)
		return workerdoctor.ExitUsage
	}
	dt := nowOrZero()
	dp := workerdoctor.NewDefaultProbes()

	var agg *workerdoctor.Aggregator
	if cfg != nil {
		// Determine master URL via the same priority the worker
		// uses: env wins over config. resolveMasterURL lives in
		// cmd/worker/main.go; we re-derive it here so the doctor
		// stays hermetic.
		masterURL := workerruntime.ResolveMasterURL(cfg)
		dcfg := workerdoctor.NewDoctorConfig(cfg)
		agg = workerdoctor.NewFromConfig(dcfg, masterURL, dp)
		agg.WorkerID = workerruntime.Env("VELOX_WORKER_ID", workerruntime.Hostname("unknown"))
		agg.WorkerVersion = workerruntime.Env("VELOX_WORKER_VERSION", "dev")
		agg.MasterURL = masterURL
		agg.MTLSEnabled = dcfg.MTLSEnabled()
		agg.ConfigPath = getStringFlag(flags.flagSet, "config", "config.yaml")
		agg.Production = flags.production
	} else {
		agg = workerdoctor.NewFromConfigEmpty(dp)
	}

	// 2) Wire /ready probe (only useful after master is reachable).
	// We still wire it; probeMasterReachable above is informational,
	// /ready is the deep one. The probe internally polls; if master
	// is down, /ready returns ok=false and verdict flips accordingly.
	if agg.MasterURL != "" {
		if werr := workerdoctor.WireReady(agg, agg.MasterURL, dp); werr != nil {
			log.Warn("doctor: WireReady failed", zap.Error(werr))
		}
	}

	// 3) Run + emit.
	// AGENTS.md §7 post-write save ctx — doctor tool composition root;
	// one-shot RunOnce aggregation has no parent ctx, so Background is
	// the canonical choice for a CLI subcommand.
	rep := agg.RunOnce(context.Background())
	out := chooseOutput(flags, *rep, agg)

	if flags.jsonOutput {
		emitJSON(*out, flags, log)
	} else {
		emitHuman(*out, flags, log, agg)
	}

	exit := workerdoctor.ReturnCodeFromVerdict(rep.Verdict)
	if rep.Verdict != workerdoctor.VerdictReady && !flags.quiet {
		diag := agg.Diagnose(rep)
		for _, d := range diag {
			fmt.Fprintln(os.Stderr, "  -", d)
		}
	}
	_ = dt // reserved for future timestamp-consistent logging
	return exit
}

// emitJSON marshals the report with sorted keys and a trailing
// newline so shell pipelines behave predictably. Errors are
// surfaced to stderr with a non-zero exit (handled by caller).
func emitJSON(rep workerdoctor.Report, _ *doctorFlags, log *zap.Logger) {
	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		log.Error("doctor: marshal report failed", zap.Error(err))
		fmt.Fprintln(os.Stderr, "FATAL: could not marshal doctor report:", err)
		os.Exit(workerdoctor.ExitInternal)
	}
	fmt.Println(string(buf))
}

// emitHuman renders the report as a compact table. The format is
// plain ASCII with deterministic ordering so diffs in CI logs are
// stable. We deliberately avoid ANSI colors: doctor output may
// be fed into log shippers that don't grok color escapes.
func emitHuman(rep workerdoctor.Report, flags *doctorFlags, _ *zap.Logger, _ *workerdoctor.Aggregator) {
	if !flags.quiet {
		fmt.Printf("velox-worker-agent doctor — verdict: %s\n", rep.Verdict)
		fmt.Printf("schema_version: %d   timestamp: %s   total_ms: %d\n",
			rep.SchemaVersion, rep.Timestamp.Format(time.RFC3339), rep.TotalDurationMS)
		if rep.WorkerID != "" {
			fmt.Printf("worker_id: %s   worker_version: %s\n", rep.WorkerID, rep.WorkerVersion)
		}
		if rep.MasterURL != "" {
			fmt.Printf("master_url: %s   mtls_enabled: %v\n", rep.MasterURL, rep.MTLSEnabled)
		}
		fmt.Println()
	}
	fmt.Printf("  %-22s  %-7s  %-12s  %s\n", "check", "verdict", "applicable", "duration_ms")
	fmt.Printf("  %-22s  %-7s  %-12s  %s\n", strings.Repeat("-", 22), strings.Repeat("-", 7), strings.Repeat("-", 12), strings.Repeat("-", 11))
	for _, id := range workerdoctor.AllCheckIDs {
		cr, ok := rep.Checks[id]
		if !ok {
			continue
		}
		v := "PASS"
		if !cr.OK && cr.Applicable {
			v = "FAIL"
		} else if !cr.Applicable {
			v = "SKIP"
		}
		fmt.Printf("  %-22s  %-7s  %-12t  %d\n", id, v, cr.Applicable, cr.DurationMS)
		if !flags.quiet && cr.Error != "" {
			fmt.Printf("    error: %s\n", cr.Error)
		}
		if !flags.quiet && cr.Note != "" {
			fmt.Printf("    note : %s\n", cr.Note)
		}
	}
	fmt.Println()
	switch rep.Verdict {
	case workerdoctor.VerdictReady:
		fmt.Println("READY — safe to start")
	default:
		fmt.Println("NOT_READY — fix the failing checks listed above; the worker would fail-fast at boot")
	}
}

// chooseOutput is a no-op at the moment — both modes show the same
// JSON-equivalent fields — but it's a seam for future human-only
// enrichments (e.g. summary statistics) without a flag-tangle.
func chooseOutput(_ *doctorFlags, rep workerdoctor.Report, _ *workerdoctor.Aggregator) *workerdoctor.Report {
	return &rep
}

// getStringFlag extracts a string flag value with a default.
// Wraps flag.FlagSet.Get in a nil-safe way for the doctor's nested
// flag set.
func getStringFlag(fs *flag.FlagSet, name, def string) string {
	if fs != nil {
		// Visit looks for explicitly-set values; if unset, fall through.
		var explicit string
		fs.Visit(func(f *flag.Flag) {
			if f.Name == name {
				explicit = f.Value.String()
			}
		})
		if explicit != "" {
			return explicit
		}
	}
	return def
}

// nowOrZero is the testable date seam; in production returns the
// zero time so we don't accidentally bake it into the report.
func nowOrZero() time.Time {
	if t := os.Getenv("VELOX_DOCTOR_FIXED_TIME"); t != "" {
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// keep http.Client import alive when production build drops the
// unused dp.HTTPDo override path.
var _ = http.Client{}
