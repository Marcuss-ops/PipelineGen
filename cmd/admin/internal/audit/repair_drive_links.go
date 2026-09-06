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
package audit

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

func RunRepairDriveLinks(args []string) error {
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

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
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
	ctx := cli.CmdContext()

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
		report := repairAuditReport{
			JobID:         *jobID,
			ExecutedAt:    time.Now().UTC().Format(time.RFC3339),
			RemoveInvalid: *removeInvalid,
			RefreshDocs:   *refreshDocs,
		}
		report.NoOp = repairReportIsNoOp(report)
		if *audit {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				return fmt.Errorf("failed to encode no-op audit report: %w", err)
			}
		} else {
			fmt.Println("No Drive links found. Nothing to repair.")
		}
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
		locationChanges     = make(map[string]scriptpkg.AssetLocationChange)
		locationConflictErr error
	)

	recordLocationChange := func(assetID, fileID, link string) {
		if !*removeInvalid || strings.TrimSpace(assetID) == "" || strings.HasPrefix(strings.TrimSpace(assetID), "voiceover:") {
			return
		}
		change := scriptpkg.AssetLocationChange{
			AssetID: strings.TrimSpace(assetID), DriveFileID: strings.TrimSpace(fileID), DriveLink: strings.TrimSpace(link),
		}
		if previous, exists := locationChanges[change.AssetID]; exists && previous != change {
			if locationConflictErr == nil {
				locationConflictErr = fmt.Errorf("conflicting durable location changes for asset %q", change.AssetID)
			}
			return
		}
		locationChanges[change.AssetID] = change
	}

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
				recordLocationChange(ref.assetID, verified.DriveFileID, verified.DriveLink)
			}

		case scriptpkg.LocationStateMissing:
			report.Missing++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateTrashed:
			report.Trashed++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateInaccessible:
			report.Inaccessible++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateMalformed:
			report.Malformed++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateOrphanDriveFile:
			report.Orphans++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateBrokenAssetLocation:
			report.BrokenLocations++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}

		case scriptpkg.LocationStateDuplicate:
			report.Duplicates++
			detail.Action = "cleared"
			if *removeInvalid {
				*ref.linkPtr = ""
				report.SpecSceneRepaired = true
				recordLocationChange(ref.assetID, verified.DriveFileID, "")
			}
		}

		report.Details = append(report.Details, detail)
	}

	report.AssetsReferenced = len(links)
	if locationConflictErr != nil {
		return fmt.Errorf("repair refused conflicting durable location changes: %w", locationConflictErr)
	}

	// ── Step 5 + 6: Persist asset locations, outbox, and SpecScene atomically ──
	if *removeInvalid && report.SpecSceneRepaired {
		if root.Outbox == nil || root.Outbox.EventsRepo == nil {
			return fmt.Errorf("repair requires the canonical SQLite/outbox committer when --remove-invalid is set")
		}
		changes := make([]scriptpkg.AssetLocationChange, 0, len(locationChanges))
		for _, change := range locationChanges {
			changes = append(changes, change)
		}
		mutator, ok := root.CanonicalAssetWriter.(persistence.AssetMutator)
		if !ok || mutator == nil {
			return fmt.Errorf("repair requires the canonical asset mutator location port")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("repair durable transaction begin failed: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				// A failed rollback would leave the durable repair transaction
				// dangling; surface it instead of silently moving on.
				if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
					log.Warn("repair drive links: rollback after failed durable update", zap.Error(rbErr))
				}
			}
		}()
		patches := make([]persistence.DriveLocationPatch, 0, len(changes))
		for _, change := range changes {
			patches = append(patches, persistence.DriveLocationPatch{
				AssetID:     change.AssetID,
				DriveFileID: change.DriveFileID,
				DriveLink:   change.DriveLink,
			})
		}
		if err := mutator.ReconcileDriveLocationsTx(ctx, tx, patches); err != nil {
			return fmt.Errorf("repair durable location commit failed: %w", err)
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("failed to marshal reconciled envelope: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE jobs SET result_json = ? WHERE id = ?",
			string(raw), *jobID); err != nil {
			return fmt.Errorf("failed to update result_json: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("repair durable transaction commit failed: %w", err)
		}
		committed = true
		report.SQLiteUpdated = len(changes) > 0
		// Projection processing remains asynchronous and is not claimed here.
		report.QdrantEventsEmitted = len(changes)
	}

	// ── Step 7: Refresh Google Docs ───────────────────────────
	if *refreshDocs && report.SpecSceneRepaired {
		if root.Drive == nil || root.Drive.DocClient == nil {
			return fmt.Errorf("repair requested Google Doc refresh but the document client is unavailable")
		}
		refreshed := 0
		expectedDocs := 0
		for i := range envelope.Items {
			item := &envelope.Items[i]
			if item.Result == nil || item.Result.Provenance == nil {
				continue
			}
			docID := strings.TrimSpace(item.Result.Provenance.DocID)
			if docID == "" {
				return fmt.Errorf("repair requested Google Doc refresh but item %d has no provenance document ID", i)
			}
			expectedDocs++
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
		if expectedDocs == 0 {
			return fmt.Errorf("repair requested Google Doc refresh but no provenance documents were found")
		}
		if refreshed != expectedDocs {
			return fmt.Errorf("Google Doc refresh incomplete: refreshed %d of %d", refreshed, expectedDocs)
		}
		report.DocumentsRefreshed = refreshed
	}

	// ── Output ────────────────────────────────────────────────
	report.NoOp = repairReportIsNoOp(report)
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
