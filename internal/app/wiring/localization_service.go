package wiring

// localization_service.go is the localization composition root: it wires the
// full fan-out — source resolution, plan building, the bounded render
// scheduler, Drive upload, and Google Docs assembly — into one service with a
// single Localize entry point.
//
// Pipeline (per call):
//
//	LocalizeInput (asset_id + source language + ordered request)
//	  → SourceResolver.ResolveSource       (verified SHA-256 + duration + fps)
//	  → SourceInput
//	  → PlanBuilder.Build                  ([]LocalizedClipPlan, fingerprinted)
//	  → Service.Localize                   (render → upload → docs)
//	  → LocalizeResult
//
// godlike/06 SSOT (one canonical owner per fact): this is the SINGLE wiring
// of the localization capability. It composes the capability ports with the
// already-wired concrete adapters (asset registry, text-track store, Drive
// publisher, Doc client, Rust render boundary) — no second path, no direct
// FFmpeg/ffprobe, no duplicated Drive/docs logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	clipadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	localizationadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// LocalizationConfig pins the deployment-scoped render facts every
// LocalizedClipPlan fingerprints. These are resolved ONCE at the composition
// root and shared by every localized render — never re-derived per plan.
type LocalizationConfig struct {
	// SourceLanguage is the canonical BCP-47 language of the source clip
	// (the transcript track the source-language plan burns).
	SourceLanguage string
	// OutputProfileHash identifies the canonical render output profile
	// (codec/geometry). Folded into every plan fingerprint.
	OutputProfileHash string
	// RendererVersion pins the renderer binary/behavior version.
	RendererVersion string
	// SubtitleStyleHash is the canonical ASS style + generator hash baked
	// into every burned .ass.
	SubtitleStyleHash string
	// EncoderPolicyHash is the canonical 64-hex SHA-256 of the encoder
	// policy (preset / CRF / pixel format) applied to every render.
	EncoderPolicyHash string
	// WorkDir is the scratch root where rendered outputs + subtitle ASS land.
	WorkDir                 string
	GlobalRenderConcurrency int
	UploadConcurrency       int
}

// LocalizationRendererVersion is the canonical renderer version for the
// localized clip render boundary. It is a fingerprint input: bumping it
// invalidates every cached localized artifact.
const LocalizationRendererVersion = "rust-render/localization-v1"

// LocalizationSubtitleStyleHash is the canonical ASS style identifier for
// localized subtitle burn. It is a fingerprint input (changing the style
// bumps every variant fingerprint) and a valid ASS style name.
const LocalizationSubtitleStyleHash = "vidrush-default"

// LocalizationService is the composition-root facade for the localization
// fan-out. It is immutable after construction and safe for concurrent
// Localize calls.
type LocalizationService struct {
	sources localization.SourceResolver
	plans   localization.PlanBuilder
	service *localization.Service
	cfg     LocalizationConfig
}

// LocalizationDeps are the capability ports the composition root wires. Each
// port is satisfied by a concrete adapter already built from the canonical
// infrastructure (see BuildLocalizationService).
type LocalizationDeps struct {
	Sources          localization.SourceResolver
	TrackResolver    localization.TrackResolver
	SubtitleResolver localization.SubtitleResolver
	SubtitleCompiler localization.SubtitleArtifactCompiler
	Executor         localization.RenderPlanExecutor
	Uploader         localization.DriveUploader
	DocPublisher     localization.DocPublisher
}

// NewLocalizationService wires the capability objects from the resolved
// ports. Fail-closed: every port is mandatory — a service that cannot
// resolve, build plans, render, upload, or assemble can never complete a
// fan-out.
func NewLocalizationService(deps LocalizationDeps, cfg LocalizationConfig) (*LocalizationService, error) {
	compiler, err := localization.NewLocalizedClipCompiler(deps.Sources, localization.CompilerConfig{
		WorkDir:           cfg.WorkDir,
		EncoderPolicyHash: cfg.EncoderPolicyHash,
	})
	if err != nil {
		return nil, fmt.Errorf("localization service: compiler: %w", err)
	}
	wire, err := localization.NewSubtitleWire(deps.SubtitleResolver, deps.SubtitleCompiler, cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("localization service: subtitle wire: %w", err)
	}
	renderer, err := localization.NewLocalizedClipRenderer(compiler, wire, deps.Executor)
	if err != nil {
		return nil, fmt.Errorf("localization service: renderer: %w", err)
	}
	drivePublisher, err := localization.NewDrivePublisher(deps.Uploader)
	if err != nil {
		return nil, fmt.Errorf("localization service: drive publisher: %w", err)
	}
	assembler, err := localization.NewDocumentAssembler(deps.DocPublisher)
	if err != nil {
		return nil, fmt.Errorf("localization service: document assembler: %w", err)
	}
	renderConcurrency := cfg.GlobalRenderConcurrency
	if renderConcurrency < 1 {
		renderConcurrency = 2
	}
	uploadConcurrency := cfg.UploadConcurrency
	if uploadConcurrency < 1 {
		uploadConcurrency = 4
	}
	svc, err := localization.NewServiceWithConcurrency(renderer, drivePublisher, assembler, renderConcurrency, uploadConcurrency)
	if err != nil {
		return nil, fmt.Errorf("localization service: service: %w", err)
	}
	plans, err := localization.NewLocalizationPlanBuilder(deps.TrackResolver)
	if err != nil {
		return nil, fmt.Errorf("localization service: plan builder: %w", err)
	}
	return &LocalizationService{sources: deps.Sources, plans: plans, service: svc, cfg: cfg}, nil
}

// LocalizeInput is the fully-resolved input for one localization fan-out.
type LocalizeInput struct {
	// AssetID is the canonical source clip asset id.
	AssetID string
	// JobID correlates the fan-out to its enclosing Master job (may be empty).
	JobID string
	// SceneID is the editorial scene (optional for standalone clips).
	SceneID string
	// ClipID overrides the plan ClipID; empty means "use AssetID".
	ClipID string
	// SourceLanguage overrides LocalizationConfig.SourceLanguage for this call
	// (clips may differ in language); empty falls back to the config value.
	SourceLanguage string
	Watermark      *cliprender.MaterializedAsset
	WatermarkSpec  *cliprender.WatermarkSpec
	WatermarkText  string

	// Background is the resolved background layer: the materialized asset
	// (non-nil ONLY for mode=asset) plus the request-level mode
	// (none | blur_source | asset).
	Background     *cliprender.MaterializedAsset
	BackgroundMode string

	// SubtitlesStyle carries the caller's explicit subtitle visual overrides
	// (color, size, shadow, transition) into the sealed render plan.
	SubtitlesStyle *scriptpkg.VideoVisualStyleSpec

	ForegroundScalePercent int

	// Request is the ordered language fan-out + render concurrency.
	Request localization.LocalizationRequest

	// FolderID is the Drive folder the rendered clips upload into.
	FolderID string
	// SubtitleFolderID is the resolved per-clip Drive folder for the ASS.
	SubtitleFolderID       string
	UploadSubtitleArtifact bool
	OnRendered             func(localization.LocalizedClipArtifact) error
	// DocTitle / DocFolderID / DocIdempotencyKey / DocForce configure the
	// localization manifest Google Doc.
	DocTitle          string
	DocFolderID       string
	DocIdempotencyKey string
	DocForce          bool
	// SkipDocument keeps clip localization limited to MP4 render/upload. The
	// script-generation runner publishes the single final Google Doc.
	SkipDocument bool
}

// Localize runs the full fan-out: resolve the source facts, build the
// fingerprinted plans, then render + upload + assemble. Fail-closed: an
// invalid request, an unresolvable source, or a plan-build failure aborts
// before any render starts; per-language render/upload failures are recorded
// on the result without aborting the other languages.
func (s *LocalizationService) Localize(ctx context.Context, in LocalizeInput) (*localization.LocalizeResult, error) {
	if s == nil || s.sources == nil || s.plans == nil || s.service == nil {
		return nil, fmt.Errorf("localization service is not initialized")
	}
	if err := in.Request.Validate(); err != nil {
		return nil, fmt.Errorf("localization: localize: %w", err)
	}

	sourceLanguage := in.SourceLanguage
	if sourceLanguage == "" {
		sourceLanguage = s.cfg.SourceLanguage
	}

	facts, err := s.sources.ResolveSource(ctx, in.AssetID)
	if err != nil {
		return nil, fmt.Errorf("localization: localize: resolve source %q: %w", in.AssetID, err)
	}

	sourceInput := localization.SourceInput{
		JobID:                  in.JobID,
		SceneID:                in.SceneID,
		AssetID:                facts.AssetID,
		ClipID:                 in.ClipID,
		SourceLanguage:         sourceLanguage,
		SourceSHA256:           facts.SHA256,
		DurationMS:             facts.DurationMS,
		OutputProfileHash:      s.cfg.OutputProfileHash,
		RendererVersion:        s.cfg.RendererVersion,
		SubtitleStyleHash:      s.resolveSubtitleStyleHash(in.SubtitlesStyle),
		Watermark:              in.Watermark,
		WatermarkSpec:          in.WatermarkSpec,
		WatermarkText:          in.WatermarkText,
		Background:             in.Background,
		BackgroundMode:         in.BackgroundMode,
		ForegroundScalePercent: in.ForegroundScalePercent,
		SubtitlesStyle:         in.SubtitlesStyle,
	}

	plans, err := s.plans.Build(ctx, sourceInput, in.Request.Languages)
	if err != nil {
		return nil, fmt.Errorf("localization: localize: build plans: %w", err)
	}

	return s.service.Localize(ctx, localization.LocalizeInput{
		Concurrency:            in.Request.RenderConcurrency,
		FolderID:               in.FolderID,
		SubtitleFolderID:       in.SubtitleFolderID,
		UploadSubtitleArtifact: in.UploadSubtitleArtifact,
		DocTitle:               in.DocTitle,
		DocFolderID:            in.DocFolderID,
		DocIdempotencyKey:      in.DocIdempotencyKey,
		DocForce:               in.DocForce,
		SkipDocument:           in.SkipDocument,
		Plans:                  plans,
	})
}

func (s *LocalizationService) resolveSubtitleStyleHash(style *scriptpkg.VideoVisualStyleSpec) string {
	if s == nil || style == nil || strings.TrimSpace(style.Font) == "" {
		if s != nil && s.cfg.SubtitleStyleHash != "" {
			return s.cfg.SubtitleStyleHash
		}
		return LocalizationSubtitleStyleHash
	}
	font := strings.ToLower(strings.TrimSpace(style.Font))
	return fmt.Sprintf("%s-%s", LocalizationSubtitleStyleHash, font)
}

// UploadRendered republishes a locally certified artifact without invoking
// the renderer. It is used only by crash/retry recovery of localized clips.
func (s *LocalizationService) UploadRendered(ctx context.Context, artifact localization.LocalizedClipArtifact, folderID string) (localization.LocalizedClipArtifact, error) {
	if s == nil || s.service == nil {
		return artifact, fmt.Errorf("localization service: upload-only service is not wired")
	}
	return s.service.UploadRendered(ctx, artifact, folderID)
}

// BuildLocalizationService is the composition-root factory: it wires the
// concrete adapters from the *ComposeRoot into the localization
// capability. Fail-closed: a missing dependency (asset registry, text-track
// store, Drive publisher, Doc client, Rust render boundary, or media config)
// is a typed error, never a silently degraded fan-out.
func BuildLocalizationService(cfg *config.Config, root *ComposeRoot, log *zap.Logger) (*LocalizationService, error) {
	if root == nil {
		return nil, fmt.Errorf("localization service: composition root is nil")
	}
	if root.Repos == nil || root.Repos.Assets == nil {
		return nil, fmt.Errorf("localization service: asset registry is required")
	}
	if root.Drive == nil || root.Drive.Publisher == nil {
		return nil, fmt.Errorf("localization service: Drive publisher is required")
	}
	if root.Drive.DocClient == nil {
		return nil, fmt.Errorf("localization service: Drive Doc client is required")
	}
	if root.Repos.TextTrackRepo == nil {
		return nil, fmt.Errorf("localization service: text-track repository is required")
	}
	if root.MediaExec == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("localization service: resolved media execution config is required")
	}

	trackStore, ok := root.Repos.TextTrackRepo.(localizationTrackStore)
	if !ok {
		return nil, fmt.Errorf("localization service: text-track repository %T does not expose FindByID (subtitle PK lookup)", root.Repos.TextTrackRepo)
	}

	// Source resolution: reuse the SAME asset registry + materializer + probe
	// adapters the clip.render preparation phase uses (no second path).
	scratchDir := filepath.Join(cfg.Storage.TempPath(), "localization")
	resolver, resolverErr := clipadapters.NewClipRenderAssetResolver(root.Repos.Assets, log)
	if resolverErr != nil {
		return nil, fmt.Errorf("localization service: build asset resolver: %w", resolverErr)
	}
	var driveReader drive.Reader
	if root.Drive != nil {
		driveReader = root.Drive.Reader
	}
	materializer, materializerErr := clipadapters.NewClipRenderMaterializer(driveReader, scratchDir, log)
	if materializerErr != nil {
		return nil, fmt.Errorf("localization service: build asset materializer: %w", materializerErr)
	}
	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	sources := localizationadapters.NewSourceResolver(resolver, materializer, prober)

	// Subtitle ports: resolve the translated track by PK + compile the ASS via
	// the canonical texttracks.CompileASSContent generator.
	subtitleResolver := localizationadapters.NewSubtitleResolver(trackStore)
	subtitleCompiler := localizationadapters.NewSubtitleCompiler()

	// Rust/Chronon render boundary: reuse the composition-root runtime shared
	// with /api/clips/render. This is deliberately not rebuilt here: the
	// resolver, capability probe and native certifier are single owners.
	renderRuntime, runtimeErr := BuildClipRenderRuntime(cfg, root, log)
	if runtimeErr != nil {
		return nil, fmt.Errorf("localization service: build shared render runtime: %w", runtimeErr)
	}
	executor := localizationadapters.NewRenderPlanExecutor(renderRuntime.Executor, root.MediaExec.Profile, log)

	uploader := localizationadapters.NewDriveUploader(root.Drive.Publisher)
	docPublisher := localizationadapters.NewDocPublisher(root.Drive.DocClient)

	svcCfg := LocalizationConfigFromConfig(cfg)

	return NewLocalizationService(LocalizationDeps{
		Sources:          sources,
		TrackResolver:    localizationadapters.NewTrackResolver(trackStore),
		SubtitleResolver: subtitleResolver,
		SubtitleCompiler: subtitleCompiler,
		Executor:         executor,
		Uploader:         uploader,
		DocPublisher:     docPublisher,
	}, svcCfg)
}

// LocalizationConfigFromConfig resolves the deployment-scoped localization
// facts from the platform config. SourceLanguage defaults to the multilingual
// config (or "en"); OutputProfileHash and EncoderPolicyHash are deterministic
// SHA-256 folds of the resolved profile/policy so a config change bumps every
// plan fingerprint; SubtitleStyleHash and RendererVersion are the canonical
// constants; WorkDir is the canonical scratch root.
func LocalizationConfigFromConfig(cfg *config.Config) LocalizationConfig {
	sourceLanguage := "en"
	if cfg != nil && cfg.Media.Multilingual.SourceLanguage != "" {
		sourceLanguage = cfg.Media.Multilingual.SourceLanguage
	}

	mediaConfig := MediaexecConfig(cfg)
	profile := mediaConfig.Profile.WithDefaults()
	policy := mediaConfig.Policy

	workDir := "data/media/localized"
	if cfg != nil && cfg.Storage.TempPath() != "" {
		workDir = filepath.Join(cfg.Storage.TempPath(), "localization")
	}

	return LocalizationConfig{
		SourceLanguage:          sourceLanguage,
		OutputProfileHash:       canonicalFactHash(profile),
		RendererVersion:         LocalizationRendererVersion,
		SubtitleStyleHash:       LocalizationSubtitleStyleHash,
		EncoderPolicyHash:       canonicalFactHash(policy),
		WorkDir:                 workDir,
		GlobalRenderConcurrency: cfg.Scripts.LocalizedRenderGlobalConcurrency,
		UploadConcurrency:       cfg.Scripts.LocalizedRenderUploadConcurrency,
	}
}

// canonicalFactHash folds a deterministic fact struct into a 64-hex SHA-256.
// JSON field order is stable (struct field order), so the same value always
// folds to the same digest — the shape render.RenderExecutionPolicy and the
// plan fingerprint require.
func canonicalFactHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return digest.SHA256Bytes(b)
}

// localizationTrackStore is the narrow combined seam the composition root
// needs from the text-track repository: the canonical READY lookup (for plan
// building) plus the raw PK fetch (for subtitle resolution). The concrete
// *texttracks.TextTrackRepositorySQLite satisfies both.
type localizationTrackStore interface {
	detail.TextTrackRepository
	FindByID(ctx context.Context, trackID int64) (*detail.TextTrack, []detail.TimedCue, error)
}
