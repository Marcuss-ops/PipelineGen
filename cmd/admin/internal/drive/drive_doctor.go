// cmd/admin/drive_doctor.go — F4: Drive doctor CLI command (July 2026)
//
// Reads the drive_folder_catalog table and prints a per-destination
// status report. Operators use this to verify that the catalog is
// healthy and all destinations have active folder mappings.
//
// Usage:
//
//		go run ./cmd/admin drive-doctor [--root <ID>] [--json]
//
//	  --root    Drive folder ID of the media root (default: config
//	            VELOX_DRIVE_MEDIA_ROOT, then the built-in default)
//	  --json    Output as JSON (default: human-readable table)
//
// //
// godlike/06 SSOT (one canonical owner per fact): the doctor CLI is a
// READ-ONLY diagnostic surface. It never writes to Drive or the catalog.
//
// godlike/07 NO-FAKE-AVAILABILITY: if the catalog table is empty, the
// doctor reports "0 entries" and exits 0 — this is a valid state (no
// bootstrap has been run yet), NOT a silent success.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

type doctorDestination struct {
	Destination string `json:"destination"`
	Namespace   string `json:"namespace"`
	FolderCount int    `json:"folder_count"`
	Statuses    string `json:"statuses"`
}

type doctorReport struct {
	// Root is the resolved media-root folder ID; RootSource records where
	// it came from ("flag" | "config" | "default"). RootConfigured is true
	// only when the operator actually configured a root (flag/config) — a
	// fallback to the built-in default is reported as not-configured so the
	// doctor never implies an env var is set when it is not.
	Root           string              `json:"root"`
	RootSource     string              `json:"root_source"`
	RootConfigured bool                `json:"root_configured"`
	TotalEntries   int                 `json:"total_entries"`
	Destinations   []doctorDestination `json:"destinations"`
}

func RunDriveDoctor(args []string) error {
	fs := flag.NewFlagSet("drive-doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "Drive folder ID of the media root (default: config, then built-in default)")
	jsonOut := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	return executeDoctor(cli.CmdContext(), cfg, log, *root, *jsonOut)
}

// resolveDoctorRoot resolves the media root the doctor reports on:
// explicit --root > cfg.Drive.RootFolder() > built-in default. The
// second return value is the source label, kept honest so an unset env
// var is never reported as "configured".
func resolveDoctorRoot(flagRoot string, cfg *config.Config) (string, string) {
	if id := strings.TrimSpace(flagRoot); id != "" {
		return id, "flag"
	}
	if cfg != nil {
		if id := strings.TrimSpace(cfg.Drive.RootFolder()); id != "" {
			return id, "config"
		}
	}
	if id := strings.TrimSpace(config.DefaultMediaRootFolderID); id != "" {
		return id, "default"
	}
	return "", "none"
}

func executeDoctor(ctx context.Context, cfg *config.Config, log *zap.Logger, flagRoot string, jsonOut bool) error {
	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("drive-doctor: open database set: %w", err)
	}
	defer dbSet.Close()

	catalogRepo := sqlitedelivery.NewRepository(dbSet.Primary.DB)
	entries, err := catalogRepo.FindAll(ctx)
	if err != nil {
		return fmt.Errorf("drive-doctor: read catalog: %w", err)
	}

	report := buildDoctorReport(flagRoot, cfg, entries)

	if jsonOut {
		printDriveDoctorJSON(report)
	} else {
		printDoctorText(report)
	}
	return nil
}

// buildDoctorReport aggregates catalog entries into the doctor report.
// Pure (no I/O) so it is unit-testable with fake entries + config.
//
// Canonical destinations (canonicalDriveNamespaces) always appear, in
// canonical order, even when absent (statuses="missing"). Destinations
// found in the catalog but outside the canonical set are appended after.
func buildDoctorReport(flagRoot string, cfg *config.Config, entries []sqlitedelivery.CatalogEntry) doctorReport {
	report := doctorReport{
		Destinations: make([]doctorDestination, 0),
	}
	// Resolve the media root the operator cares about (flag → config →
	// built-in default) and record where it came from.
	report.Root, report.RootSource = resolveDoctorRoot(flagRoot, cfg)
	report.RootConfigured = report.RootSource == "flag" || report.RootSource == "config"
	report.TotalEntries = len(entries)

	// Group by destination.
	byDest := make(map[string][]sqlitedelivery.CatalogEntry)
	for _, e := range entries {
		byDest[e.Destination] = append(byDest[e.Destination], e)
	}

	for _, c := range canonicalDriveNamespaces {
		group, ok := byDest[c.Destination]
		dd := doctorDestination{Destination: c.Destination, Namespace: c.Namespace}
		if ok {
			dd.FolderCount = len(group)
			statuses := make(map[string]int)
			for _, e := range group {
				statuses[e.Status]++
			}
			dd.Statuses = formatStatuses(statuses)
			delete(byDest, c.Destination)
		} else {
			dd.Statuses = "missing"
		}
		report.Destinations = append(report.Destinations, dd)
	}

	// Any destinations in catalog but not in canonical order.
	for dest, group := range byDest {
		statuses := make(map[string]int)
		for _, e := range group {
			statuses[e.Status]++
		}
		report.Destinations = append(report.Destinations, doctorDestination{
			Destination: dest,
			Namespace:   group[0].Namespace,
			FolderCount: len(group),
			Statuses:    formatStatuses(statuses),
		})
	}
	return report
}

// formatStatuses renders a status→count map as a deterministic, sorted
// "status=N, status=M" string ("none" when empty).
func formatStatuses(s map[string]int) string {
	if len(s) == 0 {
		return "none"
	}
	statuses := make([]string, 0, len(s))
	for status := range s {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%s=%d", status, s[status]))
	}
	return strings.Join(parts, ", ")
}

// printDriveDoctorJSON is the local JSON helper. Named to avoid
// the redeclaration with text_tracks_backfill.go's
// printTextTracksBackfillJSON (which takes a different struct
// type and is package-wide).
func printDriveDoctorJSON(report doctorReport) {
	b, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(b))
}

func printDoctorText(report doctorReport) {
	fmt.Println("Drive Doctor")
	if report.Root == "" {
		fmt.Println("Media root: (not configured, no default available)")
	} else {
		fmt.Printf("Media root: %s (source: %s)\n", report.Root, report.RootSource)
	}
	if !report.RootConfigured {
		fmt.Println("  ⚠️  VELOX_DRIVE_MEDIA_ROOT is not set; using the built-in default.")
		fmt.Println("      Pass --root <ID> or set the env var to pin it explicitly.")
	}
	fmt.Printf("Total catalog entries: %d\n\n", report.TotalEntries)

	if report.TotalEntries == 0 {
		fmt.Println("No catalog entries. Run 'drive-bootstrap --root <ID> --apply' to populate the catalog.")
		return
	}

	fmt.Printf("%-20s %-16s %-6s %s\n", "destination", "namespace", "count", "statuses")
	fmt.Println(strings.Repeat("-", 70))
	for _, d := range report.Destinations {
		fmt.Printf("%-20s %-16s %-6d %s\n", d.Destination, d.Namespace, d.FolderCount, d.Statuses)
	}
	fmt.Println()
}
