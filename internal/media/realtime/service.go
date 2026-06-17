package realtime

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/reranker"
)

type EmbeddingClient interface {
	EmbedText(ctx context.Context, text string) ([]float64, error)
	EmbedTextWithNormalized(ctx context.Context, text string) ([]float64, string, error)
	EmbedVisual(ctx context.Context, text string) ([]float64, error)
	EmbedAudio(ctx context.Context, text string) ([]float64, error)
}

type JobService interface {
	EnqueueMediaGeneration(ctx context.Context, query string, source string) (string, error)
}

type Service struct {
	vectorSvc *vectorstore.Service
	embedder  EmbeddingClient
	jobSvc    JobService
	reranker  *reranker.Client
	rerankCfg config.RerankerConfig
	cfg       *config.VectorSearchConfig
	log       *zap.Logger

	// IndexHealth cross-check inputs — narrow interface seams defined in
	// index_health.go (IndexHealthClips / IndexHealthOutbox). Concrete
	// *clips.Repository / *outbox.Repository satisfy these structurally,
	// so production callers compile unchanged. Both deps are nil-safe —
	// a nil dep falls back to zeros in IndexHealth + a soft warning log.
	clips  IndexHealthClips
	outbox IndexHealthOutbox

	cacheMu        sync.RWMutex
	embeddingCache map[string][]float32
}

// NewService constructs the real-time matching service.
//
// PR3-5b.4: the clips and outbox parameters are REQUIRED for the canonical
// IndexHealth cross-check (sqlite_assets / sqlite_indexed / qdrant_points /
// missing_in_qdrant / orphan_in_qdrant / pending_outbox / dead_letter). A
// nil value is tolerated — the handler will fall back to the legacy raw-SQL
// path AND log a WARN at startup so the wiring gap is visible — but the
// canonical cross-check requires both.
//
// All production wiring must pass non-nil. The append at the end of the
// parameter list keeps the existing 7-arg callers compiling; new callers
// (composeRealtimeService) thread them explicitly.
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
	if clips == nil && log != nil {
		log.Warn("realtime.NewService: clips repository is nil — IndexHealth will fall back to zeros")
	}
	if outbox == nil && log != nil {
		log.Warn("realtime.NewService: outbox repository is nil — pending_outbox/dead_letter will be reported as 0")
	}
	return &Service{
		vectorSvc:      vectorSvc,
		embedder:       embedder,
		jobSvc:         jobSvc,
		reranker:       rerankerClient,
		rerankCfg:      rerankCfg,
		cfg:            cfg,
		clips:          clips,
		outbox:         outbox,
		log:            log,
		embeddingCache: make(map[string][]float32),
	}
}

func (s *Service) getEmbeddingForVector(ctx context.Context, query string, mode string) ([]float32, error) {
	cacheKey := mode + ":" + query

	s.cacheMu.RLock()
	if cached, ok := s.embeddingCache[cacheKey]; ok {
		s.cacheMu.RUnlock()
		return cached, nil
	}
	s.cacheMu.RUnlock()

	if s.embedder == nil {
		return nil, fmt.Errorf("embedding client not configured")
	}

	var emb64 []float64
	var err error

	switch mode {
	case "visual":
		emb64, err = s.embedder.EmbedVisual(ctx, query)
	case "audio":
		emb64, err = s.embedder.EmbedAudio(ctx, query)
	default:
		emb64, err = s.embedder.EmbedText(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("embedding failed for mode %s: %w", mode, err)
	}

	emb32 := make([]float32, len(emb64))
	for i, v := range emb64 {
		emb32[i] = float32(v)
	}

	s.cacheMu.Lock()
	s.embeddingCache[cacheKey] = emb32
	s.cacheMu.Unlock()

	return emb32, nil
}

func (s *Service) getEmbedding(ctx context.Context, query string) ([]float32, error) {
	return s.getEmbeddingForVector(ctx, query, "text")
}

func (s *Service) ClearEmbeddingCache() {
	s.cacheMu.Lock()
	s.embeddingCache = make(map[string][]float32)
	s.cacheMu.Unlock()
}

func (s *Service) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return s.getEmbedding(ctx, text)
}

func (s *Service) EmbedTextForVector(ctx context.Context, text string, mode string) ([]float32, error) {
	return s.getEmbeddingForVector(ctx, text, mode)
}

func (s *Service) VectorStore() *vectorstore.Service {
	return s.vectorSvc
}
