package audit

import (
	"fmt"
)

// repairAuditReport is the canonical JSON audit output produced by
// the repair-drive-links command when --audit is set.
type repairAuditReport struct {
	JobID               string `json:"job_id"`
	ExecutedAt          string `json:"executed_at"`
	RemoveInvalid       bool   `json:"remove_invalid"`
	AssetsReferenced    int    `json:"assets_referenced"`
	Verified            int    `json:"verified"`
	Updated             int    `json:"updated"`
	Missing             int    `json:"missing"`
	Trashed             int    `json:"trashed"`
	Inaccessible        int    `json:"inaccessible"`
	Malformed           int    `json:"malformed"`
	Orphans             int    `json:"orphans"`
	BrokenLocations     int    `json:"broken_locations"`
	Duplicates          int    `json:"duplicates"`
	TransportErrors     int    `json:"transport_errors"`
	QdrantMismatches    int    `json:"qdrant_mismatches"`
	QdrantEventsEmitted int    `json:"qdrant_events_emitted"`
	SpecSceneRepaired   bool   `json:"specscene_repaired"`
	SQLiteUpdated       bool   `json:"sqlite_updated"`
	// NoOp is derived from the report and records the formal replay
	// contract: the run observed the job but performed no mutation.
	NoOp     bool                `json:"no_op"`
	Warnings []string            `json:"warnings,omitempty"`
	Details  []repairAssetDetail `json:"details,omitempty"`
}

// repairAssetDetail carries per-link diagnostic information for the
// --audit JSON report.
type repairAssetDetail struct {
	ItemIdx   int    `json:"item_idx"`
	SceneID   string `json:"scene_id"`
	Label     string `json:"label"`
	AssetID   string `json:"asset_id"`
	FileID    string `json:"file_id"`
	Link      string `json:"link"`
	State     string `json:"state"`
	ErrorCode string `json:"error_code,omitempty"`
	Action    string `json:"action"` // "preserved", "updated", "cleared", "error"
}

// repairReportIsNoOp is the formal second-run contract. Read-only
// observations such as AssetsReferenced, Verified, and Details do not
// make a replay non-idempotent. A requested mutation that was not
// successfully persisted is still a failure of the contract, so the
// predicate remains fail-closed for remove-invalid runs.
func repairReportIsNoOp(r repairAuditReport) bool {
	if r.TransportErrors > 0 || r.QdrantMismatches > 0 || len(r.Warnings) > 0 {
		return false
	}
	if r.SpecSceneRepaired || r.SQLiteUpdated || r.QdrantEventsEmitted > 0 {
		return false
	}
	if r.RemoveInvalid && (r.Updated > 0 || r.Missing > 0 || r.Trashed > 0 ||
		r.Inaccessible > 0 || r.Malformed > 0 || r.Orphans > 0 ||
		r.BrokenLocations > 0 || r.Duplicates > 0) {
		return false
	}
	return true
}

// printRepairSummary prints the human-readable repair summary to stdout.
func printRepairSummary(r *repairAuditReport) {
	fmt.Println("=== Repair Drive Links ===")
	fmt.Printf("Job ID:         %s\n", r.JobID)
	fmt.Printf("Executed at:    %s\n", r.ExecutedAt)
	fmt.Printf("Remove:         %v\n", r.RemoveInvalid)
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("Summary")
	fmt.Println("──────────────────────────────────────────────")
	fmt.Printf("  Assets referenced:      %d\n", r.AssetsReferenced)
	fmt.Printf("  Verified:               %d\n", r.Verified)
	fmt.Printf("  Updated:                %d\n", r.Updated)
	fmt.Printf("  Missing:                %d\n", r.Missing)
	fmt.Printf("  Trashed:                %d\n", r.Trashed)
	fmt.Printf("  Inaccessible:           %d\n", r.Inaccessible)
	fmt.Printf("  Malformed:              %d\n", r.Malformed)
	fmt.Printf("  Orphans:                %d\n", r.Orphans)
	fmt.Printf("  Broken locations:       %d\n", r.BrokenLocations)
	fmt.Printf("  Duplicates:             %d\n", r.Duplicates)
	fmt.Printf("  Transport errors:       %d\n", r.TransportErrors)
	fmt.Printf("  Qdrant mismatches:      %d\n", r.QdrantMismatches)
	fmt.Printf("  Qdrant events emitted:   %d\n", r.QdrantEventsEmitted)
	fmt.Printf("  SpecScene repaired:     %v\n", r.SpecSceneRepaired)
	fmt.Printf("  SQLite updated:         %v\n", r.SQLiteUpdated)
	if len(r.Warnings) > 0 {
		fmt.Println("\n  Warnings:")
		for _, w := range r.Warnings {
			fmt.Printf("    - %s\n", w)
		}
	}
	if r.TransportErrors > 0 {
		fmt.Printf("\n  Final status: COMPLETED_WITH_WARNINGS\n")
	} else {
		fmt.Printf("\n  Final status: COMPLETED\n")
	}
}
