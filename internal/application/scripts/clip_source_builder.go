// Package scripts — clip_source_builder.go replaces the ClipSourceBuilder
// stub with a real implementation that fetches clips from the repository
// and builds context from their metadata + transcripts.
//
// PG-033 (June 2026): NewClipSourceBuilder drops the dead `ollamaClient`
// argument (field was unused, removed in PG-033); clipsRepo is typed
// *assets.ClipsRepository (was `interface{}`); BuildClipContext returns
// map[string]any (was `interface{}`); ComputeFingerprint / NewFingerprintContext
// signatures are concrete (no `interface{}` slots).
package scripts

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	"go.uber.org/zap"
)

// NewClipSourceBuilder constructs a real ClipSourceBuilder backed by
// the clips repository. PG-033 dropped the unused `ollamaClient` arg.
//
// clipsRepo may be nil (BuildClipContext returns an error when nil
// and clip IDs are provided).
func NewClipSourceBuilder(
	clipsRepo *assets.ClipsRepository,
	log *zap.Logger,
) *ClipSourceBuilder {
	return &ClipSourceBuilder{
		clipsRepo: clipsRepo,
		log:       log,
	}
}

// SetVectorStore attaches a vector-store adapter for future semantic
// enrichment of clip context. The arg stays `interface{}` because
// multiple backend adapters (qdrant.SearchAdapter, ...) are accepted;
// see MediaCurator.vectorStore for the live match-port definition.
func (c *ClipSourceBuilder) SetVectorStore(v interface{}) { c.vectorStore = v }

// SetReranker attaches a reranker client for future search reranking.
// Same multi-backend rationale as SetVectorStore.
func (c *ClipSourceBuilder) SetReranker(r interface{}) { c.reranker = r }

// BuildClipContext fetches clips by ID from the repository and builds
// a context pack, narrative plan, and source text for the script engine.
//
// Returns:
//   - pack: a map[string]any with clip data (IDs, names, transcripts, metadata)
//   - plan: a NarrativePlan derived from the clip titles
//   - sourceText: a concatenated string of clip names + search text + transcript excerpts
//
// PG-033: return shape changed from`interface{}` to `map[string]any`
// (the canonical shape produced by this builder; callers typed accordingly).
func (c *ClipSourceBuilder) BuildClipContext(
	ctx context.Context,
	clipIDs []string,
	opts *ClipGenerationOptions,
) (map[string]any, *NarrativePlan, string, error) {
	if c == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: not constructed")
	}
	if c.clipsRepo == nil {
		return nil, nil, "", fmt.Errorf("clip source builder: clips repository not configured")
	}

	// Deduplicate and trim.
	seen := make(map[string]struct{}, len(clipIDs))
	uniqueIDs := make([]string, 0, len(clipIDs))
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	if len(uniqueIDs) == 0 {
		return nil, nil, "", fmt.Errorf("clip source builder: no valid clip IDs provided")
	}

	clipsRepo := c.clipsRepo
	clips := make([]*asset.Asset, 0, len(uniqueIDs))
	clipNames := make([]string, 0, len(uniqueIDs))
	var sourceTextBuilder strings.Builder

	for _, id := range uniqueIDs {
		clip, err := clipsRepo.GetClip(ctx, id)
		if err != nil {
			if c.log != nil {
				c.log.Warn("clip source builder: failed to fetch clip",
					zap.String("clip_id", id),
					zap.Error(err))
			}
			continue
		}
		if clip == nil {
			continue
		}
		clips = append(clips, clip)
		name := strings.TrimSpace(clip.Name)
		if name == "" {
			name = strings.TrimSpace(clip.Filename)
		}
		if name == "" {
			name = id
		}
		clipNames = append(clipNames, name)

		// Build source text from clip metadata.
		sourceTextBuilder.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, name))
		if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
		}
		transcript := clip.GetMetadataString("transcript")
		if transcript == "" {
			transcript = clip.GetMetadataString("clean_transcript")
		}
		if transcript != "" {
			excerpt := transcript
			if len(excerpt) > 500 {
				excerpt = excerpt[:500] + "..."
			}
			sourceTextBuilder.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
		}
		if len(clip.Tags) > 0 {
			sourceTextBuilder.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
		}
		sourceTextBuilder.WriteString("\n")
	}

	if len(clips) == 0 {
		return nil, nil, "", fmt.Errorf("clip source builder: no clips found for the provided IDs")
	}

	// Build narrative plan from clip titles.
	title := "script"
	language := ""
	tone := ""
	targetWords := 0
	model := ""
	if opts != nil {
		if v := strings.TrimSpace(opts.Title); v != "" {
			title = v
		}
		language = strings.TrimSpace(opts.Language)
		tone = strings.TrimSpace(opts.Tone)
		targetWords = opts.TargetWords
		model = strings.TrimSpace(opts.Model)
	}

	sections := make([]NarrativeSection, 0, len(clipNames))
	for i, name := range clipNames {
		sections = append(sections, NarrativeSection{
			Role:       fmt.Sprintf("section_%d", i+1),
			Purpose:    fmt.Sprintf("Cover content from clip: %s", name),
			WordBudget: targetWords / maxInt(len(clipNames), 1),
		})
	}

	plan := &NarrativePlan{
		Title:      title,
		Sections:   sections,
		TotalWords: targetWords,
		Style:      tone,
	}

	// Build pack with clip data.
	pack := map[string]any{
		"clip_ids":   uniqueIDs,
		"clip_names": clipNames,
		"title":      title,
		"language":   language,
		"tone":       tone,
		"model":      model,
		"clip_count": len(clips),
	}

	if c.log != nil {
		c.log.Info("clip source builder: context built",
			zap.Int("clip_ids", len(uniqueIDs)),
			zap.Int("clips_found", len(clips)),
			zap.Int("source_text_chars", sourceTextBuilder.Len()))
	}

	return pack, plan, sourceTextBuilder.String(), nil
}

// ComputeFingerprint builds a deterministic fingerprint string from clip
// IDs and generation options. Used as a cache key for the memory gate.
// PG-033: pack is map[string]any and fpCtx is map[string]string (the
// canonical shapes produced by BuildClipContext / NewFingerprintContext).
func (c *ClipSourceBuilder) ComputeFingerprint(
	clipIDs []string,
	pack map[string]any,
	opts *ClipGenerationOptions,
	fpCtx map[string]string,
) string {
	parts := []string{"clips"}
	for _, id := range clipIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			parts = append(parts, id)
		}
	}
	if opts != nil {
		if v := strings.TrimSpace(opts.Title); v != "" {
			parts = append(parts, "title="+v)
		}
		if v := strings.TrimSpace(opts.Language); v != "" {
			parts = append(parts, "lang="+v)
		}
		if v := strings.TrimSpace(opts.Tone); v != "" {
			parts = append(parts, "tone="+v)
		}
		if v := strings.TrimSpace(opts.Model); v != "" {
			parts = append(parts, "model="+v)
		}
		if v := strings.TrimSpace(opts.TranscriptPolicy); v != "" {
			parts = append(parts, "transcript="+v)
		}
		if v := strings.TrimSpace(opts.OrderingStrategy); v != "" {
			parts = append(parts, "order="+v)
		}
	}
	// fpCtx keys (model / prompt_model) become suffix tokens so the
	// cache key carries both clip identity and model identity.
	if fpCtx != nil {
		if v := strings.TrimSpace(fpCtx["model"]); v != "" {
			parts = append(parts, "model_ctx="+v)
		}
		if v := strings.TrimSpace(fpCtx["prompt_model"]); v != "" {
			parts = append(parts, "prompt_model_ctx="+v)
		}
	}
	return strings.Join(parts, "|")
}

// NewFingerprintContext creates a fingerprint context for cache key derivation.
// PG-033: return shape changed from `interface{}` to `map[string]string`
// (always the canonical two-key map; matches ComputeFingerprint's typed param).
func NewFingerprintContext(model, promptModel string) map[string]string {
	return map[string]string{
		"model":        strings.TrimSpace(model),
		"prompt_model": strings.TrimSpace(promptModel),
	}
}
