package wiring

import (
	"context"
	"fmt"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type localizedRenderRequest struct {
	identity           localizedRenderIdentity
	request            localization.LocalizationRequest
	generatedSubtitles bool
	watermark          *cliprender.MaterializedAsset
	watermarkSpec      *cliprender.WatermarkSpec
	background         *cliprender.MaterializedAsset
	backgroundMode     string
	overlays           []cliprender.OverlayRefSpec
	destinationFolder  string
	subtitleFolder     string
}

func (a *localizedRenderEnqueuerAdapter) buildLocalizedRenderRequest(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (localizedRenderRequest, error) {
	identity, err := a.resolveRenderIdentity(ctx, in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	generated, err := a.ensureDatabaseSubtitles(ctx, identity.assetID, identity.sourceLang, identity.targetLang, in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	watermark, watermarkSpec, err := a.resolveWatermark(ctx, in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	background, backgroundMode, err := a.resolveBackground(ctx, in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	overlays, err := a.resolveOverlays(in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	destination, subtitle, err := a.resolveRenderFolders(ctx, in, identity.clipID)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	request := localization.LocalizationRequest{RenderConcurrency: a.cfg.Concurrency, Languages: []localization.LanguageRequest{{Language: identity.targetLang, Priority: 0}}}
	request.Normalize()
	return localizedRenderRequest{
		identity: identity, request: request, generatedSubtitles: generated,
		watermark: watermark, watermarkSpec: watermarkSpec, background: background,
		backgroundMode: backgroundMode, overlays: overlays,
		destinationFolder: destination, subtitleFolder: subtitle,
	}, nil
}

// resolveOverlays carries the run's certified, LANGUAGE-INDEPENDENT entity
// overlays into the render request, one per semantic overlay item. It performs
// no resolution of its own: each lineage IS the certified overlay render
// reference, and every segment is content-addressed, so every language variant
// passes the same render_keys and the fan-out reuses one overlay render per item
// instead of re-rendering them per language.
//
// Fail-closed: a partial lineage (the clip.render contract is all-or-nothing)
// or an invalid window is a typed error, never a half-declared overlay that
// silently composites nothing — and never a variant that carries three of the
// four declared overlays.
func (a *localizedRenderEnqueuerAdapter) resolveOverlays(in scriptgeneration.LocalizedRenderInput) ([]cliprender.OverlayRefSpec, error) {
	lineages := in.Overlays
	if len(lineages) == 0 {
		return nil, nil
	}
	out := make([]cliprender.OverlayRefSpec, 0, len(lineages))
	for i, lineage := range lineages {
		if strings.TrimSpace(lineage.RenderJobID) == "" ||
			strings.TrimSpace(lineage.PlanFingerprint) == "" ||
			strings.TrimSpace(lineage.RenderKey) == "" ||
			strings.TrimSpace(lineage.SourceVideoAssetID) == "" {
			return nil, fmt.Errorf("localized render: overlay %d lineage is incomplete (render_job_id, plan_fingerprint, render_key and source_video_asset_id are required)", i)
		}
		if lineage.StartUS < 0 || lineage.EndUS <= lineage.StartUS {
			return nil, fmt.Errorf("localized render: overlay %d window is invalid (end_us %d must be > start_us %d >= 0)", i, lineage.EndUS, lineage.StartUS)
		}
		// The scripts domain mirror carries the identical identity: the mapping
		// is field-for-field, so every language variant keeps passing the same
		// render_keys and each overlay render is reused instead of re-rendered.
		out = append(out, cliprender.OverlayRefSpec{
			RenderJobID:        lineage.RenderJobID,
			PlanFingerprint:    lineage.PlanFingerprint,
			RenderKey:          lineage.RenderKey,
			SourceVideoAssetID: lineage.SourceVideoAssetID,
			StartUS:            lineage.StartUS,
			EndUS:              lineage.EndUS,
		})
	}
	return out, nil
}

func (a *localizedRenderEnqueuerAdapter) resolveWatermark(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (*cliprender.MaterializedAsset, *cliprender.WatermarkSpec, error) {
	watermark := in.Render.Watermark
	if watermark == nil || !watermark.Enabled {
		return nil, nil, nil
	}
	if strings.TrimSpace(watermark.Text) == "" && (strings.TrimSpace(watermark.AssetID) == "" || a.assets == nil || a.material == nil) {
		return nil, nil, fmt.Errorf("localized render: watermark requested but its asset resolver is not wired")
	}
	var materialized *cliprender.MaterializedAsset
	if strings.TrimSpace(watermark.AssetID) != "" {
		ref, err := a.assets.ResolveAsset(ctx, watermark.AssetID)
		if err != nil {
			return nil, nil, fmt.Errorf("localized render: resolve watermark %q: %w", watermark.AssetID, err)
		}
		materialized, err = a.material.Materialize(ctx, *ref)
		if err != nil {
			return nil, nil, fmt.Errorf("localized render: materialize watermark %q: %w", watermark.AssetID, err)
		}
	}
	return materialized, &cliprender.WatermarkSpec{Enabled: true, AssetID: watermark.AssetID, Text: watermark.Text, Position: watermark.Position, Opacity: watermark.Opacity, MarginPX: watermark.MarginPX, Style: watermark.Style}, nil
}

func (a *localizedRenderEnqueuerAdapter) resolveBackground(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (*cliprender.MaterializedAsset, string, error) {
	background := in.Render.Background
	if background == nil {
		return nil, "", nil
	}
	mode := background.Mode
	if mode == "" {
		mode = cliprender.BackgroundModeNone
	}
	if mode != cliprender.BackgroundModeAsset {
		return nil, mode, nil
	}
	if strings.TrimSpace(background.AssetID) == "" || a.assets == nil || a.material == nil {
		return nil, "", fmt.Errorf("localized render: background requested but its asset resolver is not wired")
	}
	ref, err := a.assets.ResolveAsset(ctx, background.AssetID)
	if err != nil {
		return nil, "", fmt.Errorf("localized render: resolve background %q: %w", background.AssetID, err)
	}
	materialized, err := a.material.Materialize(ctx, *ref)
	if err != nil {
		return nil, "", fmt.Errorf("localized render: materialize background %q: %w", background.AssetID, err)
	}
	return materialized, mode, nil
}

func (a *localizedRenderEnqueuerAdapter) resolveRenderFolders(ctx context.Context, in scriptgeneration.LocalizedRenderInput, clipID string) (string, string, error) {
	destination := strings.TrimSpace(a.cfg.FolderID)
	if value := strings.TrimSpace(in.Render.DriveFolderID); value != "" {
		destination = value
	}
	subtitle := strings.TrimSpace(a.cfg.SubtitleFolderID)
	var err error
	if subtitle != "" {
		resolvedSub, err := a.resolveFolder(ctx, "subtitle\x00"+subtitle+"\x00"+clipID, clipID, subtitle)
		if err != nil {
			if a.log != nil {
				a.log.Warn("localized render: could not create subtitle subfolder, proceeding without separate subtitle folder", zap.String("clip_id", clipID), zap.Error(err))
			}
			subtitle = ""
		} else {
			subtitle = resolvedSub
		}
	}
	if subfolder := strings.TrimSpace(in.Render.DriveSubfolderName); subfolder != "" {
		destination, err = a.resolveFolder(ctx, destination+"\x00"+subfolder, subfolder, destination)
		if err != nil {
			return "", "", fmt.Errorf("localized render: ensure Drive subfolder %q: %w", subfolder, err)
		}
	}
	return destination, subtitle, nil
}

func (a *localizedRenderEnqueuerAdapter) resolveFolder(ctx context.Context, key, name, parent string) (string, error) {
	if a.cfg.FolderAdmin == nil {
		return "", fmt.Errorf("folder admin is not wired")
	}
	a.folderMu.Lock()
	defer a.folderMu.Unlock()
	if cached := a.folderCache[key]; cached != "" {
		return cached, nil
	}
	resolved, err := a.cfg.FolderAdmin.GetOrCreateFolder(ctx, name, parent)
	if err != nil {
		return "", err
	}
	a.folderCache[key] = resolved
	return resolved, nil
}

func (a *localizedRenderEnqueuerAdapter) localizeInput(in scriptgeneration.LocalizedRenderInput, built localizedRenderRequest) LocalizeInput {
	return LocalizeInput{
		AssetID: built.identity.assetID, JobID: in.RunID, SceneID: in.SceneID,
		ClipID: built.identity.clipID, SourceLanguage: built.identity.sourceLang,
		Request: built.request, FolderID: built.destinationFolder, SubtitleFolderID: built.subtitleFolder,
		UploadSubtitleArtifact: built.generatedSubtitles,
		DocTitle:               fmt.Sprintf("Localized — %s (%s)", built.identity.clipID, built.identity.targetLang),
		DocFolderID:            a.cfg.DocFolderID, DocIdempotencyKey: in.RunID + ":" + in.SceneID + ":" + built.identity.targetLang,
		SkipDocument: true, Watermark: built.watermark, WatermarkSpec: built.watermarkSpec,
		Background: built.background, BackgroundMode: built.backgroundMode,
		Overlays:               built.overlays,
		ForegroundScalePercent: in.Render.ForegroundScalePercent,
		SubtitlesStyle:         subtitleStyle(in),
		OnRendered: func(artifact localization.LocalizedClipArtifact) error {
			if in.OnRenderReady == nil {
				return nil
			}
			return in.OnRenderReady(scriptgeneration.LocalizedRenderResult{
				SceneID: artifact.SceneID, SceneIndex: in.SceneIndex, Language: scriptgeneration.Language(artifact.Language),
				ClipID: artifact.ClipID, AssetID: artifact.AssetID, SHA256: artifact.SHA256, DurationMS: artifact.DurationMS,
				LocalPath: artifact.LocalPath, Status: string(artifact.Status), Metrics: metricsMapFromJSON(artifact.MetricsJSON), StartedAt: time.Now().UTC(),
			})
		},
	}
}

func subtitleStyle(in scriptgeneration.LocalizedRenderInput) *scriptpkg.VideoVisualStyleSpec {
	if in.Render.Subtitles == nil {
		return nil
	}
	return in.Render.Subtitles.Style
}
