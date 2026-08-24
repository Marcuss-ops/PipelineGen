package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"encoding/json"
	"flag"
	"fmt"
	"os"

	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	platformcontrol "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/controlplane"
)

func RunControlPlaneVerify(args []string) error {
	fs := flag.NewFlagSet("control-plane verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "emit JSON")
	deep := fs.Bool("deep", false, "certify the complete migration ledger")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	db, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("control-plane verify: open DB: %w", err)
	}
	defer db.Close()
	topology := []storage.ConfiguredDatabase{
		{Name: "primary", Path: cfg.Storage.PrimaryDBFullPath(), Role: storage.ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "observability", Path: cfg.Storage.ObservabilityDBFullPath(), Role: storage.ControlPlaneRoleReadOnly, Writable: true, ControlPlane: false},
	}
	if cfg.Jobs.SplitDBEnabled {
		jobsPath := cfg.Jobs.JobsDBPath
		if jobsPath == "" {
			jobsPath = cfg.Storage.PrimaryDBFullPath()
		}
		topology = append(topology, storage.ConfiguredDatabase{
			Name: "jobs", Path: jobsPath, Role: storage.ControlPlaneRoleCanonical, Writable: true, ControlPlane: true,
		})
	}
	v, err := platformcontrol.NewWithTopology(db.DB, cfg.Storage.PrimaryDBFullPath(), topology)
	if err != nil {
		return err
	}
	var report capcontrol.Report
	if *deep {
		report, err = v.VerifyDeep(cli.CmdContext())
	} else {
		report, err = v.Verify(cli.CmdContext())
	}
	if err != nil {
		return err
	}
	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		printControlPlaneReport(report)
	}
	if !report.Healthy() {
		return fmt.Errorf("control-plane verification failed")
	}
	return nil
}

func RunControlPlane(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return fmt.Errorf("usage: admin control-plane verify [--json] [--deep]")
	}
	return runControlPlaneVerify(args[1:])
}

func printControlPlaneReport(r capcontrol.Report) {
	fmt.Println("PIPELINEGEN CONTROL PLANE VERIFICATION")
	fmt.Println("========================================")
	fmt.Printf("Canonical DB       %s\n", r.CanonicalDBPath)
	fmt.Printf("Database ID        %s\n", r.DatabaseID)
	fmt.Printf("Schema family      %s\n", r.SchemaFamily)
	fmt.Printf("Instance role      %s\n", r.InstanceRole)
	fmt.Printf("Schema version     %d\n", r.SchemaVersion)
	fmt.Printf("Migration gaps     %v\n", r.MigrationGaps)
	fmt.Printf("Migration checksum mismatches %v\n", r.MigrationChecksumMismatches)
	fmt.Printf("Assets             %d\n", r.Assets)
	fmt.Printf("Transcripts        %d\n", r.Transcripts)
	fmt.Printf("Descriptions       %d\n", r.Descriptions)
	fmt.Printf("Jobs               %d\n", r.Jobs)
	fmt.Printf("CAS objects        %d\n", r.CASObjects)
	fmt.Printf("CAS orphans        %d\n", r.CASOrphans)
	fmt.Printf("Broken CAS links   %d\n", r.BrokenCASLinks)
	fmt.Printf("Registry sequence  %d\n", r.RegistrySeq)
	fmt.Printf("Projection sequence %d\n", r.ProjectionSeq)
	fmt.Printf("Projection drift   %d\n\n", r.ProjectionDrift)
	fmt.Printf("Projection state   %s\n\n", r.ProjectionState)
	fmt.Printf("Performance runs   %d\n", r.PerformanceRuns)
	fmt.Printf("Uncorrelated perf  %d\n\n", r.UncorrelatedPerformanceRuns)
	for _, check := range r.Checks {
		fmt.Printf("%-30s %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	fmt.Printf("\nFINAL STATUS       %s\n", r.Status)
}
