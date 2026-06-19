// Package realtime is the backward-compatibility shim for
// internal/media/realtime. The canonical implementation now lives in
// internal/application/realtime (PR-D.3 migration). Existing callers
// keep working unchanged; new code MUST import
// internal/application/realtime directly.
//
// Coverage mirrors every public identifier exported from the 7 source
// + 2 test files that lived in media/realtime (service, types,
// search_clips, match, index_health, embedding_adapter, job_adapter +
// 2 _test.go). Private helpers stay with the implementation.
package realtime

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	appassoc "github.com/Marcuss-ops/PipelineGen/internal/application/realtime"
)

// ── Type aliases ────────────────────────────────────────────────────

type (
	Service               = appassoc.Service
	JobServiceAdapter     = appassoc.JobServiceAdapter
	PythonEmbeddingAdapter = appassoc.PythonEmbeddingAdapter

	EmbeddingClient  = appassoc.EmbeddingClient
	JobService       = appassoc.JobService
	IndexHealthClips = appassoc.IndexHealthClips
	IndexHealthOutbox = appassoc.IndexHealthOutbox

	MatchRequest      = appassoc.MatchRequest
	MatchResponse     = appassoc.MatchResponse
	MatchAsset        = appassoc.MatchAsset
	VisualAnalysisResult = appassoc.VisualAnalysisResult
)

// ── Const re-exports ─────────────────────────────────────────────────

const (
	IndexHealthSampleCap = appassoc.IndexHealthSampleCap
	IndexHealthTimeout   = appassoc.IndexHealthTimeout
)

// ── Function re-exports ─────────────────────────────────────────────

// NewService mirrors appassoc.NewService verbatim. Concrete-typed callers
// pass the same *vectorstore.Service, *reranker.Client, and *zap.Logger
// with the same configurables and narrow interface seams
// (IndexHealthClips, IndexHealthOutbox) as before.
func NewService(
	vectorSvc *vectorstore.Service,
	embedder EmbeddingClient,
	jobSvc JobService,
	rerankerClient *reranker.Client,
	rerankCfg config.RerankerConfig,
	cfg *config.VectorSearchConfig,
	clips IndexHealthClips,
	outbox IndexHealthOutbox,
	log *zap.Logger,
) *Service {
	return appassoc.NewService(vectorSvc, embedder, jobSvc, rerankerClient, rerankCfg, cfg, clips, outbox, log)
}

// NewJobServiceAdapter mirrors appassoc.NewJobServiceAdapter verbatim.
// The canonical constructor requires a *jobs.Service and a *zap.Logger
// to bind the adapter's enqueue / cancel paths and lifecycle logging;
// pass-through forwarding keeps caller type compatibility exact.
func NewJobServiceAdapter(svc *jobs.Service, log *zap.Logger) *JobServiceAdapter {
	return appassoc.NewJobServiceAdapter(svc, log)
}

// NewPythonEmbeddingAdapter mirrors appassoc.NewPythonEmbeddingAdapter.
// The canonical constructor takes a single serverURL string (the HTTP
// endpoint that the Python bridge listens on) — no separate timeout arg.
func NewPythonEmbeddingAdapter(serverURL string) *PythonEmbeddingAdapter {
	return appassoc.NewPythonEmbeddingAdapter(serverURL)
}
