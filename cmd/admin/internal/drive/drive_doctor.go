// cmd/admin/drive_doctor.go — F4: Drive doctor CLI command (July 2026)
//
// Reads the drive_folder_catalog table and prints a per-destination
// status report. Operators use this to verify that the catalog is
// healthy and all destinations have active folder mappings.
//
// Usage:
//
//		go run ./cmd/admin drive-doctor [--json]
//
//	  --json    Output as JSON (default: human-readable table)
//	  --db-path Override canonical SQLite DB path
//
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
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

type doctorDestination struct {
	Destination string `json:"destination"`
	Namespace   string `json:"namespace"`
	FolderCount int    `json:"folder_count"`
	Statuses    string `json:"statuses"`
}

type doctorReport struct {
	RootConfigured bool                `json:"root_configured"`
	TotalEntries   int                 `json:"total_entries"`
	Destinations   []doctorDestination `json:"destinations"`
}

func RunDriveDoctor(args []string) error {
	fs := flag.NewFlagSet("drive-doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	dbPath := fs.String("db-path", "", "Canonical SQLite DB path (default: $VELOX_DATA_DIR/media.db.sqlite)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	return executeDoctor(cli.CmdContext(), cfg, log, *jsonOut, *dbPath)
}

func executeDoctor(ctx context.Context, cfg *config.Config, log *zap.Logger, jsonOut bool, dbPathFlag string) error {
	path := cli.ResolveDBPath(cfg, dbPathFlag)
	if path == "" {
		return ErrAdminNoDB
	}

	sqliteDB, err := storage.OpenSQLiteDB(path, log)
	if err != nil {
		return fmt.Errorf("drive-doctor: open DB: %w", err)
	}
	defer sqliteDB.Close()

	catalogRepo := sqlitedelivery.NewRepository(sqliteDB.DB)
	entries, err := catalogRepo.FindAll(ctx)
	if err != nil {
		return fmt.Errorf("drive-doctor: read catalog: %w", err)
	}

	report := doctorReport{
		Destinations: make([]doctorDestination, 0),
	}

	// Check if media root is configured.
	report.RootConfigured = cfg.Drive.RootFolder() != ""
	report.TotalEntries = len(entries)

	// Group by destination.
	byDest := make(map[string][]sqlitedelivery.CatalogEntry)
	for _, e := range entries {
		byDest[e.Destination] = append(byDest[e.Destination], e)
	}

	// Canonical destination order — shared with drive-bootstrap.
	canonicalOrder := canonicalDriveNamespaces

	for _, c := range canonicalOrder {
		entries, ok := byDest[c.Destination]
		dd := doctorDestination{
			Destination: c.Destination,
			Namespace:   c.Namespace,
		}
		if ok {
			dd.FolderCount = len(entries)
			statuses := make(map[string]int)
			for _, e := range entries {
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
	for dest, entries := range byDest {
		statuses := make(map[string]int)
		for _, e := range entries {
			statuses[e.Status]++
		}
		report.Destinations = append(report.Destinations, doctorDestination{
			Destination: dest,
			Namespace:   entries[0].Namespace,
			FolderCount: len(entries),
			Statuses:    formatStatuses(statuses),
		})
	}

	if jsonOut {
		printDriveDoctorJSON(report)
	} else {
		printDoctorText(cfg, report)
	}
	return nil
}

func formatStatuses(s map[string]int) string {
	parts := make([]string, 0, len(s))
	for status, count := range s {
		parts = append(parts, fmt.Sprintf("%s=%d", status, count))
	}
	if len(parts) == 0 {
		return "none"
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

func printDoctorText(cfg *config.Config, report doctorReport) {
	fmt.Println("Drive Doctor")
	fmt.Printf("Media root configured: %t", report.RootConfigured)
	if report.RootConfigured {
		fmt.Printf(" (%s)", cfg.Drive.RootFolder())
	}
	fmt.Println()
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
