// cmd/admin/text_tracks_backfill_args.go — pure, testable flag
// parsing for the text-tracks-backfill CLI (Fase 5, July 2026).
// Extracted from text_tracks_backfill.go; no behavior change.
package backfill

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
)

// parseTextTracksBackfillArgs is the pure, testable flag parser.
func parseTextTracksBackfillArgs(args []string) (textTracksBackfillDeps, error) {
	deps := textTracksBackfillDeps{
		Progress: 50,
		TextKind: "transcript",
	}
	for i := 0; i < len(args); i++ {
		a := strings.TrimSpace(args[i])
		switch {
		case a == "":
			continue
		case a == "--apply":
			deps.Apply = true
		case a == "--dry-run":
			deps.DryRun = true
		case a == "--json":
			deps.JSON = true
		case a == "--only-missing":
			deps.OnlyMissing = true
		case a == "--all":
			deps.OnlyMissing = false
		case a == "--resume":
			deps.Resume = true
		case a == "--retry-failed":
			deps.RetryFailed = true
		// Value flags accept both the documented space form (`--source youtube`)
		// and the `--source=youtube` form. Before this, the help text and the
		// runbook used the space form while the parser only accepted `=`, so a
		// copied command died with `unknown flag: --source`.
		case a == "--source", a == "--languages", a == "--text-kind", a == "--asset-ids", a == "--limit", a == "--progress", a == "--checkpoint":
			if i+1 >= len(args) || strings.HasPrefix(strings.TrimSpace(args[i+1]), "--") {
				return deps, fmt.Errorf("%s requires a value", a)
			}
			i++
			if err := assignTextTracksBackfillValue(&deps, a, args[i]); err != nil {
				return deps, err
			}
		case strings.HasPrefix(a, "--source="):
			deps.Source = strings.TrimPrefix(a, "--source=")
		case strings.HasPrefix(a, "--languages="):
			deps.Languages = strings.TrimPrefix(a, "--languages=")
		case strings.HasPrefix(a, "--text-kind="):
			deps.TextKind = strings.TrimPrefix(a, "--text-kind=")
		case strings.HasPrefix(a, "--asset-ids="):
			deps.AssetIDs = strings.TrimPrefix(a, "--asset-ids=")
		case strings.HasPrefix(a, "--limit="):
			n, err := cli.ParsePositiveFlag(a, "--limit")
			if err != nil {
				return deps, err
			}
			deps.Limit = n
		case strings.HasPrefix(a, "--progress="):
			n, err := cli.ParsePositiveFlag(a, "--progress")
			if err != nil {
				return deps, err
			}
			deps.Progress = n
		case strings.HasPrefix(a, "--checkpoint="):
			deps.Checkpoint = strings.TrimPrefix(a, "--checkpoint=")
		default:
			if strings.HasPrefix(a, "-") {
				return deps, fmt.Errorf("unknown flag: %s", a)
			}
		}
	}
	if deps.Source == "" {
		return deps, fmt.Errorf("--source is required (e.g. --source=youtube)")
	}
	if deps.Languages == "" {
		return deps, fmt.Errorf("--languages is required (e.g. --languages=it,en,es,pt-BR,fr,de; first entry is the source language)")
	}
	if deps.Apply && deps.DryRun {
		return deps, fmt.Errorf("--apply and --dry-run are mutually exclusive")
	}
	if deps.Resume && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--resume requires --checkpoint=<path>")
	}
	if deps.RetryFailed && deps.Checkpoint == "" {
		return deps, fmt.Errorf("--retry-failed requires --checkpoint=<path>")
	}
	if deps.Progress <= 0 {
		deps.Progress = 50
	}
	return deps, nil
}

// assignTextTracksBackfillValue applies one value flag parsed in the
// space-separated form. It shares the numeric validation with the `=` form so
// the two spellings cannot drift.
func assignTextTracksBackfillValue(deps *textTracksBackfillDeps, flag, raw string) error {
	value := strings.TrimSpace(raw)
	switch flag {
	case "--source":
		deps.Source = value
	case "--languages":
		deps.Languages = value
	case "--text-kind":
		deps.TextKind = value
	case "--asset-ids":
		deps.AssetIDs = value
	case "--checkpoint":
		deps.Checkpoint = value
	case "--limit":
		n, err := cli.ParsePositiveFlag(value, flag)
		if err != nil {
			return err
		}
		deps.Limit = n
	case "--progress":
		n, err := cli.ParsePositiveFlag(value, flag)
		if err != nil {
			return err
		}
		deps.Progress = n
	default:
		return fmt.Errorf("unknown flag: %s", flag)
	}
	return nil
}

// splitCSV splits a comma-separated list into trimmed non-empty values.

// splitLanguages splits the --languages CSV into (source, targets).
// The first entry is the source language; the rest are targets.
func splitLanguages(csv string) (string, []string, error) {
	parts := strings.Split(csv, ",")
	source := strings.TrimSpace(parts[0])
	if source == "" {
		return "", nil, fmt.Errorf("--languages: first entry is the source language and must be non-empty")
	}
	targets := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		targets = append(targets, t)
	}
	return source, targets, nil
}

// The text-kind allow-list lives in the materialize pipeline
// (texttracks.IsKnownTextTrackKind). This CLI used to carry a copy that its own
// comment required to stay aligned with jobs.go by hand; the caller now consults
// the owner directly, so an added kind cannot reach one of them and not the
// other.
