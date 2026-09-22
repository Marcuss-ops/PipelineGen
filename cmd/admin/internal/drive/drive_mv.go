// cmd/admin/internal/drive/drive_mv.go — drive-mv: move Drive files
// between folders.
//
// Usage:
//
//	go run ./cmd/admin drive-mv --to <DEST_FOLDER_ID> --file <FILE_ID> [--file <ID>...] [--apply]
//	go run ./cmd/admin drive-mv --from <SRC_FOLDER_ID> --name <FILENAME> --to <DEST_FOLDER_ID> [--apply]
//
//	--file    Drive file ID to move (repeatable and/or comma-separated)
//	--from    source folder ID; validated against each file's current parent
//	--name    file name to move from --from (alternative to --file)
//	--to      destination folder ID (required)
//	--apply   actually move (default: dry-run)
//	--json    emit machine-readable JSON
//
// godlike/07 NO-FAKE-AVAILABILITY: dry-run is the default. Moves happen
// only with the explicit --apply opt-in, and every target is validated
// (existence, current parent) before a single File.Update is issued.
// Ambiguity fails closed: a --name that matches more than one file is an
// error, never a silent first-match.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// stringListFlag is a repeatable, comma-aware string flag.
type stringListFlag []string

func (s *stringListFlag) String() string { return strings.Join(*s, ",") }

func (s *stringListFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			*s = append(*s, trimmed)
		}
	}
	return nil
}

// driveMVArgs holds the validated inputs.
type driveMVArgs struct {
	Files []string
	From  string
	Name  string
	To    string
}

// parseDriveMVArgs normalises and validates the raw CLI inputs. Pure so
// it can be unit-tested without Drive.
func parseDriveMVArgs(files []string, from, name, to string) (driveMVArgs, error) {
	a := driveMVArgs{
		From: strings.TrimSpace(from),
		Name: strings.TrimSpace(name),
		To:   strings.TrimSpace(to),
	}
	for _, f := range files {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			a.Files = append(a.Files, trimmed)
		}
	}

	switch {
	case a.To == "":
		return a, fmt.Errorf("drive-mv: --to is required")
	case a.Name != "" && len(a.Files) > 0:
		return a, fmt.Errorf("drive-mv: use either --file or --name, not both")
	case a.Name != "" && a.From == "":
		return a, fmt.Errorf("drive-mv: --name requires --from")
	case a.Name == "" && len(a.Files) == 0:
		return a, fmt.Errorf("drive-mv: provide at least one --file, or --from + --name")
	case a.From != "" && a.From == a.To:
		return a, fmt.Errorf("drive-mv: --from and --to are the same folder (%s)", a.To)
	}
	return a, nil
}

// moveTarget is the per-file outcome of a drive-mv run.
type moveTarget struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	From   string `json:"from,omitempty"`
	Status string `json:"status"` // "moved" | "planned" | "skipped" | "error"
	Error  string `json:"error,omitempty"`
}

type driveMVReport struct {
	Destination string       `json:"destination"`
	Applied     bool         `json:"applied"`
	Moved       int          `json:"moved"`
	Skipped     int          `json:"skipped"`
	Planned     int          `json:"planned"`
	Errors      int          `json:"errors"`
	Results     []moveTarget `json:"results"`
}

func RunDriveMV(args []string) error {
	fs := flag.NewFlagSet("drive-mv", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var files stringListFlag
	fs.Var(&files, "file", "Drive file ID to move (repeatable / comma-separated)")
	from := fs.String("from", "", "source folder ID (validated against each file's current parent)")
	name := fs.String("name", "", "file name to move from --from (alternative to --file)")
	to := fs.String("to", "", "destination folder ID (required)")
	apply := fs.Bool("apply", false, "actually move files (default: dry-run)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	parsed, err := parseDriveMVArgs(files, *from, *name, *to)
	if err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	admin, err := cli.BuildDriveAdminForCLI(cli.CmdContext(), cfg, log)
	if err != nil {
		return fmt.Errorf("drive-mv: build Drive admin: %w", err)
	}

	ctx, cancel := context.WithTimeout(cli.CmdContext(), 10*time.Minute)
	defer cancel()

	ids := append([]string(nil), parsed.Files...)
	if parsed.Name != "" {
		lookup, lerr := admin.FindFileByName(ctx, parsed.From, parsed.Name)
		if lerr != nil {
			return fmt.Errorf("drive-mv: lookup %q under %s: %w", parsed.Name, parsed.From, lerr)
		}
		switch len(lookup.Matches) {
		case 0:
			return fmt.Errorf("drive-mv: no file named %q in folder %s", parsed.Name, parsed.From)
		case 1:
			ids = append(ids, lookup.Matches[0].FileID)
		default:
			return fmt.Errorf("drive-mv: %d files named %q in folder %s (ambiguous; move by --file ID instead)",
				len(lookup.Matches), parsed.Name, parsed.From)
		}
	}

	report := driveMVReport{Destination: parsed.To, Applied: *apply, Results: make([]moveTarget, 0, len(ids))}
	seen := make(map[string]struct{})
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		res := moveTarget{ID: id}
		meta, merr := admin.GetFileMeta(ctx, id)
		if merr != nil {
			res.Status = "error"
			res.Error = merr.Error()
			report.Results = append(report.Results, res)
			continue
		}
		res.Name = meta.Name
		currentParent := ""
		if len(meta.Parents) > 0 {
			currentParent = meta.Parents[0]
		}
		res.From = currentParent

		switch {
		case currentParent != "" && currentParent == parsed.To:
			res.Status = "skipped"
			res.Error = "already in destination folder"
		case parsed.From != "" && currentParent != parsed.From:
			res.Status = "error"
			res.Error = fmt.Sprintf("current parent %q does not match --from %q (fail-closed)", currentParent, parsed.From)
		case !*apply:
			res.Status = "planned"
		default:
			if mverr := admin.MoveFile(ctx, id, currentParent, parsed.To); mverr != nil {
				res.Status = "error"
				res.Error = mverr.Error()
			} else {
				res.Status = "moved"
			}
		}

		switch res.Status {
		case "moved":
			report.Moved++
		case "skipped":
			report.Skipped++
		case "planned":
			report.Planned++
		case "error":
			report.Errors++
		}
		report.Results = append(report.Results, res)
	}

	if *jsonOut {
		printDriveMVJSON(report)
	} else {
		printDriveMVText(report)
	}

	if report.Errors > 0 {
		return fmt.Errorf("drive-mv: %d file(s) could not be moved", report.Errors)
	}
	return nil
}

func printDriveMVText(report driveMVReport) {
	mode := "DRY RUN"
	if report.Applied {
		mode = "APPLY"
	}
	fmt.Printf("Drive MV (%s) — destination %s\n\n", mode, report.Destination)
	for _, r := range report.Results {
		label := r.Name
		if label == "" {
			label = r.ID
		}
		switch r.Status {
		case "moved":
			fmt.Printf("  ✅ moved   %s (%s)  %s → %s\n", label, r.ID, r.From, report.Destination)
		case "planned":
			fmt.Printf("  ➕ plan    %s (%s)  %s → %s\n", label, r.ID, r.From, report.Destination)
		case "skipped":
			fmt.Printf("  ⏭  skip   %s (%s)  %s\n", label, r.ID, r.Error)
		case "error":
			fmt.Printf("  ❌ error  %s (%s)  %s\n", label, r.ID, r.Error)
		}
	}
	fmt.Printf("\nSummary: moved=%d planned=%d skipped=%d errors=%d\n",
		report.Moved, report.Planned, report.Skipped, report.Errors)
	if !report.Applied && report.Planned > 0 {
		fmt.Println("Pass --apply to execute the moves.")
	}
}

func printDriveMVJSON(report driveMVReport) {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-mv: marshal JSON: %v\n", err)
		return
	}
	fmt.Println(string(b))
}
