package main

import (
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// repairAuditReport is the canonical JSON audit output produced by
// the repair-drive-links command when --audit is set.
type repairAuditReport struct {
	JobID               string `json:"job_id"`
	ExecutedAt          string `json:"executed_at"`
	RemoveInvalid       bool   `json:"remove_invalid"`
	RefreshDocs         bool   `json:"refresh_docs"`
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
	DocumentsRefreshed  int    `json:"documents_refreshed"`
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
	if r.SpecSceneRepaired || r.SQLiteUpdated || r.QdrantEventsEmitted > 0 || r.DocumentsRefreshed > 0 {
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
	fmt.Printf("Refresh:        %v\n\n", r.RefreshDocs)
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
	fmt.Printf("  Documents refreshed:    %d\n", r.DocumentsRefreshed)
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

// buildRepairHTML renders a minimal SpecScene HTML document for the
// repair refresh. Uses the same structure as
// BuildSpecSceneDocumentHTML but without importing the adapters
// package (keeps the admin CLI free of application-layer deps).
func buildRepairHTML(model *scriptpkg.ModelScriptOutputV1, title string) string {
	if model == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	if strings.TrimSpace(title) != "" {
		b.WriteString("<h1>")
		writeEscaped(&b, strings.TrimSpace(title))
		b.WriteString("</h1>")
	}
	if len(model.SpecScene.Scenes) > 0 {
		b.WriteString("<h2>Scenes</h2>")
		for i := range model.SpecScene.Scenes {
			scene := &model.SpecScene.Scenes[i]
			b.WriteString("<section>")
			b.WriteString("<h3>")
			writeEscaped(&b, scene.ID)
			b.WriteString("</h3>")
			if text := strings.TrimSpace(scene.Text); text != "" {
				b.WriteString("<p>")
				writeEscaped(&b, text)
				b.WriteString("</p>")
			}
			if clip := scene.Bindings.Clip; clip != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				writeLink(&b, clip.DriveLink, clip.ClipTitle, clip.ClipID)
				b.WriteString("</p>")
				if clip.SubtitleLink != "" {
					b.WriteString("<p><strong>Subtitles ASS:</strong> ")
					writeLink(&b, clip.SubtitleLink, clip.SubtitleFileID, clip.SubtitleFileID)
					b.WriteString("</p>")
				}
			}
			if stock := scene.Bindings.Stock; stock != nil {
				b.WriteString("<p><strong>Clip:</strong> ")
				writeLink(&b, stock.DriveLink, stock.Name, stock.AssetID)
				b.WriteString("</p>")
			}
			b.WriteString("</section>")
		}
	}
	raw, err := json.MarshalIndent(model.SpecScene, "", "  ")
	if err == nil {
		b.WriteString("<h2>SpecScene JSON</h2><pre>")
		writeEscaped(&b, string(raw))
		b.WriteString("</pre>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

func writeEscaped(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
}

func writeLink(b *strings.Builder, url, label, fallback string) {
	url = strings.TrimSpace(url)
	if url == "" {
		if fallback == "" {
			b.WriteString("(no link)")
			return
		}
		writeEscaped(b, fallback)
		return
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = url
	}
	b.WriteString("<a href=\"")
	writeEscaped(b, url)
	b.WriteString("\">")
	writeEscaped(b, label)
	b.WriteString("</a>")
}
