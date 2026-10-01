package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"go.uber.org/zap"
)

// VidRushMaterializationProcessor is the single acquisition boundary for
// provider candidates. Search processors only discover candidates; this
// processor is the only postprocessor allowed to acquire, verify and send a
// candidate through the common finalizer.
type VidRushMaterializationProcessor struct {
	providers *VidRushAssetProviderRegistry
	finalizer scriptports.VidRushArtifactFinalizer
	cache     scriptports.VidRushCachePort
	catalog   entitycatalog.Repository
	metrics   VidRushTimingMetrics
	sampler   scriptports.MediaSamplerPort
	log       *zap.Logger
}

// WithLogger makes materialization failures observable in live runs without
// changing the provider/finalizer ports or leaking infrastructure into them.
func (p *VidRushMaterializationProcessor) WithLogger(log *zap.Logger) *VidRushMaterializationProcessor {
	if p != nil {
		p.log = log
	}
	return p
}

type vidRushMaterializedSegment struct {
	result   scriptpkg.VidRushSegmentResult
	warnings []string
}

const (
	vidRushArtlistAcquireBudget = 3
	// Provider calls are bounded independently from the postprocessor context.
	// A failed remote provider must not consume the whole VidRush run while its
	// fallback scraper or browser waits on an unavailable upstream.
	// Browser-authenticated HLS downloads commonly need ~30 seconds before
	// the scraper returns the verified local artifact. Keep this below the
	// resolver's broader timeout while avoiding premature cancellation of a
	// valid Artlist acquisition.
	vidRushArtlistAcquireTimeout    = 120 * time.Second
	vidRushImageAcquireTimeout      = 15 * time.Second
	vidRushGenerationAcquireTimeout = 2 * time.Minute
	vidRushVerifyTimeout            = 20 * time.Second
	// Search providers routinely return a mixture of hotlink-protected,
	// corrupt and valid URLs. Keep the candidate set bounded upstream, but
	// allow enough acquisition attempts to reach the requested number of
	// durable verified images without ever promoting an unverified hit.
	// Search providers commonly place unusable hotlinks before downloadable
	// images. Keep trying the bounded candidate pool until the scene reaches
	// its target instead of turning the first few bad URLs into a false miss.
	vidRushImageAcquireSlack = 20
)

func NewVidRushMaterializationProcessor(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	return NewVidRushMaterializationProcessorWithCatalog(providers, finalizer, nil, nil, metrics...)
}

func NewVidRushMaterializationProcessorWithCatalog(providers *VidRushAssetProviderRegistry, finalizer scriptports.VidRushArtifactFinalizer, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushTimingMetrics) *VidRushMaterializationProcessor {
	var m VidRushTimingMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushMaterializationProcessor{providers: providers, finalizer: finalizer, cache: cache, catalog: catalog, metrics: m}
}

// WithMediaSampler installs the canonical selection port. Materialization
// requires this port before binding a primary video.
func (p *VidRushMaterializationProcessor) WithMediaSampler(sampler scriptports.MediaSamplerPort) *VidRushMaterializationProcessor {
	if p != nil {
		p.sampler = sampler
	}
	return p
}

func (p *VidRushMaterializationProcessor) Name() ProcessorName {
	return ProcessorVidRushMaterialization
}

func (p *VidRushMaterializationProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && (providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration)) {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

func (p *VidRushMaterializationProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p == nil {
		return nil, fmt.Errorf("vidrush materialization: processor not configured")
	}
	if result, handled := p.metadataOnlyResult(plan, input); handled {
		return result, nil
	}
	if allSegmentsFixedMedia(input.SpecScene, input.VidRushSegments) {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			cloned := CloneVidRushSegmentResult(segment)
			cloned.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
			segments = append(segments, cloned)
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, nil
	}
	if err := materializationDependenciesError(plan, input, p.providers, p.finalizer); err != nil {
		return nil, err
	}
	if p.providers == nil || p.finalizer == nil {
		return &PostProcessResult{}, nil
	}
	if err := requireVidRushEnabledProviders(plan, p.providers); err != nil {
		return nil, err
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}

	processed, err := concurrent.Map(ctx, input.VidRushSegments, materializationWorkers(plan), func(ctx context.Context, _ int, segment scriptpkg.VidRushSegmentResult) (vidRushMaterializedSegment, error) {
		if _, ok := stockBindingForSegment(plan, input.StockBindings, segment); ok {
			// A direct stock binding is this scene's authoritative visual
			// source: acquiring, verifying or persisting provider media for it
			// would leak the stock scene into provider search/materialization.
			// Mark the provider delta and keep the scene out of the workers.
			cloned := CloneVidRushSegmentResult(segment)
			cloned.Cache.Artlist = "BYPASSED"
			cloned.Cache.InternetImages = "BYPASSED"
			cloned.Cache.ImageGeneration = "BYPASSED"
			cloned.Cache.YouTube = "BYPASSED"
			cloned.Cache.Binding = "STOCK_BOUND"
			return vidRushMaterializedSegment{result: cloned}, nil
		}
		return p.materializeOne(ctx, plan, segment)
	})
	if err != nil {
		return nil, fmt.Errorf("vidrush materialization: bounded segment workers: %w", err)
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(processed))
	var warnings []string
	for _, item := range processed {
		segments = append(segments, item.result)
		warnings = append(warnings, item.warnings...)
	}

	// Internet-image discovery runs before materialization, when candidates
	// still have only remote provenance. Re-project entity bindings after the
	// common finalizer has persisted the assets so the SpecScene receives the
	// durable AssetID/DriveLink rather than the earlier not_found result.
	var entityImagePolicy mediadomain.EntityImagePolicy
	if plan != nil {
		entityImagePolicy = plan.MediaPlan.Extraction.EntityImages
		entityImagePolicy.Enabled = plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()
	}
	updatedSpecScene := projectEntityImageBindings(input.SpecScene, segments, entityImagePolicy)
	return &PostProcessResult{
		VidRushSegments:  segments,
		UpdatedSpecScene: updatedSpecScene,
		SpecSceneChanged: len(updatedSpecScene.Scenes) > 0,
		Warnings:         warnings,
		Changed:          true,
	}, nil
}

// Materialize implements the single-segment materialization boundary consumed
// by the incremental VidRush coordinator. It reuses materializeOne so the
// acquire → verify → finalize stage is implemented exactly once, and it
// returns an immutable result without mutating shared scene state.
func (p *VidRushMaterializationProcessor) Materialize(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if p == nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush materialization: processor not configured")
	}
	if result, handled := p.metadataOnlyResult(plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{segment}}); handled {
		return result.VidRushSegments[0], nil
	}
	if segment.ExecutionMode.IsFixedMedia() {
		cloned := CloneVidRushSegmentResult(segment)
		cloned.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		return cloned, nil
	}
	if _, stockBound := stockBindingForSegment(plan, nil, segment); stockBound {
		// The incremental coordinator materializes one segment at a time and
		// carries no ProcessInput bindings surface, so the plan is the only
		// stock-binding source here. A stock-bound segment must leave this
		// processor untouched exactly like the batch path.
		cloned := CloneVidRushSegmentResult(segment)
		cloned.Cache.Artlist = "BYPASSED"
		cloned.Cache.InternetImages = "BYPASSED"
		cloned.Cache.ImageGeneration = "BYPASSED"
		cloned.Cache.YouTube = "BYPASSED"
		cloned.Cache.Binding = "STOCK_BOUND"
		return cloned, nil
	}
	if err := materializationDependenciesError(plan, ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{segment}}, p.providers, p.finalizer); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.providers == nil || p.finalizer == nil {
		return CloneVidRushSegmentResult(segment), nil
	}
	if err := requireVidRushEnabledProviders(plan, p.providers); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.log != nil {
		driveFolderID := ""
		planTitle := ""
		planLanguage := ""
		if plan != nil {
			driveFolderID = strings.TrimSpace(plan.DriveFolderID)
			planTitle = plan.Title
			planLanguage = plan.Language
		}
		p.log.Info("VidRush materialization started", zap.String("segment_id", segment.SegmentID), zap.Int("candidates", len(segment.Assets.Candidates)), zap.Int("secondary_images", len(segment.Assets.SecondaryImages)), zap.String("mode", plan.MediaPlan.Materialization.Mode), zap.String("drive_folder_id", driveFolderID), zap.String("plan_title", planTitle), zap.String("plan_language", planLanguage))
	}
	out, err := p.materializeOne(ctx, plan, segment)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	if p.log != nil {
		p.log.Info("VidRush materialization completed", zap.String("segment_id", segment.SegmentID), zap.Int("selected_images", len(out.result.Assets.SecondaryImages)), zap.Int("materialized_candidates", len(out.result.Assets.Candidates)), zap.Strings("warnings", out.warnings))
	}
	return out.result, nil
}

func (p *VidRushMaterializationProcessor) hydrateEntityCatalogMaterialization(ctx context.Context, candidate scriptpkg.SegmentAssetCandidate) (scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.catalog == nil || !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
		return candidate, nil
	}
	if rawID := strings.TrimPrefix(strings.TrimSpace(candidate.AssetID), "entity-image-"); rawID != strings.TrimSpace(candidate.AssetID) {
		if candidateID, err := strconv.ParseInt(rawID, 10, 64); err == nil && candidateID > 0 {
			materialization, matErr := p.catalog.GetMaterialization(ctx, candidateID)
			if matErr != nil && !errors.Is(matErr, entitycatalog.ErrCandidateNotFound) {
				return candidate, matErr
			}
			if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
				return hydrated, nil
			}
		}
	}
	entityName := strings.TrimSpace(candidate.Entity)
	if entityName == "" || strings.TrimSpace(candidate.SourceURL) == "" {
		return candidate, nil
	}
	identity, err := entitycatalog.CanonicalizePersonName(entityName)
	if err != nil {
		return candidate, nil
	}
	rows, err := p.catalog.ListCandidates(ctx, identity.CanonicalEntityID, 100)
	if err != nil {
		if errors.Is(err, entitycatalog.ErrEntityNotFound) {
			return candidate, nil
		}
		return candidate, err
	}
	for _, row := range rows {
		if !strings.EqualFold(strings.TrimSpace(row.SourceURL), strings.TrimSpace(candidate.SourceURL)) {
			continue
		}
		materialization, matErr := p.catalog.GetMaterialization(ctx, row.ID)
		if matErr != nil {
			return candidate, matErr
		}
		if hydrated, ok := applyEntityImageCatalogMaterialization(candidate, materialization); ok {
			return hydrated, nil
		}
	}
	return candidate, nil
}

func (p *VidRushMaterializationProcessor) persistEntityCatalogMaterialization(ctx context.Context, discovered, persisted scriptpkg.SegmentAssetCandidate) error {
	if p == nil || p.catalog == nil || !strings.EqualFold(strings.TrimSpace(discovered.Provider), scriptpkg.VidRushProviderInternetImages) {
		return nil
	}
	candidateID, err := entityImageCatalogCandidateID(ctx, p.catalog, discovered)
	if err != nil || candidateID < 1 {
		return err
	}
	// A catalog-managed image is reusable only when its canonical materialization
	// is durable. Do not allow a successful VidRush result to hide a missing
	// reference-database row or incomplete Drive/hash identity.
	if strings.TrimSpace(persisted.AssetID) == "" || strings.TrimSpace(persisted.DriveLink) == "" || strings.TrimSpace(persisted.LegacyFileMD5) == "" {
		return fmt.Errorf("entity image catalog: materialized candidate %d is missing asset_id, drive_link, or legacy_file_md5", candidateID)
	}
	now := time.Now().UTC()
	if err := p.catalog.UpsertMaterialization(ctx, entitycatalog.Materialization{
		CandidateID: candidateID, AssetID: persisted.AssetID, LegacyFileMD5: persisted.LegacyFileMD5,
		DriveLink: persisted.DriveLink,
		Status:    entitycatalog.MaterializationStatusMaterialized,
		// No local path is recorded: the row is durable media metadata, and a
		// filesystem path is not durable media metadata.
		MaterializedAt: now, LastVerifiedAt: now,
	}); err != nil {
		return err
	}
	return p.catalog.SetCandidateStatus(ctx, candidateID, entitycatalog.CandidateStatusFresh)
}
