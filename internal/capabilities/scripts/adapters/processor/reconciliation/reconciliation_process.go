// Package reconciliation — reconciliation_process.go contains the per-scene
// verification loop of the AssetLocationReconciliationProcessor (Process) plus
// the pure helpers it uses. See processor_asset_location_reconciliation.go for
// the processor header and reconciliation_verify.go for the single-link
// verifier wrapper.
package reconciliation

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// reconciliationState is the accumulator shared by every scene and binding
// phase. It owns the counters, warnings and durable-change conflict check so
// each phase can be read in isolation without losing the aggregate contract.
type reconciliationState struct {
	reconciled        []scriptpkg.SpecScene
	warnings          []string
	changed           bool
	okCount           int
	updatedCount      int
	missingCount      int
	trashedCount      int
	inaccessibleCount int
	malformedCount    int
	assetChanges      map[string]scriptpkg.AssetLocationChange
	conflictErr       error
}

func newReconciliationState(scenes []scriptpkg.SpecScene) *reconciliationState {
	return &reconciliationState{
		reconciled:   cloneSpecScenes(scenes),
		assetChanges: make(map[string]scriptpkg.AssetLocationChange),
	}
}

// record folds one successful link result into the aggregate. A failing link
// deliberately goes through fail instead: the original contract preserved the
// fail-closed scene and warning but did not count the failed link.
func (s *reconciliationState) record(result reconcileResult) {
	s.changed = s.changed || result.changed
	s.okCount += result.ok
	s.updatedCount += result.updated
	s.missingCount += result.missing
	s.trashedCount += result.trashed
	s.inaccessibleCount += result.inaccessible
	s.malformedCount += result.malformed
	if result.warning != "" {
		s.warnings = append(s.warnings, result.warning)
	}
	if result.assetChange != nil {
		if previous, exists := s.assetChanges[result.assetChange.AssetID]; exists && previous != *result.assetChange && s.conflictErr == nil {
			s.conflictErr = fmt.Errorf("%w: conflicting durable location changes for asset %q", scriptpkg.ErrPostprocessFailed, result.assetChange.AssetID)
		}
		s.assetChanges[result.assetChange.AssetID] = *result.assetChange
	}
}

func (s *reconciliationState) fail(input adapters.ProcessInput, result reconcileResult, err error) (*adapters.PostProcessResult, error) {
	changed := s.changed || result.changed
	warnings := s.warnings
	if result.warning != "" {
		warnings = append(warnings, result.warning)
	}
	return &adapters.PostProcessResult{
		Changed:          changed,
		SpecSceneChanged: changed,
		UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, s.reconciled),
		Warnings:         warnings,
	}, err
}

// Process iterates over every scene in the SpecScene and verifies each
// drive_link found in the bindings. Verified links are kept; updated links are
// replaced with the canonical webViewLink; missing/trashed/inaccessible/
// malformed links are cleared and flagged as warnings. The UpdatedSpecScene
// field carries the reconciled SpecScene for downstream consumption.
//
// Process coordinates scan/classify, builds the immutable plan, applies it by
// plan and reports the reconciled scene. The per-link state machine remains
// owned by verifyAndReconcile.
func (p *AssetLocationReconciliationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input adapters.ProcessInput,
) (*adapters.PostProcessResult, error) {
	if p.verifier == nil {
		// Configuration failure is required, but still return a fail-closed
		// scene so a registry that is already running the plan cannot forward
		// stale links to later processors before the required error surfaces.
		reconciled := cloneSpecScenes(input.SpecScene.Scenes)
		clearUnverifiedSceneLinks(reconciled)
		return reconciliationReport(input, reconciled, len(reconciled) > 0, []string{"asset_location_reconciliation: verifier is not configured (all Drive links cleared)"}), fmt.Errorf("%w: asset_location_reconciliation processor: AssetLocationVerifier not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &adapters.PostProcessResult{}, nil
	}

	state := newReconciliationState(scenes)
	for i := range state.reconciled {
		result, err := p.reconcileScene(ctx, plan, &state.reconciled[i], state)
		if err != nil {
			return state.fail(input, result, err)
		}
	}

	if state.conflictErr != nil {
		return reconciliationReport(input, state.reconciled, state.changed, state.warnings), state.conflictErr
	}

	// PLAN → APPLY: the immutable plan is computed from the scan/classify
	// phase and Apply consumes only plan values, never a re-scan of the
	// mutable scenes. Two runs over the same classification therefore produce
	// the same Hash() and an already-reconciled scene yields an empty plan.
	reconcilePlan := buildReconcilePlan(state.assetChanges)
	reconcilePlan.Noops = state.okCount
	if len(reconcilePlan.Conflicts) > 0 {
		return reconciliationReport(input, state.reconciled, state.changed, state.warnings), fmt.Errorf("%w: reconciliation plan contains conflicts", scriptpkg.ErrPostprocessFailed)
	}

	if err := p.applyReconcilePlan(ctx, reconcilePlan); err != nil {
		return reconciliationReport(input, state.reconciled, state.changed, state.warnings), fmt.Errorf("%w: asset_location_reconciliation commit failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}

	return reconciliationReport(input, state.reconciled, state.changed, state.warnings), nil
}

// reconcileScene owns the per-scene branch order: clip (including the
// clip_only short-circuit), subtitle, stock, Drive-backed image, voiceover and
// media bindings. Each branch mutates only the cloned scene and returns the
// last result so a failure can be reported with the same fail-closed surface.
func (p *AssetLocationReconciliationProcessor) reconcileScene(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	scene *scriptpkg.SpecScene,
	state *reconciliationState,
) (reconcileResult, error) {
	bindings := &scene.Bindings

	if bindings.Clip != nil {
		if plan != nil && plan.MediaMode == scriptpkg.MediaModeClipOnly {
			normalizeClipOnlyLink(bindings.Clip)
			return reconcileResult{}, nil
		}
		if result, err := p.reconcileClipLink(ctx, bindings.Clip, scene.ID); err != nil {
			return result, err
		} else {
			state.record(result)
		}
		if result, err := p.reconcileSubtitleLink(ctx, bindings.Clip, scene.ID); err != nil {
			return result, err
		} else {
			state.record(result)
			if result.changed && strings.TrimSpace(bindings.Clip.SubtitleLink) == "" {
				bindings.Clip.SubtitleFileID = ""
			}
		}
	}

	if bindings.Stock != nil && strings.TrimSpace(bindings.Stock.FolderLink) == "" {
		if result, err := p.reconcileStockLink(ctx, bindings.Stock, scene.ID); err != nil {
			return result, err
		} else {
			state.record(result)
		}
	}

	if bindings.Image != nil && strings.TrimSpace(bindings.Image.URL) != "" {
		if result, err := p.reconcileImageLink(ctx, bindings.Image, scene.ID); err != nil {
			return result, err
		} else {
			state.record(result)
			if result.changed && strings.TrimSpace(bindings.Image.URL) == "" {
				bindings.Image.Status = string(scriptpkg.ImageStatusFailed)
			}
		}
	}

	if bindings.Voiceover != nil {
		if link := strings.TrimSpace(bindings.Voiceover.Link); link != "" {
			result, err := p.verifyAndReconcile(ctx, "voiceover:"+scene.ID, "", link, &bindings.Voiceover.Link, "voiceover", scene.ID)
			if err != nil {
				return result, err
			}
			state.record(result)
		}
	}

	for j := range bindings.Media {
		media := &bindings.Media[j]
		if link := strings.TrimSpace(media.DriveLink); link != "" {
			result, err := p.verifyAndReconcile(ctx, media.AssetID, "", link, &media.DriveLink, fmt.Sprintf("media[%d]", j), scene.ID)
			if err != nil {
				return result, err
			}
			state.record(result)
		}
	}

	return reconcileResult{}, nil
}

func normalizeClipOnlyLink(binding *scriptpkg.ClipBinding) {
	link := strings.TrimSpace(binding.DriveLink)
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return
	}
	if link == "" {
		link = strings.TrimSpace(binding.ClipID)
	}
	if len(link) >= 20 {
		link = urlutil.DriveFileURL(link)
	}
	if link != "" {
		binding.DriveLink = link
	}
}

func (p *AssetLocationReconciliationProcessor) reconcileClipLink(ctx context.Context, binding *scriptpkg.ClipBinding, sceneID string) (reconcileResult, error) {
	link := strings.TrimSpace(binding.DriveLink)
	if link == "" {
		return reconcileResult{}, nil
	}
	// Legacy clip rows can surface the canonical Drive file ID in the link
	// slot. Normalize it before verification so the verifier receives the same
	// canonical URL as the source resolver and the published contract never
	// contains a bare ID.
	if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") && len(link) >= 20 {
		link = urlutil.DriveFileURL(link)
		binding.DriveLink = link
	}
	fileID, _ := urlutil.FileIDFromDriveLink(link)
	if strings.TrimSpace(fileID) == "" {
		// The clip resolver's canonical ID is a Drive file ID for published
		// YouTube clips. Preserve the legacy binding identity without turning
		// it into a false MALFORMED result.
		fileID = strings.TrimSpace(binding.ClipID)
	}
	return p.verifyAndReconcile(ctx, binding.ClipID, fileID, link, &binding.DriveLink, "clip", sceneID)
}

func (p *AssetLocationReconciliationProcessor) reconcileSubtitleLink(ctx context.Context, binding *scriptpkg.ClipBinding, sceneID string) (reconcileResult, error) {
	link := strings.TrimSpace(binding.SubtitleLink)
	if link == "" {
		return reconcileResult{}, nil
	}
	return p.verifyAndReconcile(ctx, binding.ClipID, strings.TrimSpace(binding.SubtitleFileID), link, &binding.SubtitleLink, "subtitle", sceneID)
}

func (p *AssetLocationReconciliationProcessor) reconcileStockLink(ctx context.Context, binding *scriptpkg.StockBinding, sceneID string) (reconcileResult, error) {
	assetID := strings.TrimSpace(binding.AssetID)
	link := strings.TrimSpace(binding.DriveLink)
	// Direct YouTube stock bindings may carry the canonical Drive file ID as
	// asset_id while drive_link is absent or malformed. Resolve that ID through
	// the verifier and let Drive return the canonical webViewLink; never publish
	// the raw ID as a URL.
	fileID := ""
	if assetID != "" && (link == "" || link == assetID) {
		fileID = assetID
		link = urlutil.DriveFileURL(assetID)
		binding.DriveLink = link
	} else if parsedID, parseErr := urlutil.FileIDFromDriveLink(link); parseErr == nil {
		fileID = parsedID
	}
	if link == "" && fileID == "" {
		return reconcileResult{}, nil
	}
	return p.verifyAndReconcile(ctx, assetID, fileID, link, &binding.DriveLink, "stock", sceneID)
}

func (p *AssetLocationReconciliationProcessor) reconcileImageLink(ctx context.Context, binding *scriptpkg.ImageBinding, sceneID string) (reconcileResult, error) {
	link := strings.TrimSpace(binding.URL)
	if link == "" {
		return reconcileResult{}, nil
	}
	fileID, fileIDErr := urlutil.FileIDFromDriveLink(link)
	if (fileIDErr != nil || fileID == "") && !isGoogleDriveURL(link) {
		return reconcileResult{}, nil
	}
	return p.verifyAndReconcile(ctx, binding.ImageID, fileID, link, &binding.URL, "image", sceneID)
}

// applyReconcilePlan is the only durable mutation boundary. It consumes the
// immutable plan produced by the scan/classify phase and never re-reads scenes.
func (p *AssetLocationReconciliationProcessor) applyReconcilePlan(ctx context.Context, plan ReconcilePlan) error {
	if p.mutator == nil || plan.Empty() {
		return nil
	}
	changes := plan.AssetLocationChanges()
	patches := make([]persistence.DriveLocationPatch, 0, len(changes))
	for _, change := range changes {
		patches = append(patches, persistence.DriveLocationPatch{
			AssetID:     change.AssetID,
			DriveFileID: change.DriveFileID,
			DriveLink:   change.DriveLink,
		})
	}
	return p.mutator.ReconcileDriveLocations(ctx, patches)
}

func reconciliationReport(input adapters.ProcessInput, reconciled []scriptpkg.SpecScene, changed bool, warnings []string) *adapters.PostProcessResult {
	return &adapters.PostProcessResult{
		Changed:          changed,
		SpecSceneChanged: changed,
		UpdatedSpecScene: reconciledSpecSceneOutput(input.SpecScene, reconciled),
		Warnings:         warnings,
	}
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

// cloneSpecScenes returns a copy of the scenes slice for safe in-place mutation
// during reconciliation. It preserves every binding type, including Media.
func cloneSpecScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := make([]scriptpkg.SpecScene, len(scenes))
	for i, sc := range scenes {
		out[i] = sc
		if sc.Metadata != nil {
			meta := *sc.Metadata
			out[i].Metadata = &meta
		}
		out[i].Bindings = adapters.CloneSceneBindings(sc.Bindings)
	}
	return out
}

// clearUnverifiedSceneLinks removes every publishable Drive-backed location
// from a scene copy. It is used only for configuration failures, where
// verification cannot start at all.
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
