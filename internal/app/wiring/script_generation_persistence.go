package wiring

import (
	"context"
	"fmt"
	"sort"
	"strings"

	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// scriptGenerationPersistence keeps SQLite ownership in the existing
// PersistenceProcessor while adapting the durable capability's typed port.
type scriptGenerationPersistence struct {
	processor *documentadapters.PersistenceProcessor
}

func newScriptGenerationPersistence(repo documentadapters.ScriptRepository, log *zap.Logger) *scriptGenerationPersistence {
	if repo == nil {
		return nil
	}
	return &scriptGenerationPersistence{
		processor: documentadapters.NewPersistenceProcessor(repo, log),
	}
}

func (p *scriptGenerationPersistence) Persist(ctx context.Context, input scriptgen.ScriptPersistenceInput) (int64, error) {
	if p == nil || p.processor == nil {
		return 0, fmt.Errorf("script generation persistence adapter is not configured")
	}
	if input.Result == nil {
		return 0, fmt.Errorf("script generation persistence result is nil")
	}

	language := input.Request.SourceLanguage
	parts := make([]string, 0, len(input.Result.Scenes))
	scenes := make([]scriptpkg.SpecScene, 0, len(input.Result.Scenes))
	wordCount := 0
	for _, scene := range input.Result.Scenes {
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			langs := make([]string, 0, len(scene.Text))
			for lang := range scene.Text {
				langs = append(langs, string(lang))
			}
			sort.Strings(langs)
			if len(langs) > 0 {
				text = strings.TrimSpace(scene.Text[scriptgen.Language(langs[0])])
			}
		}
		if text == "" {
			continue
		}
		parts = append(parts, text)
		wordCount += len(strings.Fields(text))
		scenes = append(scenes, scriptpkg.SpecScene{
			ID:    scene.ID,
			Index: len(scenes),
			Text:  text,
			Kind:  scriptpkg.SceneNarration,
		})
	}
	text := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if text == "" {
		return 0, fmt.Errorf("script generation persistence has no scene text")
	}
	if input.Result.WordCount > 0 {
		wordCount = input.Result.WordCount
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:            input.Request.IdempotencyKey,
		Title:         input.Request.Title,
		Topic:         input.Request.Source.Topic,
		Language:      string(language),
		Mode:          string(input.Request.Source.Type),
		TargetWords:   input.Request.ScriptParams.TargetWords,
		PromptVersion: input.Request.ScriptParams.PromptVersion,
		SaveToDB:      true,
	}
	post, err := p.processor.Process(ctx, plan, documentadapters.ProcessInput{
		Text:              text,
		WordCount:         wordCount,
		SpecScene:         scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes},
		CacheStatus:       "generated",
		EffectiveLanguage: string(language),
	})
	if err != nil {
		return 0, err
	}
	if post == nil || post.ScriptID <= 0 {
		return 0, fmt.Errorf("canonical persistence returned invalid script_id")
	}
	return post.ScriptID, nil
}

var _ scriptgen.ScriptPersistence = (*scriptGenerationPersistence)(nil)
