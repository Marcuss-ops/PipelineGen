// cmd/admin images-catalog-cleanup — report and (opt-in) retire damaged rows of
// the durable PERSON image catalog.
//
// The catalog is written by the VidRush image path; nothing in the write path
// can retroactively clean damage that older rows already carry. This command is
// the operator surface for that damage:
//
//	duplicate_source_url      two live rows of one entity on the same normalized URL
//	duplicate_content_sha256  two live rows of one entity on the same bytes
//	suspect_person_entity     a PERSON row whose name is a scene phrase
//	materialization_cas_missing  materialized bytes absent from the local CAS
//
// A duplicate group keeps its OLDEST candidate (lowest id); every other row is
// surplus. The default is a dry run that changes nothing. --apply retires the
// reported rows through the canonical lifecycle transition (status=retired) —
// it never deletes bytes, and a retired row is skipped by later audits and by
// every catalog read.
//
// Usage:
//
//	go run ./cmd/admin images-catalog-cleanup [--json] [--apply]
//	    [--retire-suspect-entities]
package maintenance

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	sqentity "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/entitycatalog"
)

// catalogAuditReader is the narrow read port this command needs. It is
// implemented by the SQLite adapter, the sole SQL owner of the catalog tables.
type catalogAuditReader interface {
	ListCatalogAuditRows(context.Context) ([]capentity.CatalogAuditRow, error)
}

// catalogStatusWriter is the canonical lifecycle transition: the command never
// runs its own UPDATE.
type catalogStatusWriter interface {
	SetCandidateStatus(context.Context, int64, string) error
}

type imagesCatalogCleanupReport struct {
	SchemaVersion int                             `json:"schema_version"`
	Mode          string                          `json:"mode"`
	Applied       bool                            `json:"applied"`
	Retired       int                             `json:"retired"`
	Candidates    int                             `json:"candidates"`
	Findings      []capentity.CatalogAuditFinding `json:"findings"`
	Retire        []int64                         `json:"retire"`
}

func RunImagesCatalogCleanup(args []string) error {
	fs := flag.NewFlagSet("images-catalog-cleanup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Machine-readable JSON output")
	apply := fs.Bool("apply", false, "Retire the reported rows (default: dry run)")
	retireSuspect := fs.Bool("retire-suspect-entities", false,
		"Also retire every live candidate of a PERSON entity whose name is a scene phrase (requires --apply)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := cli.CmdContext()
	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer func() { _ = dbSet.Close() }()

	repo := sqentity.NewSQLiteEntityImageCatalogAdapter(dbSet.Primary.DB)
	reader, ok := repo.(catalogAuditReader)
	if !ok {
		return fmt.Errorf("images-catalog-cleanup: catalog repository does not expose the audit read")
	}
	rows, err := reader.ListCatalogAuditRows(ctx)
	if err != nil {
		return err
	}

	opts := capentity.CatalogAuditOptions{
		RetireDuplicates:      *apply,
		RetireSuspectEntities: *apply && *retireSuspect,
	}
	report := capentity.AuditCatalog(rows, opts, catalogCASHasObject(cfg, log))
	payload := imagesCatalogCleanupReport{
		SchemaVersion: 1,
		Mode:          "catalog-audit",
		Candidates:    report.Candidates,
		Findings:      report.Findings,
		Retire:        report.Retire,
	}
	if payload.Findings == nil {
		payload.Findings = []capentity.CatalogAuditFinding{}
	}
	if payload.Retire == nil {
		payload.Retire = []int64{}
	}
	if *apply && len(report.Retire) > 0 {
		retired, retireErr := retireCatalogCandidates(ctx, repo, report.Retire)
		payload.Applied = true
		payload.Retired = retired
		if retireErr != nil {
			printImagesCatalogCleanup(payload, *jsonOut)
			return retireErr
		}
	}
	printImagesCatalogCleanup(payload, *jsonOut)
	return nil
}

// retireCatalogCandidates retires the ids through the repository's canonical
// transition and reports how many succeeded. A partial failure returns the
// count so the operator sees exactly how far the apply got.
func retireCatalogCandidates(ctx context.Context, writer catalogStatusWriter, ids []int64) (int, error) {
	retired := 0
	var firstErr error
	for _, id := range ids {
		if err := writer.SetCandidateStatus(ctx, id, capentity.CandidateStatusRetired); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("retire candidate %d: %w", id, err)
			}
			continue
		}
		retired++
	}
	if firstErr != nil {
		return retired, fmt.Errorf("images-catalog-cleanup: retired %d/%d candidates: %w", retired, len(ids), firstErr)
	}
	return retired, nil
}

// catalogCASHasObject answers the audit's CAS question from the real store. An
// unavailable store returns nil (the check is skipped) rather than claiming the
// bytes are missing.
func catalogCASHasObject(cfg *config.Config, log *zap.Logger) func(string) bool {
	if cfg == nil {
		return nil
	}
	reader, err := cas.OpenReader(filepath.Join(cfg.Storage.AbsDataDir(), "cas"))
	if err != nil {
		if log != nil {
			log.Warn("images-catalog-cleanup: CAS unavailable; skipping the materialization check", zap.Error(err))
		}
		return nil
	}
	return func(sha string) bool {
		path, err := reader.LocalPath(sha)
		if err != nil {
			return false
		}
		info, err := os.Stat(path)
		return err == nil && !info.IsDir() && info.Size() > 0
	}
}

// imagesCatalogMaxPrintedFindings bounds each printed section.
const imagesCatalogMaxPrintedFindings = 10

func printImagesCatalogCleanup(report imagesCatalogCleanupReport, jsonOut bool) {
	if jsonOut {
		payload, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Printf("images-catalog-cleanup: marshal report: %v\n", err)
			return
		}
		fmt.Println(string(payload))
		return
	}
	mode := "DRY RUN (nothing changed; pass --apply to retire)"
	if report.Applied {
		mode = "APPLIED"
	}
	fmt.Println("=== Entity Image Catalog Cleanup ===")
	fmt.Printf("  mode:       %s\n", mode)
	fmt.Printf("  candidates: %d\n", report.Candidates)
	if report.Applied {
		fmt.Printf("  retired:    %d\n", report.Retired)
	}
	if len(report.Findings) == 0 {
		fmt.Println("  findings:   none")
		return
	}
	byKind := make(map[string][]capentity.CatalogAuditFinding)
	kinds := make([]string, 0)
	for _, finding := range report.Findings {
		if _, ok := byKind[finding.Kind]; !ok {
			kinds = append(kinds, finding.Kind)
		}
		byKind[finding.Kind] = append(byKind[finding.Kind], finding)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		findings := byKind[kind]
		fmt.Printf("\n  --- %s (%d findings) ---\n", kind, len(findings))
		if kind == capentity.CatalogFindingMaterializationNoCAS {
			fmt.Println("      expected for images published before the local CAS existed; reuse falls back to the Drive copy")
		}
		// Keep the actionable sections readable: an old catalog has hundreds of
		// CAS-absence entities, and burying two polluted ones under them is how
		// a cleanup report gets ignored. --json carries the full list.
		for i, finding := range findings {
			if i == imagesCatalogMaxPrintedFindings {
				fmt.Printf("    … %d more (use --json for the full list)\n", len(findings)-imagesCatalogMaxPrintedFindings)
				break
			}
			fmt.Printf("    %-38s %s\n", finding.EntityID, finding.EntityName)
			fmt.Printf("      candidates=%v  %s\n", finding.CandidateIDs, finding.Detail)
		}
	}
	if !report.Applied && len(report.Retire) > 0 {
		fmt.Printf("\n  would retire: %v\n", report.Retire)
	}
}
