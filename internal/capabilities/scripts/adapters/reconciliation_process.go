// Package adapters — reconciliation_process.go
// contains the per-scene verification loop of the
// AssetLocationReconciliationProcessor (Process) plus the pure
// helpers it uses. See processor_asset_location_reconciliation.go
// for the processor header and reconciliation_verify.go for the
// single-link verifier wrapper.
package adapters

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Process iterates over every scene in the SpecScene and verifies
// each drive_link found in the bindings. Verified links are kept;
// updated links are replaced with the canonical webViewLink;
// missing/trashed/inaccessible/malformed links are cleared and
// flagged as warnings. The UpdatedSpecScene field in the result
// carries the reconciled SpecScene for downstream consumption.
func (p *AssetLocationReconciliationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	_ = plan

	if p.verifier == nil {
		// Configuration failure is required, but still return a
		// fail-closed scene so a registry that is already running the
		// plan cannot forward stale links to later processors before
		// the required error is surfaced.
		reconciled := cloneSpecScenes(input.SpecScene.Scenes)
		clearUnverifiedSceneLinks(reconciled)
		return &PostProcessResult{
			Changed:          len(reconciled) > 0,
			SpecSceneChanged: len(reconciled) > 0,
			UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
			Warnings:         []string{"asset_location_reconciliation: verifier is not configured (all Drive links cleared)"},
		}, fmt.Errorf("%w: asset_location_reconciliation processor: AssetLocationVerifier not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	var (
		warnings          []string
		changed           bool
		okCount           int
		updatedCount      int
		missingCount      int
		trashedCount      int
		inaccessibleCount int
		malformedCount    int
		assetChanges      = make(map[string]scriptpkg.AssetLocationChange)
		conflictErr       error
	)

	// Work on a copy of the scenes so we can mutate in place.
	reconciled := cloneSpecScenes(scenes)

	for i := range reconciled {
		scene := &reconciled[i]
		bindings := &scene.Bindings

		// ── Clip binding ────────────────────────────────────
		if bindings.Clip != nil {
			if plan != nil && plan.MediaMode == scriptpkg.MediaModeClipOnly {
				// clip_only receives published clip IDs from the canonical
				// clip resolver. Legacy rows may omit the URL in the binding,
				// or carry the Drive ID in its place; normalize that trusted
				// catalog identity and keep it out of the generic stock/file
				// reconciliation path, which cannot verify these legacy rows
				// reliably against their internal asset IDs.
				link := strings.TrimSpace(bindings.Clip.DriveLink)
				if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
					if link == "" {
						link = strings.TrimSpace(bindings.Clip.ClipID)
					}
					if len(link) >= 20 {
						link = "https://drive.google.com/file/d/" + link + "/view"
					}
				}
				if link != "" {
					bindings.Clip.DriveLink = link
				}
				continue
			}
			if link := strings.TrimSpace(bindings.Clip.DriveLink); link != "" {
				// Legacy clip rows can surface the canonical Drive file ID
				// in the link slot. Normalize it before verification so the
				// verifier receives the same canonical URL as the source
				// resolver and the published contract never contains a bare ID.
				if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") && len(link) >= 20 {
					link = "https://drive.google.com/file/d/" + link + "/view"
					bindings.Clip.DriveLink = link
				}
				clipFileID, _ := urlutil.FileIDFromDriveLink(link)
				if strings.TrimSpace(clipFileID) == "" {
					// The clip resolver's canonical ID is a Drive file ID for
					// published YouTube clips. Keep reconciliation fail-closed
					// while avoiding a false MALFORMED result when a legacy
					// binding carried that ID in a non-URL representation.
					clipFileID = strings.TrimSpace(bindings.Clip.ClipID)
				}
				result, err := p.verifyAndReconcile(
					ctx, bindings.Clip.ClipID,
					clipFileID, link,
					&bindings.Clip.DriveLink,
					"clip", scene.ID,
				)
				if err != nil {
					return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
				if result.assetChange != nil {
					if previous, exists := assetChanges[result.assetChange.AssetID]; exists && previous != *result.assetChange && conflictErr == nil {
						conflictErr = fmt.Errorf("%w: conflicting durable location changes for asset %q", scriptpkg.ErrPostprocessFailed, result.assetChange.AssetID)
					}
					assetChanges[result.assetChange.AssetID] = *result.assetChange
				}
			}
			if link := strings.TrimSpace(bindings.Clip.SubtitleLink); link != "" {
				fileID := strings.TrimSpace(bindings.Clip.SubtitleFileID)
				result, err := p.verifyAndReconcile(
					ctx, bindings.Clip.ClipID,
					fileID, link,
					&bindings.Clip.SubtitleLink,
					"subtitle", scene.ID,
				)
				if err != nil {
					return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
				if result.changed && strings.TrimSpace(bindings.Clip.SubtitleLink) == "" {
					bindings.Clip.SubtitleFileID = ""
				}
			}
		}

		// ── Stock binding ───────────────────────────────────
		if bindings.Stock != nil && strings.TrimSpace(bindings.Stock.FolderLink) == "" {
			assetID := strings.TrimSpace(bindings.Stock.AssetID)
			link := strings.TrimSpace(bindings.Stock.DriveLink)
			// Direct YouTube stock bindings may carry the canonical Drive
			// file ID as asset_id while drive_link is absent or malformed.
			// Resolve that ID through the same verifier and let Drive return
			// the canonical webViewLink; never publish the raw ID as a URL.
			fileID := ""
			if assetID != "" && (link == "" || link == assetID) {
				fileID = assetID
				link = "https://drive.google.com/file/d/" + assetID + "/view"
				bindings.Stock.DriveLink = link
			} else if parsedID, parseErr := urlutil.FileIDFromDriveLink(link); parseErr == nil {
				// LocationVerifier requires the Drive file ID explicitly and
				// does not infer it from the link. Use the canonical shared
				// parser for /file/d/... and query-based Drive URLs.
				fileID = parsedID
			}
			if link != "" || fileID != "" {
				result, err := p.verifyAndReconcile(
					ctx, assetID, fileID, link,
					&bindings.Stock.DriveLink,
					"stock", scene.ID,
				)
				if err != nil {
					return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
				if result.assetChange != nil {
					if previous, exists := assetChanges[result.assetChange.AssetID]; exists && previous != *result.assetChange && conflictErr == nil {
						conflictErr = fmt.Errorf("%w: conflicting durable location changes for asset %q", scriptpkg.ErrPostprocessFailed, result.assetChange.AssetID)
					}
					assetChanges[result.assetChange.AssetID] = *result.assetChange
				}
			}
		}

		// ── Drive-backed image binding ──────────────────────
		// Image URLs may be provider URLs, so only Google Drive URLs
		// enter this verifier. Non-Drive provider URLs are left intact;
		// Drive-backed image URLs are held to the same fail-closed rule
		// as every other published location.
		if bindings.Image != nil {
			if link := strings.TrimSpace(bindings.Image.URL); link != "" {
				if fileID, fileIDErr := urlutil.FileIDFromDriveLink(link); (fileIDErr == nil && fileID != "") || isGoogleDriveURL(link) {
					result, err := p.verifyAndReconcile(
						ctx, bindings.Image.ImageID, fileID, link,
						&bindings.Image.URL, "image", scene.ID,
					)
					if err != nil {
						return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
					}
					changed = changed || result.changed
					okCount += result.ok
					updatedCount += result.updated
					missingCount += result.missing
					trashedCount += result.trashed
					inaccessibleCount += result.inaccessible
					malformedCount += result.malformed
					if result.warning != "" {
						warnings = append(warnings, result.warning)
					}
					if result.assetChange != nil {
						if previous, exists := assetChanges[result.assetChange.AssetID]; exists && previous != *result.assetChange && conflictErr == nil {
							conflictErr = fmt.Errorf("%w: conflicting durable location changes for asset %q", scriptpkg.ErrPostprocessFailed, result.assetChange.AssetID)
						}
						assetChanges[result.assetChange.AssetID] = *result.assetChange
					}
					if result.changed && strings.TrimSpace(bindings.Image.URL) == "" {
						bindings.Image.Status = string(scriptpkg.ImageStatusFailed)
					}
				}
			}
		}

		// ── Voiceover binding ───────────────────────────────
		if bindings.Voiceover != nil {
			if link := strings.TrimSpace(bindings.Voiceover.Link); link != "" {
				result, err := p.verifyAndReconcile(
					ctx, "voiceover:"+scene.ID,
					"", link,
					&bindings.Voiceover.Link,
					"voiceover", scene.ID,
				)
				if err != nil {
					return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
			}
		}

		// ── Media bindings ──────────────────────────────────
		for j := range bindings.Media {
			media := &bindings.Media[j]
			if link := strings.TrimSpace(media.DriveLink); link != "" {
				assetID := media.AssetID
				result, err := p.verifyAndReconcile(
					ctx, assetID,
					"", link,
					&media.DriveLink,
					fmt.Sprintf("media[%d]", j), scene.ID,
				)
				if err != nil {
					return reconciliationFailureResult(input, reconciled, changed, warnings, result, err)
				}
				changed = changed || result.changed
				okCount += result.ok
				updatedCount += result.updated
				missingCount += result.missing
				trashedCount += result.trashed
				inaccessibleCount += result.inaccessible
				malformedCount += result.malformed
				if result.warning != "" {
					warnings = append(warnings, result.warning)
				}
				if result.assetChange != nil {
					if previous, exists := assetChanges[result.assetChange.AssetID]; exists && previous != *result.assetChange && conflictErr == nil {
						conflictErr = fmt.Errorf("%w: conflicting durable location changes for asset %q", scriptpkg.ErrPostprocessFailed, result.assetChange.AssetID)
					}
					assetChanges[result.assetChange.AssetID] = *result.assetChange
				}
			}
		}
	}

	if conflictErr != nil {
		return &PostProcessResult{
			Changed:          changed,
			SpecSceneChanged: changed,
			UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
			Warnings:         warnings,
		}, conflictErr
	}

	// ── PLAN → APPLY ─────────────────────────────────────────────
	// Compute the immutable ReconcilePlan from the scan/classify phase, then
	// Apply BY THE PLAN. dry-run (no committer) and real execution (committer
	// wired) both build this same plan; Apply consumes only plan values and
	// plan.AssetLocationChanges(), never a re-scan of the mutable scenes. Two
	// runs over the same classification therefore produce the same Hash() and
	// an already-reconciled scene yields an empty plan (zero diff).
	reconcilePlan := buildReconcilePlan(assetChanges)
	reconcilePlan.Noops = okCount
	if len(reconcilePlan.Conflicts) > 0 {
		return &PostProcessResult{
			Changed:          changed,
			SpecSceneChanged: changed,
			UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
			Warnings:         warnings,
		}, fmt.Errorf("%w: reconciliation plan contains conflicts", scriptpkg.ErrPostprocessFailed)
	}

	if p.committer != nil && len(assetChanges) > 0 {
		if err := p.committer.CommitAssetLocations(ctx, reconcilePlan.AssetLocationChanges()); err != nil {
			return &PostProcessResult{
				Changed:          changed,
				SpecSceneChanged: changed,
				UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
				Warnings:         warnings,
			}, fmt.Errorf("%w: asset_location_reconciliation commit failed: %w", scriptpkg.ErrPostprocessFailed, err)
		}
	}

	return &PostProcessResult{
		Changed:          changed,
		SpecSceneChanged: changed,
		UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
		Warnings:         warnings,
	}, nil
}

func reconciliationFailureResult(
	input ProcessInput,
	reconciled []scriptpkg.SpecScene,
	changed bool,
	warnings []string,
	result reconcileResult,
	err error,
) (*PostProcessResult, error) {
	changed = changed || result.changed
	if result.warning != "" {
		warnings = append(warnings, result.warning)
	}
	return &PostProcessResult{
		Changed:          changed,
		SpecSceneChanged: changed,
		UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
		Warnings:         warnings,
	}, err
}

// reconciledSpecSceneOutput keeps timeline-level assignments intact while
// replacing the scene bindings with their reconciled copy. These fields are
// independent: post-segment/intro clips must survive the final Drive-link
// verification pass even though they are not scene bindings.
func reconciledSpecSceneOutput(input scriptpkg.SpecSceneOutput, scenes []scriptpkg.SpecScene) scriptpkg.SpecSceneOutput {
	return scriptpkg.SpecSceneOutput{
		Version:           input.Version,
		Scenes:            scenes,
		VisualAssignments: append([]mediadomain.VisualAssignment(nil), input.VisualAssignments...),
	}
}

func isGoogleDriveURL(rawLink string) bool {
	link := strings.TrimSpace(rawLink)
	parsed, err := url.Parse(link)
	if err == nil {
		host := strings.ToLower(parsed.Hostname())
		return host == "drive.google.com" || host == "www.drive.google.com" || host == "docs.google.com" || host == "www.docs.google.com"
	}
	lower := strings.ToLower(link)
	return strings.HasPrefix(lower, "https://drive.google.com/") || strings.HasPrefix(lower, "http://drive.google.com/")
}

// cloneSpecScenes returns a copy of the scenes slice for safe
// in-place mutation during reconciliation. It preserves every
// binding type, including Media.
func cloneSpecScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, len(scenes))
	for i, sc := range scenes {
		out[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			out[i].Metadata = &meta
		}
		out[i].Bindings = cloneSceneBindings(sc.Bindings)
	}
	return out
}

// clearUnverifiedSceneLinks removes every publishable Drive-backed
// location from a scene copy. It is used only for configuration
// failures, where verification cannot start at all.
func clearUnverifiedSceneLinks(scenes []scriptpkg.SpecScene) {
	for i := range scenes {
		bindings := &scenes[i].Bindings
		if bindings.Clip != nil {
			bindings.Clip.DriveLink = ""
			bindings.Clip.SubtitleLink = ""
			bindings.Clip.SubtitleFileID = ""
		}
		if bindings.Stock != nil {
			bindings.Stock.DriveLink = ""
		}
		if bindings.Image != nil {
			bindings.Image.URL = ""
			bindings.Image.Status = string(scriptpkg.ImageStatusFailed)
		}
		if bindings.Voiceover != nil {
			bindings.Voiceover.Link = ""
		}
		for j := range bindings.Media {
			bindings.Media[j].DriveLink = ""
		}
	}
}
