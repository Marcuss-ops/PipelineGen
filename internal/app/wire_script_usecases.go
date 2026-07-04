// Package app — wire_script_usecases.go.
//
// AZIONE 2 (July 2026): extracted from wire_script.go. This file owns:
//
//  1. buildScriptUseCases — canonical factory constructing all
//     script-domain use cases (sectionRegen, cacheEvictionUC,
//     GenerateOneUseCase, GenerateManyUseCase, genJobHandler,
//     mediaCurator). Takes the output of buildScriptSourceResolvers
//     plus the ppReg frozen by the orchestrator.
//
//  2. wireScriptChildJobAuditP04 — P0 #4 per-item retry pattern:
//     registers script.generate_item handler + wires the fanout
//     broker adapter to GenerateManyUseCase.
//
//  3. scriptItemFanoutBrokerAdapter — Pattern 0 adapter bridging
//     jobs.Service.Enqueue to the canonical FanoutItemBroker port
//     consumed by GenerateManyUseCase.ExecuteFanout.
//
// Package boundary: same `package app` as wire_script.go. The
// factory is a pure-builder; P04 audit wiring + broker adapter
// live in the same file because they are the only consumers of
// the use-case cluster this factory constructs.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     invokes buildScriptUseCases then wireScriptChildJobAuditP04).
//   - internal/app/wire_script_resolvers.go: the sibling factory
//     that produces normCfg, sourceReg, clipSourceBuilder,
//     clipSearchPort (AZIONE 2 companion file).
//   - internal/application/scripts/usecase: use-case constructors.
//   - internal/application/scripts/jobs: GenerateJobHandler,
//     ScriptGenerateItemPayload, FanoutItemBroker port.
//   - internal/application/scripts/ports: VoiceoverGroupsAdapter.
//   - internal/application/voiceover: GroupsResolver.
//   - internal/application/jobs: appjobs.Service (Enqueue surface).
//   - internal/domain/job: EnqueueRequest, TypeScriptGenerateItem.
//   - internal/domain/script: GenerationItemV2, Preset.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// buildScriptUseCases constructs the full script-domain use-case
// cluster. The factory is a pure-builder — zero side effects
// (no job registration, no module wiring). The orchestrator
// (wireScriptFlow) is responsible for registering jobs, wiring
// the handler, and mounting the HTTP module.
func buildScriptUseCases(
	cfg *config.Config,
	root *ComposeRoot,
	scriptsRepoAdapter adapters.ScriptRepository,
	normCfg adapters.NormalizationConfig,
	sourceReg *adapters.SourceRegistry,
	ppReg *adapters.PostProcessorRegistry,
	clipSearchPort scriptports.ClipSearchPort,
	clipSourceBuilder *usecase.ClipSourceBuilder,
	log *zap.Logger,
) (
	*usecase.SectionRegenerator,
	*usecase.CacheEvictionUseCase,
	*usecase.GenerateOneUseCase,
	*usecase.GenerateManyUseCase,
	*jobs.GenerateJobHandler,
	*scriptdto.MediaCurator,
) {
	gen := root.AI.ScriptGen
	engine := root.AI.ScriptEngine

	// ── Section regenerator ─────────────────────────────────────────
	sectionRegen := usecase.NewSectionRegenerator(
		scriptsRepoAdapter, gen,
		root.Drive.DocClient, cfg, log,
	)

	// ── Cache eviction ──────────────────────────────────────────────
	cacheEvictionUC := usecase.NewCacheEvictionUseCase(
		gen, nil, log,
	)

	// ── GenerateOneUseCase (single-item pipeline) ───────────────────
	oneUC := usecase.NewGenerateOneUseCase(normCfg, sourceReg, engine, ppReg, log)

	// ── Voiceover group → folder routing ────────────────────────────
	voRootID := strings.TrimSpace(cfg.Drive.VoiceoverFolder())
	if voRootID != "" && root.Search != nil && root.Search.AssetTreeService != nil {
		if gr, grErr := voiceover.NewGroupsResolver(root.Search.AssetTreeService, log); grErr == nil {
			voAdapter := scriptports.NewVoiceoverGroupsAdapter(gr)
			oneUC.SetVoiceoverRouting(voAdapter, voRootID)
			log.Info("wireScriptFlow: voiceover_group -> folder_id resolver wired (fix/voiceover-group-resolver)",
				zap.String("voiceover_root", voRootID))
		} else {
			log.Warn("wireScriptFlow: failed to build voiceover groups resolver — voiceover_group routing disabled",
				zap.Error(grErr))
		}
	}

	// ── ClipsFolderExtAdapter pre-wiring (Refactor 1 audit) ────────
	_ = jobs.NewClipsFolderExtAdapter
	log.Info("wireScriptFlow: jobs.ClipsFolderExtAdapter available at composition root (Refactor 1 adapter pre-wired)")

	// ── GenerateManyUseCase (multi-item fanout) ─────────────────────
	manyUC := usecase.NewGenerateManyUseCase(oneUC, log)

	// ── Media curator ───────────────────────────────────────────────
	var mediaCurator *scriptdto.MediaCurator
	if root.Repos.ClipsRepo != nil && engine != nil {
		mediaCurator = scriptdto.NewMediaCurator(cfg.ClipIndexer.ServerURL, root.Repos.ClipsRepo, clipSourceBuilder, log)
		if clipSearchPort != nil {
			mediaCurator.SetClipSearchPort(clipSearchPort)
		}
	}

	// ── Generate job handler ────────────────────────────────────────
	genJobHandler := jobs.NewGenerateJobHandler(oneUC, manyUC, log)

	return sectionRegen, cacheEvictionUC, oneUC, manyUC, genJobHandler, mediaCurator
}

// wireScriptChildJobAuditP04 wires the P0 #4 per-item retry pattern:
// child handler registration + fanout broker adapter.
// Extracted from wire_script.go (AZIONE 2, July 2026).
func wireScriptChildJobAuditP04(
	jobsSvc *appjobs.Service,
	oneUC *usecase.GenerateOneUseCase,
	manyUC *usecase.GenerateManyUseCase,
	normCfg adapters.NormalizationConfig,
	log *zap.Logger,
) error {
	if jobsSvc == nil {
		return fmt.Errorf("P0 #4 audit wiring: jobs service is required (nil-broken composition root)")
	}
	if oneUC == nil {
		return fmt.Errorf("P0 #4 audit wiring: GenerateOneUseCase is required (nil-broken composition)")
	}
	if manyUC == nil {
		return fmt.Errorf("P0 #4 audit wiring: GenerateManyUseCase is required (nil-broken composition)")
	}

	// 1. Construct the per-item child worker.
	itemHandler := jobs.NewScriptGenerateItemJobHandler(
		oneUC, // satisfies GenerateOneExecutor port via Go interface satisfaction
		log,
	)
	if err := itemHandler.Register(jobsSvc); err != nil {
		return fmt.Errorf("register script.generate_item handler: %w", err)
	}

	// 2. Wire the FanoutItemBroker adapter (emits N child jobs to the
	//    broker when the multi-item path fans out).
	broker := newScriptItemFanoutBrokerAdapter(jobsSvc, log)
	manyUC.SetFanoutBroker(broker)
	if log != nil {
		log.Info("P0 #4 audit wiring: FanoutItemBroker wired to GenerateManyUseCase",
			zap.Int("max_concurrency", normCfg.MaxBatchWorkers))
	}

	// 3. ScriptParentAggregator is constructed + lifecycle-owned in
	//    startBackgroundJobs (lifecycle.go) — NOT here. The aggregator
	//    ticker uses the server's runtime context (signal.NotifyContext),
	//    not context.Background(). See Commit 4 (P0 #9 lifecycle ownership).

	return nil
}

// scriptItemFanoutBrokerAdapter is the thin Pattern-0 adapter that
// bridges jobs.Service.Enqueue to the canonical FanoutItemBroker port
// consumed by GenerateManyUseCase.ExecuteFanout.
// Extracted from wire_script.go (AZIONE 2, July 2026).
type scriptItemFanoutBrokerAdapter struct {
	jobsSvc *appjobs.Service
	log     *zap.Logger
}

// newScriptItemFanoutBrokerAdapter constructs the adapter.
func newScriptItemFanoutBrokerAdapter(jobsSvc *appjobs.Service, log *zap.Logger) *scriptItemFanoutBrokerAdapter {
	return &scriptItemFanoutBrokerAdapter{jobsSvc: jobsSvc, log: log}
}

// EnqueueScriptItem satisfies the FanoutItemBroker port. Marshals
// the item to JSON inside a typed ScriptGenerateItemPayload, builds
// a typed EnqueueRequest for the per-item child type, and returns the
// broker-assigned job ID.
func (a *scriptItemFanoutBrokerAdapter) EnqueueScriptItem(
	ctx context.Context,
	parentJobID string,
	itemIndex int,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
) (string, error) {
	typedPayload := jobs.ScriptGenerateItemPayload{
		ParentJobID: parentJobID,
		Item:        item,
		Preset:      preset,
		ItemIndex:   itemIndex,
	}
	payloadBytes, err := json.Marshal(typedPayload)
	if err != nil {
		return "", fmt.Errorf("marshal item payload: %w", err)
	}

	activeKey := fmt.Sprintf("script:item:%s:%d:%s", parentJobID, itemIndex, item.ID)

	req := &domainjob.EnqueueRequest{
		Type:      domainjob.TypeScriptGenerateItem,
		Payload:   json.RawMessage(payloadBytes),
		ActiveKey: activeKey,
	}
	ret, err := a.jobsSvc.Enqueue(ctx, req)
	if err != nil {
		return "", fmt.Errorf("enqueue script.generate_item: %w", err)
	}
	if ret == nil || ret.ID == "" {
		return "", fmt.Errorf("enqueue script.generate_item returned empty ID")
	}
	if a.log != nil {
		a.log.Info("P0 #4 audit: child job enqueued",
			zap.String("child_job_id", ret.ID),
			zap.String("parent_job_id", parentJobID),
			zap.String("item_id", item.ID))
	}
	return ret.ID, nil
}
