// cmd/admin/repair_drive_links.go — repair broken Drive links in
// a completed script.generate job with deep audit and reconciliation.
//
// Reads the result_json from the jobs table, extracts every
// SpecScene across all items, verifies every drive_link via the
// canonical AssetLocationVerifier, and produces a detailed audit
// report. With --remove-invalid, cleared/updated links are persisted
// to SQLite (media_assets), SpecScene, and Manifest.
//
// Google Docs was retired (2026-10-25): the --refresh-docs leg and every
// DocClient dependency are gone with the capability.
//
// Usage:
//
//	admin repair-drive-links --job-id job_xxx
//	admin repair-drive-links --job-id job_xxx --audit > report.json
//	admin repair-drive-links --job-id job_xxx --remove-invalid
package audit

import (
	"context"
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
	opts, err := parseRepairDriveLinkOptions(args)
	if err != nil {
		return err
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

	resultJSON, err := loadRepairJobResult(ctx, db, opts.jobID)
	if err != nil {
		return err
	}

	var envelope scriptpkg.GenerationEnvelopeResult
	if err := json.Unmarshal([]byte(resultJSON), &envelope); err != nil {
		return fmt.Errorf("failed to parse result_json as GenerationEnvelopeResult: %w", err)
	}

	links := collectDriveLinkRefs(&envelope)
	if len(links) == 0 {
		report := newRepairAuditReport(opts)
		if opts.audit {
			if err := encodeRepairAuditReport(&report); err != nil {
				return err
			}
		} else {
			fmt.Println("No Drive links found. Nothing to repair.")
		}
		return nil
	}

	report := newRepairAuditReport(opts)
	locationChanges := make(map[string]scriptpkg.AssetLocationChange)
	locationConflictErr := verifyDriveLinkRefs(ctx, verifier, links, opts.removeInvalid, &report, locationChanges)
	report.AssetsReferenced = len(links)
	if locationConflictErr != nil {
		return fmt.Errorf("repair refused conflicting durable location changes: %w", locationConflictErr)
	}

	// ── Step 5 + 6: Persist asset locations, outbox, and SpecScene atomically ──
	if opts.removeInvalid && report.SpecSceneRepaired {
		if err := persistDriveLinkRepairs(ctx, db, log, root, opts.jobID, &envelope, locationChanges); err != nil {
			return err
		}
		report.SQLiteUpdated = len(locationChanges) > 0
		// Projection processing remains asynchronous and is not claimed here.
		report.IndexEventsEmitted = len(locationChanges)
	}

	// ── Output ────────────────────────────────────────────────
	if opts.audit {
		if err := encodeRepairAuditReport(&report); err != nil {
			return err
		}
	} else {
		printRepairSummary(&report)
	}

	if report.TransportErrors > 0 {
		log.Warn("repair completed with transport errors", zap.Int("count", report.TransportErrors))
	}
	return nil
}

// repairDriveLinkOptions is the parsed CLI surface. Parsing is separated from
// the repair so the required-flag check is exercised without a composition root.
type repairDriveLinkOptions struct {
	jobID         string
	removeInvalid bool
	audit         bool
}

func parseRepairDriveLinkOptions(args []string) (repairDriveLinkOptions, error) {
	fs := flag.NewFlagSet("repair-drive-links", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job-id", "", "Job ID to repair (required)")
	removeInvalid := fs.Bool("remove-invalid", false, "Clear broken links, update SQLite, and persist reconciled SpecScene")
	audit := fs.Bool("audit", false, "Output detailed JSON audit report to stdout")
	if err := fs.Parse(args); err != nil {
		return repairDriveLinkOptions{}, err
	}
	if strings.TrimSpace(*jobID) == "" {
		return repairDriveLinkOptions{}, fmt.Errorf("--job-id is required")
	}
	return repairDriveLinkOptions{jobID: *jobID, removeInvalid: *removeInvalid, audit: *audit}, nil
}

func newRepairAuditReport(opts repairDriveLinkOptions) repairAuditReport {
	return repairAuditReport{
		JobID:         opts.jobID,
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339),
		RemoveInvalid: opts.removeInvalid,
	}
}

func encodeRepairAuditReport(report *repairAuditReport) error {
	report.NoOp = repairReportIsNoOp(*report)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("failed to encode audit report: %w", err)
	}
	return nil
}

func loadRepairJobResult(ctx context.Context, db *sql.DB, jobID string) (string, error) {
	var (
		resultJSON string
		status     string
	)
	err := db.QueryRowContext(ctx,
		"SELECT status, COALESCE(result_json, '{}') FROM jobs WHERE id = ?",
		jobID,
	).Scan(&status, &resultJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("job not found: %s", jobID)
		}
		return "", fmt.Errorf("failed to read job: %w", err)
	}
	if resultJSON == "" || resultJSON == "{}" {
		return "", fmt.Errorf("job %s has no result_json", jobID)
	}
	return resultJSON, nil
}

// driveLinkRef is one (asset, scene, binding) link occurrence. linkPtr aliases
// the parsed envelope field, so clearing it in place is what the final marshal
// persists — no second pass over the tree is needed.
type driveLinkRef struct {
	itemIdx int
	sceneID string
	label   string
	assetID string
	fileID  string
	link    string
	linkPtr *string
}

// collectDriveLinkRefs walks every scene binding that carries a Drive link.
// Blank links are not tracked: there is nothing to verify or repair.
func collectDriveLinkRefs(envelope *scriptpkg.GenerationEnvelopeResult) []driveLinkRef {
	var links []driveLinkRef
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
					links = append(links, driveLinkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "clip", assetID: bindings.Clip.ClipID,
						link: l, linkPtr: &bindings.Clip.DriveLink,
					})
				}
				if l := strings.TrimSpace(bindings.Clip.SubtitleLink); l != "" {
					links = append(links, driveLinkRef{
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
					links = append(links, driveLinkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "stock", assetID: bindings.Stock.AssetID,
						link: l, linkPtr: &bindings.Stock.DriveLink,
					})
				}
			}
			if bindings.Voiceover != nil {
				if l := strings.TrimSpace(bindings.Voiceover.Link); l != "" {
					links = append(links, driveLinkRef{
						itemIdx: i, sceneID: scene.ID,
						label: "voiceover", assetID: "voiceover:" + scene.ID,
						link: l, linkPtr: &bindings.Voiceover.Link,
					})
				}
			}
			for k := range bindings.Media {
				if l := strings.TrimSpace(bindings.Media[k].DriveLink); l != "" {
					links = append(links, driveLinkRef{
						itemIdx: i, sceneID: scene.ID,
						label:   fmt.Sprintf("media[%d]", k),
						assetID: bindings.Media[k].AssetID,
						link:    l, linkPtr: &bindings.Media[k].DriveLink,
					})
				}
			}
		}
	}
	return links
}

// driveLinkStateOutcome is the per-verification-state reaction: which counter
// moves, what the detail row reports, and whether the stored link is adopted
// (UPDATED points at the canonical link) or cleared. A state absent from the
// table leaves the counters alone and keeps the row for the audit, exactly like
// the switch default it replaces.
type driveLinkStateOutcome struct {
	action string
	adopt  bool
	clear  bool
	count  func(*repairAuditReport)
}

var driveLinkStateOutcomes = map[scriptpkg.LocationState]driveLinkStateOutcome{
	scriptpkg.LocationStateVerified: {action: "preserved", count: func(r *repairAuditReport) { r.Verified++ }},
	scriptpkg.LocationStateUpdated: {action: "updated", adopt: true, count: func(r *repairAuditReport) {
		r.Updated++
		r.IndexMismatches++ // stored link ≠ canonical → index may be stale
	}},
	scriptpkg.LocationStateMissing:             {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Missing++ }},
	scriptpkg.LocationStateTrashed:             {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Trashed++ }},
	scriptpkg.LocationStateInaccessible:        {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Inaccessible++ }},
	scriptpkg.LocationStateMalformed:           {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Malformed++ }},
	scriptpkg.LocationStateOrphanDriveFile:     {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Orphans++ }},
	scriptpkg.LocationStateBrokenAssetLocation: {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.BrokenLocations++ }},
	scriptpkg.LocationStateDuplicate:           {action: "cleared", clear: true, count: func(r *repairAuditReport) { r.Duplicates++ }},
}

// applyDriveLinkOutcome mutates the parsed envelope only under --remove-invalid.
// Without it the report is a pure audit: nothing is cleared or adopted, which is
// what makes the dry run safe.
func applyDriveLinkOutcome(ref driveLinkRef, verified *scriptpkg.VerifiedLocation, removeInvalid bool, report *repairAuditReport, recordChange func(assetID, fileID, link string)) {
	outcome, ok := driveLinkStateOutcomes[verified.State]
	if !ok {
		return
	}
	outcome.count(report)
	if !removeInvalid {
		return
	}
	switch {
	case outcome.adopt:
		*ref.linkPtr = verified.DriveLink
		report.SpecSceneRepaired = true
		recordChange(ref.assetID, verified.DriveFileID, verified.DriveLink)
	case outcome.clear:
		*ref.linkPtr = ""
		report.SpecSceneRepaired = true
		recordChange(ref.assetID, verified.DriveFileID, "")
	}
}

// verifyDriveLinkRefs verifies every link and folds the outcome into report. It
// returns the first conflicting durable location change, if any; a transport
// error is a warning, not a failure, and preserves the link.
func verifyDriveLinkRefs(ctx context.Context, verifier scriptpkg.AssetLocationVerifier, links []driveLinkRef, removeInvalid bool, report *repairAuditReport, locationChanges map[string]scriptpkg.AssetLocationChange) error {
	var locationConflictErr error

	recordLocationChange := func(assetID, fileID, link string) {
		if !removeInvalid || strings.TrimSpace(assetID) == "" || strings.HasPrefix(strings.TrimSpace(assetID), "voiceover:") {
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
		if outcome, ok := driveLinkStateOutcomes[verified.State]; ok {
			detail.Action = outcome.action
		}
		applyDriveLinkOutcome(ref, verified, removeInvalid, report, recordLocationChange)
		report.Details = append(report.Details, detail)
	}
	return locationConflictErr
}

// persistDriveLinkRepairs writes the reconciled locations, the rewritten
// envelope and the outbox events in ONE transaction: a partial repair would
// leave the ledger and result_json disagreeing about which links exist.
func persistDriveLinkRepairs(ctx context.Context, db *sql.DB, log *zap.Logger, root *wiring.ComposeRoot, jobID string, envelope *scriptpkg.GenerationEnvelopeResult, locationChanges map[string]scriptpkg.AssetLocationChange) error {
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
		string(raw), jobID); err != nil {
		return fmt.Errorf("failed to update result_json: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("repair durable transaction commit failed: %w", err)
	}
	committed = true
	return nil
}
