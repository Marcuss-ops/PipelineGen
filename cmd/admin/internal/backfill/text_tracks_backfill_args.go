// cmd/admin/text_tracks_backfill_args.go — pure, testable flag
// parsing for the text-tracks-backfill CLI (Fase 5, July 2026).
// Extracted from text_tracks_backfill.go; no behavior change.
package backfill

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// parseTextTracksBackfillArgs is the pure, testable flag parser.
func parseTextTracksBackfillArgs(args []string) (textTracksBackfillDeps, error) {
	deps := textTracksBackfillDeps{
		Progress: 50,
		TextKind: "transcript",
	}
	for _, a := range args {
		a = strings.TrimSpace(a)
		switch {
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

// splitCSV splits a comma-separated list into trimmed non-empty values.
func SplitCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

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

// isKnownTextTrackKind is duplicated from jobs.go to keep the
// CLI self-contained (the application-layer
// texttracks.isKnownTextTrackKind is unexported). The set
// MUST match the canonical list in jobs.go::isKnownTextTrackKind.
func isKnownTextTrackKind(k detail.TextTrackKind) bool {
	switch k {
	case detail.TextTrackTranscript,
		detail.TextTrackDescription,
		detail.TextTrackSummary,
		detail.TextTrackTitle,
		detail.TextTrackKeywords:
		return true
	}
	return false
}
