package usecase

import (
	"context"
	"errors"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fakeTopicSourceCache is a test double for scriptports.TopicSourceCache.
type fakeTopicSourceCache struct {
	data map[string]scriptpkg.ResearchCacheRecord
}

func newFakeTopicSourceCache() *fakeTopicSourceCache {
	return &fakeTopicSourceCache{data: make(map[string]scriptpkg.ResearchCacheRecord)}
}

func (f *fakeTopicSourceCache) GetResearchCache(_ context.Context, key string) (string, error) {
	rec, ok := f.data[key]
	if !ok {
		return "", nil
	}
	if !rec.ExpiresAt.IsZero() && rec.ExpiresAt.Before(time.Now().UTC()) {
		return "", nil
	}
	return rec.SourceText, nil
}

func (f *fakeTopicSourceCache) SaveResearchCache(_ context.Context, rec scriptpkg.ResearchCacheRecord) error {
	f.data[rec.Key] = rec
	return nil
}

// failingTopicSourceCache always returns an error.
type failingTopicSourceCache struct{}

func (f *failingTopicSourceCache) GetResearchCache(context.Context, string) (string, error) {
	return "", errors.New("get failure")
}

func (f *failingTopicSourceCache) SaveResearchCache(context.Context, scriptpkg.ResearchCacheRecord) error {
	return errors.New("save failure")
}
