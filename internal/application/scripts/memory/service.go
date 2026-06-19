package memory

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	scriptrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// saveAfterGenerationTimeout caps how long the post-generation DB writes are allowed to run.
const saveAfterGenerationTimeout = 30 * time.Second

// Service is the main Memory Gate orchestrator.
type Service struct {
	repo *scriptrepo.MemoryRepository
	log  *zap.Logger
}

// NewService creates a new memory gate service.
func NewService(repo *scriptrepo.MemoryRepository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// CheckGate is called BEFORE generation. It implements the full memory gate.
func (s *Service) CheckGate(ctx context.Context, req MemoryGateRequest) (*MemoryGateResult, error) {
	result := &MemoryGateResult{}

	normalized := NormalizeInput(req.ChannelID, req.Title, req.Prompt)
	inputHash := HashInput(req.ChannelID, req.Mode, normalized)

	if req.UseMemory && !req.ForceRefresh {
		exact, err := s.repo.FindExactOutput(ctx, req.ChannelID, req.Mode, inputHash)
		if err == nil && exact != nil {
			s.log.Info("memory gate: EXACT CACHE HIT",
				zap.String("channel_id", req.ChannelID),
				zap.String("generation_id", exact.ID),
				zap.String("title", req.Title),
			)
			result.CacheHit = true
			result.SourceGenerationID = exact.ID
			result.ExactOutput = exact
			return result, nil
		}
	}

	if req.UseMemory {
		hits := RetrieveRelevantContext(ctx, s.repo, req)

		if len(hits) == 0 {
			crossHits, crossErr := s.repo.FindMemoryCrossChannel(ctx, req.ChannelID, "", 5)
			if crossErr == nil && len(crossHits) > 0 {
				s.log.Info("memory gate: CROSS-CHANNEL HIT",
					zap.String("channel_id", req.ChannelID),
					zap.Int("cross_channel_memories", len(crossHits)),
				)
				for _, ch := range crossHits {
					hits = append(hits, MemoryHit{Source: "cross_channel", Entry: ch})
				}
			}
		}

		result.MemoryHits = hits

		if len(hits) > 0 {
			result.EnrichedPrompt = BuildEnrichedPrompt(req, hits)
		}
	}

	return result, nil
}

// SaveAfterGeneration is called AFTER a successful generation.
func (s *Service) SaveAfterGeneration(ctx context.Context, input SaveGenerationInput, outputText string) (string, error) {
	normalized := NormalizeInput(input.ChannelID, input.Title, input.Prompt)
	inputHash := HashInput(input.ChannelID, input.Mode, normalized)

	saveCtx, cancel := context.WithTimeout(context.Background(), saveAfterGenerationTimeout)
	defer cancel()
	if ctx.Err() != nil {
		s.log.Warn("memory gate: request context already cancelled, proceeding with independent save context",
			zap.String("channel_id", input.ChannelID),
			zap.String("title", input.Title),
			zap.Duration("save_timeout", saveAfterGenerationTimeout),
			zap.Error(ctx.Err()),
		)
	}

	genID, err := s.repo.SaveGeneration(saveCtx, input, normalized, inputHash)
	if err != nil {
		s.log.Error("failed to save generation",
			zap.String("channel_id", input.ChannelID),
			zap.String("title", input.Title),
			zap.Duration("save_timeout", saveAfterGenerationTimeout),
			zap.Error(err),
		)
		return "", err
	}

	topicKey := BuildTopicKey(input.Title, input.Prompt)
	chunks := ChunkScript(outputText, 500)
	if err := s.repo.SaveChunks(saveCtx, genID, input.ChannelID, input.Title, topicKey, chunks); err != nil {
		s.log.Warn("failed to save script chunks",
			zap.String("gen_id", genID),
			zap.Int("chunk_count", len(chunks)),
			zap.Error(err),
		)
	}

	memories := ExtractMemories(input, genID, topicKey, outputText)
	for _, mem := range memories {
		if _, err := s.repo.SaveMemory(saveCtx, mem); err != nil {
			s.log.Warn("failed to save memory entry",
				zap.String("gen_id", genID),
				zap.String("type", mem.MemoryType),
				zap.Error(err),
			)
		}
	}

	return genID, nil
}

// CountRecentExactOutputs returns how many exact cache entries match a title pattern.
func (s *Service) CountRecentExactOutputs(ctx context.Context, channelID, mode, title string) (int, error) {
	if s.repo == nil {
		return 0, fmt.Errorf("repository not initialized")
	}
	return s.repo.CountExactOutputsByTitle(ctx, channelID, mode, title)
}

// EvictExactOutputs deletes cache entries matching the specified titles.
func (s *Service) EvictExactOutputs(ctx context.Context, titles []string) (int64, error) {
	if s.repo == nil {
		return 0, fmt.Errorf("repository not initialized")
	}
	s.log.Info("evicting exact cache outputs", zap.Int("titles_count", len(titles)), zap.Strings("titles", titles))
	return s.repo.DeleteExactOutputsByTitles(ctx, titles)
}
