package localizedrender

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
	backgroundKind     string
	backgroundBlurPX   float64
	backgroundZoom     float64
	overlays           []cliprender.OverlayRefSpec
	destinationFolder  string
	subtitleFolder     string
}

func (a *localizedRenderEnqueuerAdapter) buildLocalizedRenderRequest(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (localizedRenderRequest, error) {
	// Effects and grid are closed parameter sets. Validate them before any
	// subtitle/watermark/background resolution or render enqueue: an
	// incompatible schema must fail at the planning boundary, not after the
	// expensive fan-out has already started.
	if err := validateLocalizedRenderEffects(in); err != nil {
		return localizedRenderRequest{}, err
	}
	// The blurred-plate treatment is a closed numeric contract owned by
	// cliprender. Reject it here — before subtitle/watermark/background
	// resolution or any enqueue — so "blur 80 px on mode=asset" fails at
	// planning instead of rendering the wrong plate.
	if err := validateLocalizedRenderBackground(in); err != nil {
		return localizedRenderRequest{}, err
	}
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
	background, backgroundMode, backgroundKind, backgroundBlurPX, backgroundZoom, err := a.resolveBackground(ctx, in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	overlays, err := a.resolveOverlays(in)
	if err != nil {
		return localizedRenderRequest{}, err
	}
	// A final job on the object-store path never reads a Drive identity for
	// this clip, so resolving (and creating) its Drive folder hierarchy is
	// pure latency. The render still needs its local destination.
	var destination, subtitle string
	if !in.SkipDrivePublication {
		destination, subtitle, err = a.resolveRenderFolders(ctx, in, identity.clipID, string(identity.targetLang))
		if err != nil {
			return localizedRenderRequest{}, err
		}
	}
	request := localization.LocalizationRequest{RenderConcurrency: a.cfg.Concurrency, Languages: []localization.LanguageRequest{{Language: identity.targetLang, Priority: 0}}}
	request.Normalize()
	return localizedRenderRequest{
		identity: identity, request: request, generatedSubtitles: generated,
		watermark: watermark, watermarkSpec: watermarkSpec, background: background,
		backgroundMode: backgroundMode, backgroundKind: backgroundKind,
		backgroundBlurPX: backgroundBlurPX, backgroundZoom: backgroundZoom,
		overlays:          overlays,
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

// resolveBackground resolves the background selection for the localized
// fan-out AND its media family. The family is a sealed-plan input (an image
// plate and a video plate are different render layers), so it is resolved once
// here from the asset's canonical MediaType; a plate that cannot be classified
// fails closed instead of reaching a renderer that would have to guess.
func (a *localizedRenderEnqueuerAdapter) resolveBackground(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (*cliprender.MaterializedAsset, string, string, float64, float64, error) {
	background := in.Render.Background
	if background == nil {
		return nil, "", "", 0, 0, nil
	}
	mode := background.Mode
	if mode == "" {
		mode = cliprender.BackgroundModeNone
	}
	// The blurred-plate treatment belongs to blur_source only. It travels with
	// the mode (not only with the materialized asset) so the plan can validate
	// the pair and the fingerprint can include it. cliprender owns the bounds.
	if mode != cliprender.BackgroundModeAsset {
		return nil, mode, "", background.BlurPX, background.Zoom, nil
	}
	if strings.TrimSpace(background.AssetID) == "" || a.assets == nil || a.material == nil {
		return nil, "", "", 0, 0, fmt.Errorf("localized render: background requested but its asset resolver is not wired")
	}
	ref, err := a.assets.ResolveAsset(ctx, background.AssetID)
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("localized render: resolve background %q: %w", background.AssetID, err)
	}
	kind, ok := cliprender.BackgroundKindFromMediaType(ref.MediaType)
	if !ok {
		return nil, "", "", 0, 0, fmt.Errorf("localized render: background asset %q has media_type %q, which is not a renderable background plate (need %s or %s)", background.AssetID, ref.MediaType, cliprender.BackgroundKindImage, cliprender.BackgroundKindVideo)
	}
	materialized, err := a.material.Materialize(ctx, *ref)
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("localized render: materialize background %q: %w", background.AssetID, err)
	}
	return materialized, mode, kind, 0, 0, nil
}

// resolveRenderFolders resolves the Drive destination of ONE localized render
// and the folder of its subtitle artifact:
//
//	<clips root>/<job>/<language>
//	subtitles: <subtitle root>/<clip id>
//
// The language is a folder level, not only a filename component. A run renders
// the SAME clip once per language, so a flat destination leaves each language's
// MP4 distinguishable only by filename; the per-language level makes the
// layout state the language it holds.
//
// The language is a folder level, not only a filename component. A run renders
// the SAME clip once per language, so a flat destination left every language's
// MP4 in one folder with the languages distinguishable only by filename; the
// per-language level makes the layout state the language it holds. The level is
// created through the same FolderAdmin cache as the job level, so concurrent
// fan-out workers AND the post-crash recovery path (UploadRendered) converge on
// one folder per (destination, language) instead of racing to create duplicates.
//
// An empty language adds no level: there is no correct name to give it, and a
// nameless folder would be worse than the flat layout it replaces.
func (a *localizedRenderEnqueuerAdapter) resolveRenderFolders(ctx context.Context, in scriptgeneration.LocalizedRenderInput, clipID, language string) (string, string, error) {
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
	destination, err := a.resolveClipDestination(ctx, in)
	if err != nil {
		return "", "", err
	}
	// The language nests INSIDE the job folder so every clip of one language
	// stays grouped. The key reuses the RESOLVED parent id, so two runs that
	// share a job id still get one language folder each per parent.
	if lang := strings.TrimSpace(language); lang != "" {
		destination, err = a.resolveFolder(ctx, destination+"\x00"+lang, lang, destination)
		if err != nil {
			return "", "", fmt.Errorf("localized render: ensure Drive language folder %q: %w", lang, err)
		}
	}
	return destination, subtitle, nil
}

// resolveClipDestination resolves the folder a produced localized clip publishes
// into: the canonical clips root (or the payload's explicit drive_folder_id) with
// the payload's drive_subfolder_name. Google Docs was retired, so this clips
// root is the only destination; there is no documents tree any more.
func (a *localizedRenderEnqueuerAdapter) resolveClipDestination(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (string, error) {
	destination := strings.TrimSpace(a.cfg.FolderID)
	if value := strings.TrimSpace(in.Render.DriveFolderID); value != "" {
		destination = value
	}
	subfolder := strings.TrimSpace(in.Render.DriveSubfolderName)
	if subfolder == "" {
		return destination, nil
	}
	resolved, err := a.resolveFolder(ctx, destination+"\x00"+subfolder, subfolder, destination)
	if err != nil {
		return "", fmt.Errorf("localized render: ensure Drive subfolder %q: %w", subfolder, err)
	}
	return resolved, nil
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
		UploadSubtitleArtifact: built.generatedSubtitles && !in.SkipDrivePublication,
		SkipDrivePublication:   in.SkipDrivePublication,
		Watermark:              built.watermark, WatermarkSpec: built.watermarkSpec,
		Background: built.background, BackgroundMode: built.backgroundMode, BackgroundKind: built.backgroundKind,
		BackgroundBlurPX: built.backgroundBlurPX, BackgroundZoom: built.backgroundZoom,
		Overlays:               built.overlays,
		ForegroundScalePercent: in.Render.ForegroundScalePercent,
		SourceFrame:            renderSourceFrame(in.Render.SourceFrame),
		Effects:                renderClipEffects(in.Render.Effects),
		Grid:                   renderClipGrid(in.Render.Grid),
		SubtitlesStyle:         subtitleStyle(in),
		OnRendered: func(artifact localization.LocalizedClipArtifact) error {
			if in.OnRenderReady == nil {
				return nil
			}
			return in.OnRenderReady(scriptgeneration.LocalizedRenderResult{
				SceneID: artifact.SceneID, SceneIndex: in.SceneIndex, Language: scriptgeneration.Language(artifact.Language),
				RenderJobID: artifact.PlanRevision,
				Reused:      artifact.Reused, RenderSource: artifact.RenderSource,
				ClipID: artifact.ClipID, AssetID: artifact.AssetID, SHA256: artifact.SHA256, DurationMS: artifact.DurationMS,
				DriveFolderID: artifact.DriveFolderID,
				LocalPath:     artifact.LocalPath, Status: string(artifact.Status), Metrics: metricsMapFromJSON(artifact.MetricsJSON), StartedAt: time.Now().UTC(),
			})
		},
	}
}

// validateLocalizedRenderEffects is the single early gate for the native clip
// treatment schema. cliprender owns the bounds; this projection only adapts
// the kernel wire shape, so there is no second catalog to drift.
func validateLocalizedRenderEffects(in scriptgeneration.LocalizedRenderInput) error {
	if err := cliprender.ValidateClipEffects(renderClipEffects(in.Render.Effects), renderClipGrid(in.Render.Grid)); err != nil {
		return fmt.Errorf("localized render: invalid effects/grid: %w", err)
	}
	return nil
}

// validateLocalizedRenderBackground applies cliprender's blurred-plate
// contract to the caller's output.render.background: the treatment is legal
// only for mode=blur_source and only inside the certified bounds. The numbers
// live in cliprender so script.generate and clip.render cannot disagree.
func validateLocalizedRenderBackground(in scriptgeneration.LocalizedRenderInput) error {
	bg := in.Render.Background
	if bg == nil {
		return nil
	}
	mode := bg.Mode
	if mode == "" {
		mode = cliprender.BackgroundModeNone
	}
	if err := cliprender.ValidateBackgroundTreatment(mode, bg.BlurPX, bg.Zoom); err != nil {
		return fmt.Errorf("localized render: background: %w", err)
	}
	return nil
}

func renderClipEffects(in []scriptpkg.VideoClipEffectSpec) []cliprender.ClipEffectSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]cliprender.ClipEffectSpec, len(in))
	for i, effect := range in {
		out[i] = cliprender.ClipEffectSpec{
			Type: effect.Type, Amount: effect.Amount, Radius: effect.Radius, Softness: effect.Softness,
			BlockSize: effect.BlockSize, Size: effect.Size, Angle: effect.Angle, Intensity: effect.Intensity,
			Amplitude: effect.Amplitude, Wavelength: effect.Wavelength, Seed: effect.Seed, Animated: effect.Animated,
			Curvature: effect.Curvature, Scale: effect.Scale, Threshold: effect.Threshold, Speed: effect.Speed,
		}
	}
	return out
}

func renderClipGrid(in *scriptpkg.VideoClipGridSpec) *cliprender.ClipGridSpec {
	if in == nil {
		return nil
	}
	return &cliprender.ClipGridSpec{
		Scope: in.Scope, Style: in.Style, SpacingPX: in.SpacingPX, LineWidthPX: in.LineWidthPX,
		DotRadiusPX: in.DotRadiusPX, Color: in.Color, Opacity: in.Opacity,
	}
}

// renderSourceFrame projects the script boundary's card declaration onto the
// clip.render plan's sealed block. It is a field-for-field projection: the
// kernel shape owns the wire keys (output.render.source_frame), the plan owns
// the render semantics. A nil block stays nil (bare clip).
func renderSourceFrame(in *scriptpkg.VideoSourceFrameSpec) *cliprender.PlanSourceFrame {
	if in == nil {
		return nil
	}
	out := &cliprender.PlanSourceFrame{ClipRadiusPX: in.ClipRadiusPX}
	if in.Border != nil {
		out.Border = &cliprender.PlanFrameBorder{
			WidthPX: in.Border.WidthPX, Color: in.Border.Color, RadiusPX: in.Border.RadiusPX,
		}
	}
	if in.Shadow != nil {
		out.Shadow = &cliprender.PlanFrameShadow{
			Color: in.Shadow.Color, Opacity: in.Shadow.Opacity, BlurPX: in.Shadow.BlurPX,
			OffsetXP: in.Shadow.OffsetXP, OffsetYP: in.Shadow.OffsetYP,
		}
	}
	if in.Stroke != nil {
		out.Stroke = &cliprender.PlanFrameStroke{WidthPX: in.Stroke.WidthPX, Color: in.Stroke.Color}
	}
	return out
}

func subtitleStyle(in scriptgeneration.LocalizedRenderInput) *scriptpkg.VideoVisualStyleSpec {
	if in.Render.Subtitles == nil {
		return nil
	}
	return in.Render.Subtitles.Style
}

// ── Canonical media-SSOT write for a produced localized clip ──────────
//
// godlike/06 SSOT: a produced clip is not "done" when it reaches Drive. The
// row is written through the canonical AssetCommitter, which is the only
// producer of the asset.index.requested outbox event, so the render is
// discoverable by asset id instead of being re-derived at runtime. This lives
// beside the request builder (same adapter, same feature) rather than in a new
// file because internal/app/wiring is a registered hotspot whose production
// file count must not grow.

// renderedAssetIDPrefix namespaces a rendered clip in the media SSOT. It is
// the SAME identity the direct clip.render publisher mints, so a localized
// script render and a direct render converge on one asset lineage instead of
// producing a second, Drive-only asset.
const renderedAssetIDPrefix = "cliprender_"

// renderedAssetIDSHAChars is how many leading hex characters of the content
// hash identify a rendered asset. A shorter digest cannot mint the canonical
// identity at all, so the commit fails closed rather than slicing past the end.
const renderedAssetIDSHAChars = 24

// canonicalRenderedAssetID returns the content-addressed media SSOT id for a
// produced render, or ok=false when the digest is too short to identify one.
func canonicalRenderedAssetID(sha256Hex string) (string, bool) {
	sha := strings.ToLower(strings.TrimSpace(sha256Hex))
	if len(sha) < renderedAssetIDSHAChars {
		return "", false
	}
	return renderedAssetIDPrefix + sha[:renderedAssetIDSHAChars], true
}

// commitLocalizedRenderAsset registers the uploaded localization artifact in
// the PostgreSQL media SSOT through the canonical AssetCommitter and returns
// the canonical content-addressed asset id it committed.
//
// It returns ("", nil) when no committer is wired: hermetic/unit compositions
// exercise the Drive projection without the media plane, and production wiring
// rejects that configuration before installing the enqueuer. When a committer
// IS wired the commit is fail-closed — an unusable digest or a commit error
// aborts the enqueue instead of reporting a render that was never persisted.
func (a *localizedRenderEnqueuerAdapter) commitLocalizedRenderAsset(ctx context.Context, in scriptgeneration.LocalizedRenderInput, artifact localization.LocalizedClipArtifact) (string, error) {
	if a == nil || a.committer == nil {
		// Never use this branch in the live runtime.
		return "", nil
	}
	assetID, ok := canonicalRenderedAssetID(artifact.SHA256)
	if !ok {
		return "", fmt.Errorf("rendered artifact has unusable SHA-256 %q (need at least %d hex chars to mint %s<prefix>)",
			artifact.SHA256, renderedAssetIDSHAChars, renderedAssetIDPrefix)
	}
	sha := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	filename := artifact.ClipID + "." + artifact.Language + "." + sha[:12] + ".mp4"
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	if err != nil {
		return "", fmt.Errorf("resolve rendered localization taxonomy: %w", err)
	}
	searchText := strings.Join([]string{artifact.ClipID, artifact.Language, strings.TrimSpace(in.Text)}, " ")
	location := asset.LocationCommit{
		Kind: "drive", Provider: "google_drive", ExternalID: artifact.DriveFileID,
		WebViewLink: artifact.DriveLink, MimeType: "video/mp4", FileSizeBytes: artifact.SizeBytes,
		IsPrimary: true,
	}
	if in.SkipDrivePublication {
		// No Drive publication happened: the content address is the address.
		storageKey := strings.ToLower(strings.TrimSpace(artifact.StorageKey))
		if storageKey == "" {
			storageKey = sha
		}
		location = asset.LocationCommit{
			Kind: string(asset.LocationKindObjectStorage), Provider: "objectstore", ExternalID: storageKey,
			URI: artifact.ArtifactURL, MimeType: "video/mp4", FileSizeBytes: artifact.SizeBytes,
			ContentSHA256: sha, IsPrimary: true,
		}
	}
	_, err = a.committer.CommitAsset(ctx, persistence.AssetCommitRequest{
		AssetID:        assetID,
		Source:         "script.localized_render",
		Name:           filename,
		Filename:       filename,
		MediaType:      "video",
		Category:       "clip-render",
		DurationMs:     artifact.DurationMS,
		ContentHash:    sha,
		SearchText:     searchText,
		LifecycleState: "ACTIVE",
		IndexState:     "DISCOVERED",
		FolderID:       artifact.DriveFolderID,
		SourceURL:      artifact.ClipID,
		Rendition:      "rendered",
		Title:          filename,
		SourceProvider: "pipelinegen",
		Taxonomy:       taxonomy,
		Metadata: persistence.TypedMetadata{
			Title:         filename,
			Origin:        "script.localized_render",
			SourceVersion: sha,
			PublishAction: "script.generate",
			SizeBytes:     artifact.SizeBytes,
			Extra: map[string]any{
				"source_asset_id": artifact.ClipID,
				"script_run_id":   in.RunID,
				"scene_id":        artifact.SceneID,
				"language":        artifact.Language,
				"drive_file_id":   artifact.DriveFileID,
				"drive_link":      artifact.DriveLink,
				"delivery_status": "completed",
			},
		},
		Locations:      []asset.LocationCommit{location},
		EmitIndexEvent: true,
	})
	if err != nil {
		return "", fmt.Errorf("commit localized render %q: %w", assetID, err)
	}
	return assetID, nil
}

type localizedRenderIdentity struct {
	assetID     string
	clipID      string
	clipIDChild string
	sourceLang  string
	targetLang  string
}

func (a *localizedRenderEnqueuerAdapter) resolveRenderIdentity(ctx context.Context, in scriptgeneration.LocalizedRenderInput) (localizedRenderIdentity, error) {
	assetID := strings.TrimSpace(in.ClipAssetID)
	if assetID == "" {
		assetID = strings.TrimSpace(in.ClipID)
	}
	clipID := strings.TrimSpace(in.ClipID)
	if clipID == "" {
		clipID = assetID
	}
	sourceLang := string(in.SourceLanguage)
	if sourceLang == "" {
		sourceLang = strings.TrimSpace(a.cfg.SourceLanguage)
	}
	targetLang := string(in.Language)
	if targetLang == "" {
		targetLang = sourceLang
	}
	if sourceLang == "" || targetLang == "" {
		return localizedRenderIdentity{}, fmt.Errorf("localized render: source and target language are required (scene %q)", in.SceneID)
	}
	resolved, err := a.resolveExistingSubtitleLanguage(ctx, assetID, sourceLang)
	if err != nil {
		return localizedRenderIdentity{}, err
	}
	if resolved != "" {
		sourceLang = resolved
	}
	// The requested language is validated (and, when missing, materialized)
	// after the source transcript exists: see ensureDatabaseSubtitles. Keeping
	// the check there means a missing target can be created on demand instead
	// of aborting the render before the canonical backfill pipeline runs.
	return localizedRenderIdentity{assetID: assetID, clipID: clipID, clipIDChild: clipID, sourceLang: sourceLang, targetLang: targetLang}, nil
}

var _ texttracks.TimedCueWriter = (*localizedRenderEnqueuerAdapter)(nil)
