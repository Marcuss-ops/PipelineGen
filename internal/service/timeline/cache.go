package timeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/sliceutil"
)

const cacheVersion = "v18"

type Cache struct {
	repo *clips.Repository
	gen  *ollama.Generator
}

func NewCache(repo *clips.Repository, gen *ollama.Generator) *Cache {
	return &Cache{
		repo: repo,
		gen:  gen,
	}
}

func (c *Cache) BuildKey(topic, template, sourceText, narrative string, duration int) string {
	payload, _ := json.Marshal([]string{
		cacheVersion,
		strings.ToLower(strings.TrimSpace(topic)),
		strings.ToLower(strings.TrimSpace(template)),
		fmt.Sprintf("%d", duration),
		strings.ToLower(strings.TrimSpace(sourceText)),
		strings.ToLower(strings.TrimSpace(narrative)),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (c *Cache) HashSegment(topic, template string, duration int, narrative string, keywords, entities []string) string {
	payload, _ := json.Marshal([]any{
		cacheVersion,
		strings.ToLower(strings.TrimSpace(topic)),
		duration,
		strings.ToLower(strings.TrimSpace(template)),
		strings.TrimSpace(narrative),
		sliceutil.UniqueStrings(keywords),
		sliceutil.UniqueStrings(entities),
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (c *Cache) LoadPlan(ctx context.Context, cacheKey string) ([]clips.SegmentEmbeddingRecord, error) {
	if c.repo == nil || strings.TrimSpace(cacheKey) == "" {
		return nil, nil
	}
	return c.repo.GetSegmentEmbeddingsByScriptKey(ctx, cacheKey)
}

func (c *Cache) StoreSegment(ctx context.Context, cacheKey string, rec *clips.SegmentEmbeddingRecord) error {
	if c.repo == nil || strings.TrimSpace(cacheKey) == "" {
		return nil
	}
	return c.repo.UpsertSegmentEmbedding(ctx, rec)
}

func (c *Cache) ClearKey(ctx context.Context, cacheKey string) error {
	if c.repo == nil || strings.TrimSpace(cacheKey) == "" {
		return nil
	}
	return c.repo.DeleteSegmentEmbeddingsByScriptKey(ctx, cacheKey)
}

func (c *Cache) GenerateEmbedding(ctx context.Context, text string) (string, error) {
	if c.gen == nil || c.gen.GetClient() == nil || text == "" {
		return "[]", nil
	}
	embedding, err := c.gen.GetClient().Embed(ctx, text)
	if err != nil {
		return "[]", err
	}
	data, err := json.Marshal(embedding)
	if err != nil {
		return "[]", err
	}
	return string(data), nil
}
