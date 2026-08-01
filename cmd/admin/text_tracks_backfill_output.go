// cmd/admin/text_tracks_backfill_output.go — human and machine
// output for the text-tracks-backfill CLI (Fase 5, July 2026).
// Extracted from text_tracks_backfill.go; no behavior change.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// printTextTracksBackfillJSON is the local JSON helper. Named
// to avoid the redeclaration with drive_doctor.go's printJSON
// (which takes a specific doctorReport type).
func printTextTracksBackfillJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Printf("json marshal failed: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

// printHumanTextTracksBackfill prints the human-readable
// summary for the --apply path.
func printHumanTextTracksBackfill(r textTracksBackfillReport) {
	fmt.Println("=== Text-Tracks Backfill Complete ===")
	fmt.Printf("  Source:            %s\n", r.Source)
	fmt.Printf("  Source language:   %s\n", r.SourceLanguage)
	fmt.Printf("  Target languages:  %s\n", strings.Join(r.TargetLanguages, ", "))
	fmt.Printf("  Text kind:         %s\n", r.TextKind)
	fmt.Printf("  Only-missing:      %v\n", r.OnlyMissing)
	fmt.Printf("  Total candidates:  %d\n", r.TotalCandidates)
	fmt.Printf("  Processed:         %d\n", r.Processed)
	fmt.Printf("  Source READY:      %d\n", r.SourceReady)
	fmt.Printf("  Source ACQUIRED:   %d\n", r.SourceAcquired)
	fmt.Printf("  Source missing:    %d\n", r.SourceMissing)
	fmt.Printf("  Skipped (complete):%d\n", r.SkippedOnlyMissing)
	fmt.Printf("  Languages created: %d\n", r.CreatedTotal)
	fmt.Printf("  Languages skipped: %d\n", r.SkippedLangTotal)
	fmt.Printf("  Languages re-tr:   %d\n", r.RetranslatedTotal)
	fmt.Printf("  Languages failed:  %d\n", r.FailedLangTotal)
	fmt.Printf("  Duration:          %dms\n", r.DurationMs)
	if r.Checkpoint != "" {
		fmt.Printf("  Checkpoint:        %s\n", r.Checkpoint)
	}
	if len(r.FailedAssetIDs) > 0 {
		fmt.Printf("  Failed asset IDs (%d):\n", len(r.FailedAssetIDs))
		for _, id := range r.FailedAssetIDs {
			fmt.Printf("    - %s\n", id)
		}
		fmt.Println("  Re-run with --retry-failed --checkpoint=<path> to retry.")
	}
}
