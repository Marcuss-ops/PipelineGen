// Package classifier provides shared LLM-based video title classification.
package classifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/prompts"

	"go.uber.org/zap"
)

// LLMClient is the minimal interface for Ollama-style simple generation.
type LLMClient interface {
	SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, _ map[string]any) (string, error)
}

// CategoryCache provides get/set for classification results (typically backed by SQLite).
type CategoryCache interface {
	Get(ctx context.Context, title string) (string, bool)
	Set(ctx context.Context, title, category string) error
}

// Options configures the classification behavior.
type Options struct {
	// DataDir is the root data directory (e.g. "data/"). Used to scan existing categories.
	DataDir string
	// Model is the Ollama model name (default: "gemma4:e2b").
	Model string
	// FallbackCategory is returned when classification fails (default: "general").
	FallbackCategory string
	// ExcludeCategories are category names to exclude from the existing list.
	ExcludeCategories []string
	// EnsureCategories are category names that must always be present in the list.
	EnsureCategories []string
	// DefaultCategories is used when no existing categories are found on disk.
	DefaultCategories []string
	// Cache is an optional cache for classification results.
	Cache CategoryCache
	// Semaphore is an optional channel-based concurrency limiter (e.g. make(chan struct{}, 2)).
	Semaphore chan struct{}
}

// Classify classifies a video title into a category using an LLM.
// It scans existing category directories for context, builds a prompt,
// and sanitizes the response to a lowercase alphanumeric+hypens token.
func Classify(ctx context.Context, log *zap.Logger, client LLMClient, title string, opts Options) string {
	if opts.FallbackCategory == "" {
		opts.FallbackCategory = "general"
	}
	if opts.Model == "" {
		opts.Model = "gemma4:e2b"
	}
	if client == nil {
		return opts.FallbackCategory
	}

	existingCategories := scanCategories(opts.DataDir, opts.ExcludeCategories, opts.EnsureCategories, opts.DefaultCategories)

	var prompt string
	if cfg := prompts.Get(); cfg != nil {
		rendered, err := cfg.RenderClassification(title, strings.Join(existingCategories, ", "))
		if err == nil {
			prompt = rendered
		}
	}
	if prompt == "" {
		prompt = fmt.Sprintf("You are a classification assistant. Classify the video title: '%s' into ONE of these existing categories: %s. "+
			"RULES:\n"+
			"- You MUST pick from the list above. Do NOT create new categories.\n"+
			"- Pick the closest match even if imperfect.\n"+
			"- Do NOT add suffixes like 'tech' to category names (e.g. use 'music' not 'musictech').\n"+
			"- Do NOT use format-based words: interviews, videos, general, other, clips, youtube.\n"+
			"Your response must contain ONLY the category name as a single word in lowercase, with no explanation and no punctuation.",
			title, strings.Join(existingCategories, ", "))
	}

	response, err := client.SimpleGenerate(ctx, opts.Model, prompt, 30*time.Second, nil)
	if err != nil {
		log.Warn("ollama call failed for classification, using fallback", zap.Error(err))
		return opts.FallbackCategory
	}

	category := sanitizeCategory(response)
	if category == "" {
		return opts.FallbackCategory
	}
	return category
}

// CachedClassify wraps Classify with cache and optional semaphore support.
// If cache is provided, it checks for a cached result before calling the LLM.
// If sem is provided, it acquires a slot before calling the LLM.
func CachedClassify(ctx context.Context, log *zap.Logger, client LLMClient, title string, opts Options) string {
	if client == nil {
		if opts.FallbackCategory == "" {
			opts.FallbackCategory = "general"
		}
		return opts.FallbackCategory
	}

	if opts.Cache != nil {
		if cat, found := opts.Cache.Get(ctx, title); found {
			log.Info("resolved classification from cache", zap.String("title", title), zap.String("category", cat))
			return cat
		}
	}

	// Acquire semaphore if provided
	if opts.Semaphore != nil {
		select {
		case opts.Semaphore <- struct{}{}:
			defer func() { <-opts.Semaphore }()
		case <-ctx.Done():
			return opts.FallbackCategory
		}
	}

	category := Classify(ctx, log, client, title, opts)

	if opts.Cache != nil && category != opts.FallbackCategory {
		if err := opts.Cache.Set(ctx, title, category); err != nil {
			log.Warn("failed to cache classification", zap.Error(err))
		}
	}

	return category
}

// scanCategories reads subdirectories of dataDir/media/clips to discover existing categories.
func scanCategories(dataDir string, exclude, ensure, defaults []string) []string {
	var categories []string
	if dataDir != "" {
		youtubeDir := filepath.Join(dataDir, "media", "clips")
		if dirs, err := os.ReadDir(youtubeDir); err == nil {
			for _, d := range dirs {
				if d.IsDir() {
					categories = append(categories, strings.ToLower(d.Name()))
				}
			}
		}
	}

	// Filter excluded categories
	if len(exclude) > 0 {
		excludeSet := make(map[string]bool, len(exclude))
		for _, e := range exclude {
			excludeSet[strings.ToLower(e)] = true
		}
		var filtered []string
		for _, c := range categories {
			if !excludeSet[c] {
				filtered = append(filtered, c)
			}
		}
		categories = filtered
	}

	// Use defaults if no categories found
	if len(categories) == 0 {
		if len(defaults) > 0 {
			categories = defaults
		} else {
			categories = []string{"general", "music", "rap", "interviews"}
		}
	}

	// Ensure required categories are present
	if len(ensure) > 0 {
		existSet := make(map[string]bool, len(categories))
		for _, c := range categories {
			existSet[c] = true
		}
		for _, e := range ensure {
			if !existSet[strings.ToLower(e)] {
				categories = append(categories, strings.ToLower(e))
			}
		}
	}

	return categories
}

// sanitizeCategory strips everything except lowercase alphanumeric, hyphens, and underscores.
func sanitizeCategory(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return -1
	}, s)
}
