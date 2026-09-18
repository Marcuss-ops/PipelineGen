package audit

// broken_references_report.go owns the broken-references report shape and its
// human-readable rendering.
//
// Split out of broken_references.go to stay under the 600-LOC strict cap
// (godlike/08 forward-prevention gate). Symbols moved verbatim: no behaviour
// change, same package.

import "fmt"

// ── Report types ──────────────────────────────────────────────────────

type brokenRefsReport struct {
	SchemaVersion int               `json:"schema_version"`
	Mode          string            `json:"mode"`
	GeneratedAt   string            `json:"generated_at"`
	NoDeletions   bool              `json:"no_deletions_performed"`
	Summary       brokenRefsSummary `json:"summary"`
	FKOrphans     []fkOrphanTable   `json:"fk_orphans"`
	DriveBroken   []brokenDriveRef  `json:"drive_broken"`
	LocalBroken   []brokenLocalRef  `json:"local_broken"`
	QdrantMissing []string          `json:"qdrant_missing"`
	Errors        []string          `json:"errors,omitempty"`
}

type brokenRefsSummary struct {
	FKOrphanRows   int `json:"fk_orphan_rows"`
	FKOrphanTables int `json:"fk_orphan_tables"`
	DriveRefsTotal int `json:"drive_refs_total"`
	DriveBroken    int `json:"drive_broken"`
	LocalRefsTotal int `json:"local_refs_total"`
	LocalBroken    int `json:"local_broken"`
	EligibleAssets int `json:"eligible_assets"`
	QdrantMissing  int `json:"qdrant_missing"`
}

type fkOrphanTable struct {
	Table      string   `json:"table"`
	OwnerTable string   `json:"owner_table"`
	OrphanRows int      `json:"orphan_rows"`
	SampleIDs  []string `json:"sample_ids,omitempty"`
}

type brokenDriveRef struct {
	Table       string `json:"table"`
	Column      string `json:"column"` // drive_file_id or local_path
	RefValue    string `json:"ref_value"`
	AssetID     string `json:"asset_id,omitempty"`
	FailureKind string `json:"failure_kind"` // "drive_file_not_found" | "local_path_not_found"
	Error       string `json:"error,omitempty"`
}

type brokenLocalRef struct {
	Table       string `json:"table"`
	Column      string `json:"column"`
	LocalPath   string `json:"local_path"`
	AssetID     string `json:"asset_id,omitempty"`
	FailureKind string `json:"failure_kind"` // "file_not_found" | "stat_error"
	Error       string `json:"error,omitempty"`
}

// ── Output ────────────────────────────────────────────────────────────

func printBrokenRefsReport(r *brokenRefsReport) {
	fmt.Println("=== Broken References Report (FASE 4) ===")
	fmt.Printf("  mode:         %s\n", r.Mode)
	fmt.Printf("  generated:    %s\n", r.GeneratedAt)
	fmt.Printf("  no deletions: %v\n", r.NoDeletions)
	fmt.Println()

	// FK orphans.
	fmt.Printf("  --- FK Orphans (%d rows across %d tables) ---\n",
		r.Summary.FKOrphanRows, r.Summary.FKOrphanTables)
	for _, o := range r.FKOrphans {
		fmt.Printf("    %-40s → %-25s  orphans=%-6d", o.Table, o.OwnerTable, o.OrphanRows)
		if len(o.SampleIDs) > 0 {
			fmt.Printf("  sample=%v", o.SampleIDs[:min(3, len(o.SampleIDs))])
		}
		fmt.Println()
	}

	// Drive broken.
	if r.Summary.DriveRefsTotal > 0 {
		fmt.Printf("\n  --- Drive References (%d total, %d broken) ---\n",
			r.Summary.DriveRefsTotal, r.Summary.DriveBroken)
		printed := 0
		for _, b := range r.DriveBroken {
			if printed >= 30 {
				fmt.Printf("    ... +%d more broken drive refs\n", len(r.DriveBroken)-printed)
				break
			}
			assetStr := ""
			if b.AssetID != "" {
				assetStr = fmt.Sprintf(" asset=%s", b.AssetID)
			}
			fmt.Printf("    %-35s %s  %s%s\n", b.Table, shortenID(b.RefValue), b.FailureKind, assetStr)
			printed++
		}
	}

	// Local broken.
	if r.Summary.LocalRefsTotal > 0 {
		fmt.Printf("\n  --- Local Path References (%d total, %d broken) ---\n",
			r.Summary.LocalRefsTotal, r.Summary.LocalBroken)
		printed := 0
		for _, b := range r.LocalBroken {
			if printed >= 30 {
				fmt.Printf("    ... +%d more broken local paths\n", len(r.LocalBroken)-printed)
				break
			}
			fmt.Printf("    %-35s %s: %s\n", b.Table, b.FailureKind, truncatePath(b.LocalPath, 70))
			printed++
		}
	}

	// Qdrant missing.
	if r.Summary.QdrantMissing > 0 {
		fmt.Printf("\n  --- Qdrant Points (%d eligible, %d missing in Qdrant) ---\n",
			r.Summary.EligibleAssets, r.Summary.QdrantMissing)
		for i, id := range r.QdrantMissing {
			if i >= 20 {
				fmt.Printf("    ... +%d more missing Qdrant points\n", len(r.QdrantMissing)-i)
				break
			}
			fmt.Printf("    missing: %s\n", id)
		}
	}

	if len(r.Errors) > 0 {
		fmt.Printf("\n  --- Errors (%d) ---\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Printf("    %s\n", e)
		}
	}
}

func shortenID(id string) string {
	if len(id) > 24 {
		return id[:12] + "..." + id[len(id)-12:]
	}
	return id
}

func truncatePath(p string, maxLen int) string {
	if len(p) <= maxLen {
		return p
	}
	return p[:maxLen/2-2] + "..." + p[len(p)-maxLen/2+1:]
}
