package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/transcripts"
	capyoutubeusecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	youtubeinfra "github.com/Marcuss-ops/PipelineGen/internal/platform/youtube"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// buildDomainScriptServices constructs the artifact service, image
// search resolver, and the canonical segment-selection resolver and
// populates the DomainBundle with them.
//
// godlike/06 SSOT: each service constructor is the canonical SOLE
// owner of its composition.
func buildDomainScriptServices(
	ctx context.Context,
	cfg *config.Config,
	dbs *Databases,
	log *zap.Logger,
	drive *DriveBundle,
	repos *RepoBundle,
	search *SearchBundle,
	process *ProcessBundle,
	ai *AIBundle,
	bundle *DomainBundle,
	imageSvc *imgservice.Service,
) error {
	artifactBlobStore, err := artifacts.NewLocalBlobStore(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("compose domains: artifact blob store: %w", err)
	}
	artifactRepo := artifacts.NewSQLiteRepository(dbs.DualPool.Writer)
	bundle.ArtifactService = artifacts.NewService(artifactBlobStore, artifactRepo, log)
	log.Info("P0.1: artifact blob service wired (content-addressed staging + verify + promote)",
		zap.String("data_dir", cfg.Storage.DataDir))

	// ImageSearchResolver: the orchestrator provides the concrete image
	// service so this helper stays free of the images import.
	imageSearchResolver, err := buildImageSearchResolver(imageSvc, repos.ImageRepo, log)
	if err != nil {
		return fmt.Errorf("compose images: %w", err)
	}
	bundle.ImageSearchResolver = imageSearchResolver

	// SegmentSelectionResolver: the canonical explicit|important strategy
	// owner behind POST /api/clips/process selection.mode. The resolver
	// owns NO publishing behaviour — it only maps the selection mode onto
	// []dto.Segment, which then flows through the SAME canonical
	// extraction pipeline (ExtractionService → extractFanOut →
	// ProcessYouTubeSegmentUseCase). This retires the former duplicate
	// extract-important ingest system (download/upload/hash/commit loop).
	extractDl := downloader.NewYTDLP(cfg)
	extractSubtitleSource := transcripts.NewCachingTranscriptProvider(
		youtubeinfra.NewYTDLPSubtitleAdapter(youtubeinfra.Deps{
			Ytdlp:      extractDl,
			CmdBuilder: ytdlp.NewCommandBuilder(cfg),
			UseCookies: cfg.External.ResolveYouTubeCookiesPath() != "",
			Log:        log,
		}),
	)
	transcriptFetcher := &transcriptFetcherAdapter{sub: extractSubtitleSource}
	// Analyzer is the nil-tolerant forward-pointer: failClosedAnalyzerAdapter
	// surfaces ErrAnalyzerUnavailable until the real LLM analyzer lands.
	analyzer := &failClosedAnalyzerAdapter{}
	segmentSelectionResolver := capyoutubeusecase.NewSegmentSelectionResolver(log, transcriptFetcher, analyzer)
	bundle.YoutubeClipService.SetSegmentSelectionResolver(segmentSelectionResolver)

	return nil
}

type docsPublishJobEnqueuer interface {
	Enqueue(context.Context, *job.EnqueueRequest) (*job.Job, error)
}

type docsPublishChildEnqueuer struct{ jobs docsPublishJobEnqueuer }

var _ scriptgen.DocsPublishEnqueuer = (*docsPublishChildEnqueuer)(nil)

func (e *docsPublishChildEnqueuer) EnqueueDocsPublish(ctx context.Context, req scriptgen.DocsPublishEnqueue) error {
	if e == nil || e.jobs == nil {
		return fmt.Errorf("docs publish child enqueue: jobs service is not wired")
	}
	parentID, runID := strings.TrimSpace(req.ParentJobID), strings.TrimSpace(req.RunID)
	if parentID == "" || runID == "" {
		return fmt.Errorf("docs publish child enqueue: parent job id and run id are required")
	}
	payload, err := json.Marshal(map[string]string{
		"parent_job_id": parentID, "parent_job_type": scriptpkg.TypeGenerate, "run_id": runID,
	})
	if err != nil {
		return fmt.Errorf("docs publish child enqueue: encode payload: %w", err)
	}
	payload = job.InjectParentLink(payload, job.ParentLink{ParentJobID: parentID, ParentRunID: runID})
	var linkedPayload map[string]any
	if err := json.Unmarshal(payload, &linkedPayload); err != nil {
		return fmt.Errorf("docs publish child enqueue: decode linked payload: %w", err)
	}
	correlationID := strings.TrimSpace(req.CorrelationID)
	if correlationID == "" {
		correlationID = parentID + ":docs:" + runID
	}
	child, err := e.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type: scriptpkg.TypeDocsPublish, Payload: linkedPayload, CorrelationID: correlationID,
		ActiveKey:  "docs-publish:" + parentID + ":" + runID,
		MaxRetries: appjobs.Compose().DefaultMaxRetries(scriptpkg.TypeDocsPublish),
	})
	if err != nil {
		return fmt.Errorf("docs publish child enqueue: %w", err)
	}
	if child == nil || strings.TrimSpace(child.ID) == "" {
		return fmt.Errorf("docs publish child enqueue: jobs service returned an empty child")
	}
	return nil
}
