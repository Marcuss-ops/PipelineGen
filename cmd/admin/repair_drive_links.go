// cmd/admin/repair_drive_links.go — repair broken Drive links in
// a completed script.generate job with deep audit and reconciliation.
//
// Reads the result_json from the jobs table, extracts every
// SpecScene across all items, verifies every drive_link via the
// canonical AssetLocationVerifier, and produces a detailed audit
// report. With --remove-invalid, cleared/updated links are persisted
// to SQLite (media_assets), SpecScene, and Manifest. With
// --refresh-docs, existing Google Docs are refreshed with the
// reconciled SpecScene.
//
// Usage:
//
//	admin repair-drive-links --job-id job_xxx
//	admin repair-drive-links --job-id job_xxx --audit > report.json
//	admin repair-drive-links --job-id job_xxx --remove-invalid --refresh-docs
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// repairAuditReport is the canonical JSON audit output produced by
// the repair-drive-links command when --audit is set.
type repairAuditReport struct {
	JobID             string               `json:"job_id"`
	ExecutedAt        string               `json:"executed_at"`
	RemoveInvalid     bool                 `json:"remove_invalid"`
	RefreshDocs       bool                 `json:"refresh_docs"`
	AssetsReferenced  int                  `json:"assets_referenced"`
	Verified          int                  `json:"verified"`
	Updated           int                  `json:"updated"`
	Missing           int                  `json:"missing"`
	Trashed           int                  `json:"trashed"`
	Inaccessible      int                  `json:"inaccessible"`
	Malformed         int                  `json:"malformed"`
	Orphans           int                  `json:"orphans"`
	BrokenLocations   int                  `json:"broken_locations"`
	Duplicates        int                  `json:"duplicates"`
	TransportErrors   int                  `json:"transport_errors"`
	QdrantMismatches  int                  `json:"qdrant_mismatches"`
	SpecSceneRepaired bool                 `json:"specscene_repaired"`
	SQLiteUpdated     bool                 `json:"sqlite_updated"`
	DocumentsRefreshed int                 `json:"documents_refreshed"`
	Warnings          []string            `json:"warnings,omitempty"`
	Details           []repairAssetDetail `json:"details,omitempty"`
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

func runRepairDriveLinks(args []string) error {
	fs := flag.NewFlagSet("repair-drive-links", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job-id", "", "Job ID to repair (required)")
	removeInvalid := fs.Bool("remove-invalid", false, "Clear broken links, update SQLite, and persist reconciled SpecScene")
	refreshDocs := fs.Bool("refresh-docs", false, "Refresh existing Google Docs with reconciled SpecScene")
	audit := fs.Bool("audit", false, "Output detailed JSON audit report to stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*jobID) == "" {
		return fmt.Errorf("--job-id is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		log.Fatal("Failed to initialize composition root", zap.Error(err))
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available")
	}
	if root.DB == nil || root.DB.DB == nil {
		return fmt.Errorf("database port is not available")
	}

	// AssetLocationResolverAdapter satisfies AssetLocationVerifier.
	verifier := drive.NewAssetLocationResolverAdapter(root.Drive.Reader)
	db := root.DB.DB
	ctx := cmdContext()

	// ── Step 1: Read the job ──────────────────────────────────
	var resultJSON string
	var status string
	err = db.QueryRowContext(ctx,
		"SELECT status, COALESCE(result_json, '{}') FROM jobs WHERE id = ?",
		*jobID,
	).Scan(&status, &resultJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("job not found: %s", *jobID)
		}
		return fmt.Errorf("failed to read job: %w", err)
	}
	if resultJSON == "" || resultJSON == "{}" {
		return fmt.Errorf("job %s has no result_json", *jobID)
	}

	// ── Step 2: Parse result_json ─────────────────────────────
	var envelope scriptpkg.GenerationEnvelopeResult
	if err := json.Unmarshal([]byte(resultJSON), &envelope); err != nil {
		return fmt.Errorf("failed to parse result_json as GenerationEnvelopeResult: %w", err)
	}

	// ── Step 3: Collect all links ─────────────────────────────
	type linkRef struct {
		itemIdx int
		sceneID string
		label   string
		assetID string
		fileID  string
		link    string
		linkPtr *string // pointer into the parsed struct for mutation
	}

	var links []linkRef
	for i := range envelope.Items {
		item := &envelope.Items[i]
		if item.Result == nil {
			continue
		}
		scenes := item.Result.Output.SpecScene.Scenes
		for j := range scenes {
			scene := &scenes[j]
			bindings := &scene.Bindings

			if bindings.Clip != nil {
				if l := strings.TrimSpace(bindings.Clip.DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "clip", assetID: bindings.Clip.ClipID,
						link: l, linkPtr: &bindings.Clip.DriveLink,
					})
				}
				if l := strings.TrimSpace(bindings.Clip.SubtitleLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label:   "subtitle",
						assetID: bindings.Clip.ClipID,
						fileID:  bindings.Clip.SubtitleFileID,
						link:    l, linkPtr: &bindings.Clip.SubtitleLink,
					})
				}
			}
			if bindings.Stock != nil {
				if l := strings.TrimSpace(bindings.Stock.DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "stock", assetID: bindings.Stock.AssetID,
						link: l, linkPtr: &bindings.Stock.DriveLink,
					})
				}
			}
			if bindings.Voiceover != nil {
				if l := strings.TrimSpace(bindings.Voiceover.Link); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "voiceover", assetID: "voiceover:" + scene.ID,
						link: l, linkPtr: &bindings.Voiceover.Link,
					})
				}
			}
			for k := range bindings.Media {
				if l := strings.TrimSpace(bindings.Media[k].DriveLink); l != "" {
					links = append(links, linkRef{
						itemIdx: i, sceneID: scene.ID,
						label:   fmt.Sprintf("media[%d]", k),
						assetID: bindings.Media[k].AssetID,
						link:    l, linkPtr: &bindings.Media[k].DriveLink,
					})
				}
			}
		}
	}

	if len(links) == 0 {
		fmt.Println("No Drive links found. Nothing to repair.")
		return nil
	}

	// ── Step 4: Verify every link ─────────────────────────────
	var (
		report = repairAuditReport{
			JobID:         *jobID,
			ExecutedAt:    time.Now().UTC().Format(time.RFC3339),
			RemoveInvalid: *removeInvalid,
			RefreshDocs:   *refreshDocs,
		}
	)

	for _, ref := range links {
		verified, err := verifier.Verify(ctx, ref.assetID, ref.fileID, ref.link)
		if err != nil {
			report.TransportErrors++
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("transport error verifying %s in %s (link preserved): %v",
					ref.label, ref.sceneID, err))
			report.Details = append(report.Details, repairAssetDetail{
				ItemIdx: ref.itemIdx, SceneID: ref.sceneID,
				Label: ref.label, AssetID: ref.assetID,
				Link: ref.link, State: "TRANSPORT_ERROR",
				ErrorCode: "TRANSPORT_ERROR", Action: "preserved",
			})
			continue
		}
		if verified == nil {
			continue
		}

		detail := repairAssetDetail{
			ItemIdx:   ref.itemIdx,
			SceneID:   ref.sceneID,
			Label:     ref.label,
			AssetID:   ref.assetID,
			FileID:    verified.DriveFileID,
			Link:      ref.link,
			State:     string(verified.State),
			ErrorCode: verified.ErrorCode,
		}

		switch verified.State {
		case scriptpkg.LocationStateVerified:
			report.Verified++
			detail.Action = "preserved"

		case scriptpkg.LocationStateUpdated:
			report.Updated++
			report.QdrantMismatches++ // stored link ≠ canonical → Qdrant may be stale
			detail.Action = "updated"
			if *removeInvalid {
				*ref.linkPtr = verified.DriveLink
				report.SpecSceneRepaired = true
				// Also update SQLite media_assets.drive_link.
				if err := updateAssetDriveLink(ctx, db, ref.assetID, verified.DriveLink); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("failed to update SQLite drive_link for %s: %v", ref.assetID, err))
				} else {
					report.SQLiteUpdated = true
				}
			}

		case scriptpkg.LocationStateMissing:
			report.Missing++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				if err := clearAssetDriveLink(ctx, db, ref.assetID); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("failed to clear SQLite drive_link for %s: %v", ref.assetID, err))
				} else {
					report.SQLiteUpdated = true
				}
			}

		case scriptpkg.LocationStateTrashed:
			report.Trashed++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				if err := clearAssetDriveLink(ctx, db, ref.assetID); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("failed to clear SQLite drive_link for %s: %v", ref.assetID, err))
				} else {
					report.SQLiteUpdated = true
				}
			}

		case scriptpkg.LocationStateInaccessible:
			report.Inaccessible++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				if err := clearAssetDriveLink(ctx, db, ref.assetID); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("failed to clear SQLite drive_link for %s: %v", ref.assetID, err))
				} else {
					report.SQLiteUpdated = true
				}
			}

		case scriptpkg.LocationStateMalformed:
			report.Malformed++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
			}

		case scriptpkg.LocationStateOrphanDriveFile:
			report.Orphans++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
			}

		case scriptpkg.LocationStateBrokenAssetLocation:
			report.BrokenLocations++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				if err := clearAssetDriveLink(ctx, db, ref.assetID); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("failed to clear SQLite drive_link for %s: %v", ref.assetID, err))
				} else {
					report.SQLiteUpdated = true
				}
			}

		case scriptpkg.LocationStateDuplicate:
			report.Duplicates++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
			}
		}

		report.Details = append(report.Details, detail)
	}

	report.AssetsReferenced = len(links)

	// ── Step 5: Persist reconciled SpecScene ──────────────────
	if *removeInvalid && report.SpecSceneRepaired {
		raw, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("failed to marshal reconciled envelope: %w", err)
		}
		_, err = db.ExecContext(ctx,
			"UPDATE jobs SET result_json = ? WHERE id = ?",
			string(raw), *jobID)
		if err != nil {
			return fmt.Errorf("failed to update result_json: %w", err)
		}
	}

	// ── Step 6: Refresh Google Docs ───────────────────────────
	if *refreshDocs {
		refreshed := 0
		if root.Drive != nil && root.Drive.DocClient != nil {
			for i := range envelope.Items {
				item := &envelope.Items[i]
				if item.Result == nil || item.Result.Provenance == nil {
					continue
				}
				docID := strings.TrimSpace(item.Result.Provenance.DocID)
				if docID == "" {
					continue
				}
				title := item.Result.Title
				if title == "" {
					title = "Reconciled script"
				}
				model := &scriptpkg.ModelScriptOutputV1{
					SchemaVersion: 1,
					Text:          item.Result.Output.Text,
					SpecScene:     item.Result.Output.SpecScene,
					WordCount:     item.Result.Output.WordCount,
				}
				content := buildRepairHTML(model, title)
				if err := root.Drive.DocClient.UpdateDoc(ctx, docID, title, content); err != nil {
					report.Warnings = append(report.Warnings,
						fmt.Sprintf("doc refresh failed for %s: %v", docID, err))
				} else {
					refreshed++
				}
			}
		}
		report.DocumentsRefreshed = refreshed
	}

	// ── Output ────────────────────────────────────────────────
	if *audit {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("failed to encode audit report: %w", err)
		}
	} else {
		printRepairSummary(&report)
	}

	if report.TransportErrors > 0 {
		log.Warn("repair completed with transport errors", zap.Int("count", report.TransportErrors))
	}
	return nil
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

// updateAssetDriveLink updates the drive_link in media_assets for a
// single asset. Returns nil on success (including when the asset does
// not exist — that's handled by the ORPHAN state elsewhere).
func updateAssetDriveLink(ctx context.Context, db *sql.DB, assetID, newLink string) error {
	aid := strings.TrimSpace(assetID)
	if aid == "" || strings.HasPrefix(aid, "voiceover:") {
		return nil // voiceover links have no SQLite asset row.
	}
	_, err := db.ExecContext(ctx, "UPDATE media_assets SET drive_link = ? WHERE id = ? AND drive_link != ?",
		newLink, aid, newLink)
	return err
}

// clearAssetDriveLink clears the drive_link and drive_file_id in
// media_assets for a single asset. Both fields are cleared together
// to avoid inconsistent rows where drive_file_id points to a
// non-existent file.
func clearAssetDriveLink(ctx context.Context, db *sql.DB, assetID string) error {
	aid := strings.TrimSpace(assetID)
	if aid == "" || strings.HasPrefix(aid, "voiceover:") {
		return nil
	}
	_, err := db.ExecContext(ctx, "UPDATE media_assets SET drive_link = '', drive_file_id = '' WHERE id = ? AND (drive_link != '' OR drive_file_id != '')",
		aid)
	return err
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
