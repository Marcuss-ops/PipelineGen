// Package adapters — processor_asset_location_reconciliation.go
// verifies every drive_link in the SpecScene bindings against the
// canonical AssetLocationVerifier before the document is published.
//
// The processor runs after clip_bindings and stock_bindings have
// populated the per-scene bindings and before the document and
// persistence processors consume them. It ensures:
//
//   - No broken link reaches the Google Doc renderer.
//   - Stale links are replaced with the canonical webViewLink.
//   - Missing/trashed/inaccessible links are cleared and flagged
//     as warnings.
//
// The processor is BestEffort: transport errors are surfaced as
// warnings rather than hard failures, but every unverified link is
// cleared before downstream publication. A nil verifier is still a
// hard composition error.
package adapters

import (
	"context"
	"fmt"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// AssetLocationReconciliationProcessor verifies every drive_link
// in the SpecScene bindings and reconciles them against the
// canonical AssetLocationVerifier.
type AssetLocationReconciliationProcessor struct {
	verifier  scriptpkg.AssetLocationVerifier
	committer scriptpkg.AssetLocationCommitter
}

// NewAssetLocationReconciliationProcessor creates the processor.
// verifier must be non-nil (enforced at registration time).
func NewAssetLocationReconciliationProcessor(
	verifier scriptpkg.AssetLocationVerifier,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{verifier: verifier}
}

// NewDurableAssetLocationReconciliationProcessor adds the canonical
// SQLite/outbox commit port. Keeping this as a separate constructor makes
// the durable dependency explicit and prevents silently ignoring a second
// committer at composition time.
func NewDurableAssetLocationReconciliationProcessor(
	verifier scriptpkg.AssetLocationVerifier,
	committer scriptpkg.AssetLocationCommitter,
) *AssetLocationReconciliationProcessor {
	return &AssetLocationReconciliationProcessor{verifier: verifier, committer: committer}
}

func (p *AssetLocationReconciliationProcessor) Name() ProcessorName {
	return ProcessorAssetLocationReconciliation
}

// Policy is BestEffort for verification-only composition. A configured
// committer makes the complete reconciliation Required because the
// downstream SpecScene must not be published when its durable asset
// mutation and Qdrant outbox event could not be committed.
func (p *AssetLocationReconciliationProcessor) Policy(
	_ *scriptpkg.ResolvedGenerationPlan,
) ProcessorPolicy {
	if p == nil || p.verifier == nil || p.committer != nil {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

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
			UpdatedSpecScene: scriptpkg.SpecSceneOutput{Version: input.SpecScene.Version, Scenes: reconciled},
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
	)

	// Work on a copy of the scenes so we can mutate in place.
	reconciled := cloneSpecScenes(scenes)

	for i := range reconciled {
		scene := &reconciled[i]
		bindings := &scene.Bindings

		// ── Clip binding ────────────────────────────────────
		if bindings.Clip != nil {
			if link := strings.TrimSpace(bindings.Clip.DriveLink); link != "" {
				result, err := p.verifyAndReconcile(
					ctx, bindings.Clip.ClipID,
					"", link,
					&bindings.Clip.DriveLink,
					"clip", scene.ID,
				)
				if err != nil {
					return nil, err
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
					return nil, err
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
		if bindings.Stock != nil {
			if link := strings.TrimSpace(bindings.Stock.DriveLink); link != "" {
				assetID := bindings.Stock.AssetID
				result, err := p.verifyAndReconcile(
					ctx, assetID,
					"", link,
					&bindings.Stock.DriveLink,
					"stock", scene.ID,
				)
				if err != nil {
					return nil, err
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
				if fileID, fileIDErr := urlutil.FileIDFromDriveLink(link); fileIDErr == nil && fileID != "" {
					result, err := p.verifyAndReconcile(
						ctx, bindings.Image.ImageID, fileID, link,
						&bindings.Image.URL, "image", scene.ID,
					)
					if err != nil {
						return nil, err
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
					return nil, err
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
					return nil, err
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
					assetChanges[result.assetChange.AssetID] = *result.assetChange
				}
			}
		}
	}

	if p.committer != nil && len(assetChanges) > 0 {
		changes := make([]scriptpkg.AssetLocationChange, 0, len(assetChanges))
		for _, change := range assetChanges {
			changes = append(changes, change)
		}
		sort.Slice(changes, func(i, j int) bool {
			return changes[i].AssetID < changes[j].AssetID
		})
		if err := p.committer.CommitAssetLocations(ctx, changes); err != nil {
			return &PostProcessResult{
				Changed:          changed,
				UpdatedSpecScene: scriptpkg.SpecSceneOutput{Version: input.SpecScene.Version, Scenes: reconciled},
				Warnings:         warnings,
			}, fmt.Errorf("%w: asset_location_reconciliation commit failed: %w", scriptpkg.ErrPostprocessFailed, err)
		}
	}

	return &PostProcessResult{
		Changed:          changed,
		UpdatedSpecScene: scriptpkg.SpecSceneOutput{Version: input.SpecScene.Version, Scenes: reconciled},
		Warnings:         warnings,
	}, nil
}

// reconcileResult captures the outcome of a single link verification.
type reconcileResult struct {
	changed         bool
	ok              int
	updated         int
	missing         int
	trashed         int
	inaccessible    int
	malformed       int
	warning         string
	durableMutation bool
	assetChange     *scriptpkg.AssetLocationChange
	driveFileID     string
}

// verifyAndReconcile calls the verifier for a single link and
// updates the link pointer in place. Returns a reconcileResult
// with counters and an optional warning string.
// Transport errors (network timeout, Drive API 5xx) are surfaced as
// warnings and clear only the downstream link. Confirmed invalid states
// produce durable mutations; the injected committer persists those
// changes together with the Qdrant outbox event.
func (p *AssetLocationReconciliationProcessor) verifyAndReconcile(
	ctx context.Context,
	assetID string,
	fileID string,
	link string,
	linkPtr *string,
	label string,
	sceneID string,
) (result reconcileResult, err error) {
	defer func() {
		if !result.durableMutation || strings.TrimSpace(assetID) == "" || strings.HasPrefix(assetID, "voiceover:") || label == "subtitle" || linkPtr == nil {
			return
		}
		fileID := strings.TrimSpace(result.driveFileID)
		if strings.TrimSpace(*linkPtr) == "" {
			fileID = ""
		}
		result.assetChange = &scriptpkg.AssetLocationChange{
			AssetID:     strings.TrimSpace(assetID),
			DriveFileID: fileID,
			DriveLink:   strings.TrimSpace(*linkPtr),
		}
	}()

	verified, err := p.verifier.Verify(ctx, assetID, fileID, link)
	if err != nil {
		// BestEffort keeps generation running, but fails closed for
		// publication: an unverified link must never reach the
		// document, manifest, or persisted SpecScene.
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			warning: fmt.Sprintf(
				"asset_location_reconciliation: transport error verifying %s link in %s (link cleared): %v",
				label, sceneID, err),
		}, nil
	}
	if verified == nil {
		// A verifier that cannot produce a result has not established
		// that the link is usable. Fail closed rather than publishing
		// an unverified location.
		*linkPtr = ""
		return reconcileResult{
			changed: true,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s has UNKNOWN verification state (verifier returned no result; link cleared): asset=%s",
				label, sceneID, assetID),
		}, nil
	}

	result.driveFileID = verified.DriveFileID
	switch verified.State {
	case scriptpkg.LocationStateVerified:
		return reconcileResult{ok: 1}, nil

	case scriptpkg.LocationStateUpdated:
		*linkPtr = verified.DriveLink
		return reconcileResult{changed: true, durableMutation: true, updated: 1, driveFileID: verified.DriveFileID}, nil

	case scriptpkg.LocationStateMissing:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			missing:         1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is MISSING (Drive file not found): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateTrashed:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			trashed:         1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is TRASHED (file in Drive trash): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateInaccessible:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			inaccessible:    1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is INACCESSIBLE (permission denied): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateMalformed:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			malformed:       1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is MALFORMED (cannot extract file ID): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateOrphanDriveFile:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			missing:         1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is an ORPHAN (file on Drive but no SQLite record): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateBrokenAssetLocation:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			missing:         1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s has a BROKEN location (SQLite references missing Drive file): asset=%s",
				label, sceneID, assetID),
		}, nil

	case scriptpkg.LocationStateDuplicate:
		*linkPtr = ""
		return reconcileResult{
			changed:         true,
			durableMutation: true,
			malformed:       1,
			driveFileID:     verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s is a DUPLICATE (multiple assets share same drive_file_id): asset=%s",
				label, sceneID, assetID),
		}, nil

	default:
		// New or corrupt verifier states must never bypass the gate.
		// Preserve generation as best-effort, but clear the link and
		// expose the degraded outcome to status classification.
		*linkPtr = ""
		return reconcileResult{
			changed:     true,
			driveFileID: verified.DriveFileID,
			warning: fmt.Sprintf("asset_location_reconciliation: %s link in %s has UNKNOWN verification state %q (link cleared): asset=%s",
				label, sceneID, verified.State, assetID),
		}, nil
	}
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
