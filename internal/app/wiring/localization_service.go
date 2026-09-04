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
// publisher, Doc client, RenderingGen/Chronon render boundary) — no second path, no direct
// FFmpeg/ffprobe, no duplicated Drive/docs logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	mediawiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
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
	SourceLanguage string
	OutputProfileHash string
	RendererVersion string
	SubtitleStyleHash string
	EncoderPolicyHash string
	WorkDir                 string
	GlobalRenderConcurrency int
	UploadConcurrency       int
}

const LocalizationRendererVersion = "chronon-render/localization-v1"
const LocalizationSubtitleStyleHash = "vidrush-default"

type LocalizationService struct {
	sources localization.SourceResolver
	plans   localization.PlanBuilder
	service *localization.Service
	cfg     LocalizationConfig
}

type LocalizationDeps struct {
	Sources          localization.SourceResolver
	TrackResolver    localization.TrackResolver
	SubtitleResolver localization.SubtitleResolver
	SubtitleCompiler localization.SubtitleArtifactCompiler
	Executor         localization.RenderPlanExecutor
	Uploader         localization.DriveUploader
	DocPublisher     localization.DocPublisher
}

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

type LocalizeInput struct {
	AssetID string
	JobID string
	SceneID string
	ClipID string
	SourceLanguage string
	Watermark      *cliprender.MaterializedAsset
	WatermarkSpec  *cliprender.WatermarkSpec
	WatermarkText  string
	Background     *cliprender.MaterializedAsset
	BackgroundMode string
	SubtitlesStyle *scriptpkg.VideoVisualStyleSpec
	ForegroundScalePercent int
	Request localization.LocalizationRequest
	FolderID string
	SubtitleFolderID       string
	UploadSubtitleArtifact bool
	OnRendered             func(localization.LocalizedClipArtifact) error
	DocTitle          string
	DocFolderID       string
	DocIdempotencyKey string
	DocForce          bool
	SkipDocument bool
}

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

func (s *LocalizationService) UploadRendered(ctx context.Context, artifact localization.LocalizedClipArtifact, folderID string) (localization.LocalizedClipArtifact, error) {
	if s == nil || s.service == nil {
		return artifact, fmt.Errorf("localization service: upload-only service is not wired")
	}
	return s.service.UploadRendered(ctx, artifact, folderID)
}

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

	subtitleResolver := localizationadapters.NewSubtitleResolver(trackStore)
	subtitleCompiler := localizationadapters.NewSubtitleCompiler()

	renderRuntime, runtimeErr := BuildClipRenderRuntime(cfg, root, log)
	if runtimeErr != nil {
		return nil, fmt.Errorf("localization service: build shared render runtime: %w", runtimeErr)
	}
	executor := localizationadapters.NewRenderPlanExecutor(renderRuntime.RenderingGenExecutor, root.MediaExec.Profile, log)

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

func LocalizationConfigFromConfig(cfg *config.Config) LocalizationConfig {
	sourceLanguage := "en"
	if cfg != nil && cfg.Media.Multilingual.SourceLanguage != "" {
		sourceLanguage = cfg.Media.Multilingual.SourceLanguage
	}

	mediaConfig := mediawiring.MediaexecConfig(cfg)
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

func canonicalFactHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return digest.SHA256Bytes(b)
}

type localizationTrackStore interface {
	detail.TextTrackRepository
	FindByID(ctx context.Context, trackID int64) (*detail.TextTrack, []detail.TimedCue, error)
}
