package texttracks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// Materialize runs the (a-f) pipeline for a single (asset, kind) pair.
//
// targetLanguagesOverride is the optional caller override for the target
// language set. Empty / nil means "use the configured registry languages".
// A non-empty value REPLACES the configured set.
func (m *Materializer) Materialize(
	ctx context.Context,
	assetID string,
	sourceLanguageCode string,
	sourceTextHash string,
	kind detail.TextTrackKind,
	targetLanguagesOverride []string,
) (*MaterializationReport, error) {
	start := time.Now()
	if err := validateMaterializeRequest(assetID, sourceLanguageCode, sourceTextHash); err != nil {
		return nil, err
	}

	perCallCfg := materializeConfig(m.resolverCfg, sourceLanguageCode, targetLanguagesOverride)
	report := newMaterializationReport(assetID, sourceLanguageCode, sourceTextHash, kind)
	resolver := NewResolver(perCallCfg, m.repo, assetID, kind)

	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateSourceHash(source, sourceTextHash); err != nil {
		return nil, err
	}

	candidates := resolver.CandidateLanguages()
	m.logMaterializeStart(assetID, kind, sourceLanguageCode, len(candidates))
	if err := m.runMaterializationFanout(ctx, resolver, source, candidates, report); err != nil {
		report.Duration = time.Since(start)
		return report, err
	}

	normalizeReportOrder(candidates, report)
	tracksChanged := len(report.CreatedLanguages)+len(report.RetranslatedLanguages) > 0
	if err := m.refreshIndexInput(ctx, assetID, kind, tracksChanged, report); err != nil {
		report.Duration = time.Since(start)
		return report, err
	}

	report.Duration = time.Since(start)
	m.logMaterializeDone(report)
	return report, nil
}

func validateMaterializeRequest(assetID, sourceLanguageCode, sourceTextHash string) error {
	if assetID == "" {
		return &ErrInvalidMaterializeRequest{Field: "assetID", Reason: "asset_id is required"}
	}
	if sourceLanguageCode == "" {
		return &ErrInvalidMaterializeRequest{Field: "sourceLanguageCode", Reason: "source_language is required"}
	}
	if sourceTextHash == "" {
		return &ErrInvalidMaterializeRequest{Field: "sourceTextHash", Reason: "source_text_hash is required"}
	}
	return nil
}

func materializeConfig(base ResolverConfig, sourceLanguageCode string, targetLanguagesOverride []string) ResolverConfig {
	cfg := base
	cfg.SourceLanguage = sourceLanguageCode
	if len(targetLanguagesOverride) > 0 {
		cfg.OverrideTargetLanguages = append([]string{}, targetLanguagesOverride...)
	}
	return cfg
}

func newMaterializationReport(assetID, sourceLanguageCode, sourceTextHash string, kind detail.TextTrackKind) *MaterializationReport {
	return &MaterializationReport{
		AssetID:               assetID,
		Kind:                  kind,
		SourceLanguage:        sourceLanguageCode,
		SourceTextHash:        sourceTextHash,
		CreatedLanguages:      []string{},
		SkippedLanguages:      []string{},
		RetranslatedLanguages: []string{},
		FailedLanguages:       map[string]string{},
	}
}

func validateSourceHash(source *detail.TextTrack, sourceTextHash string) error {
	if source.TextHash != "" && source.TextHash != sourceTextHash {
		return &ErrInvalidMaterializeRequest{
			Field:  "sourceTextHash",
			Reason: fmt.Sprintf("caller-provided hash %q does not match source track hash %q", sourceTextHash, source.TextHash),
		}
	}
	return nil
}

func (m *Materializer) logMaterializeStart(assetID string, kind detail.TextTrackKind, sourceLanguageCode string, candidates int) {
	m.log.Info("texttracks.materialize.start",
		zap.String("asset_id", assetID),
		zap.String("kind", string(kind)),
		zap.String("source_language", sourceLanguageCode),
		zap.Int("candidate_count", candidates),
	)
}

func (m *Materializer) logMaterializeDone(report *MaterializationReport) {
	m.log.Info("texttracks.materialize.done",
		zap.String("asset_id", report.AssetID),
		zap.String("kind", string(report.Kind)),
		zap.Int("created", len(report.CreatedLanguages)),
		zap.Int("skipped", len(report.SkippedLanguages)),
		zap.Int("retranslated", len(report.RetranslatedLanguages)),
		zap.Int("failed", len(report.FailedLanguages)),
		zap.Duration("duration", report.Duration),
	)
}

func (m *Materializer) runMaterializationFanout(
	ctx context.Context,
	resolver *Resolver,
	source *detail.TextTrack,
	candidates []string,
	report *MaterializationReport,
) error {
	if m.concurrency > 1 && len(candidates) > 1 {
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(m.concurrency)
		for _, targetLang := range candidates {
			targetLang := targetLang
			g.Go(func() error {
				if err := m.materializeOne(gctx, resolver, source, targetLang, report); err != nil {
					m.recordLanguageFailure(report, targetLang, err)
				}
				return nil
			})
		}
		if gerr := g.Wait(); gerr != nil {
			return fmt.Errorf("texttracks.materialize: unexpected group error: %w", gerr)
		}
		return nil
	}

	for _, targetLang := range candidates {
		if err := m.materializeOne(ctx, resolver, source, targetLang, report); err != nil {
			m.recordLanguageFailure(report, targetLang, err)
		}
	}
	return nil
}

func (m *Materializer) recordLanguageFailure(report *MaterializationReport, targetLang string, err error) {
	m.mu.Lock()
	report.FailedLanguages[targetLang] = err.Error()
	m.mu.Unlock()
	m.log.Warn("texttracks.materialize.language_failed",
		zap.String("asset_id", report.AssetID),
		zap.String("target_language", targetLang),
		zap.Error(err),
	)
}

// materializeOne handles a single target language. The lookup-before-translate
// gate runs first; a READY current row with a matching translation key is
// reused without calling the translator.
func (m *Materializer) materializeOne(
	ctx context.Context,
	resolver *Resolver,
	source *detail.TextTrack,
	targetLang string,
	report *MaterializationReport,
) error {
	existing, err := m.findCurrentTranslation(ctx, report, targetLang)
	if err != nil {
		return err
	}
	if existing != nil {
		m.recordSkipped(report, targetLang)
		return nil
	}

	classification := m.classifyLegacyTarget(ctx, resolver, targetLang)
	translated, err := m.translateSource(ctx, source, report, targetLang)
	if err != nil {
		return err
	}
	if err := m.persistTranslatedTrack(ctx, source, report, targetLang, translated); err != nil {
		return err
	}
	m.recordTranslationOutcome(report, targetLang, classification)
	return nil
}

func (m *Materializer) findCurrentTranslation(ctx context.Context, report *MaterializationReport, targetLang string) (*detail.TextTrack, error) {
	existing, err := m.repo.FindCurrentForTranslation(
		ctx,
		report.AssetID,
		report.Kind,
		targetLang,
		report.SourceTextHash,
		m.resolverCfg.TranslationModel,
		m.resolverCfg.ModelVersion,
		m.resolverCfg.PromptVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("find current for translation: %w", err)
	}
	return existing, nil
}

func (m *Materializer) recordSkipped(report *MaterializationReport, targetLang string) {
	m.mu.Lock()
	report.SkippedLanguages = append(report.SkippedLanguages, targetLang)
	m.mu.Unlock()
	m.log.Info("texttracks.materialize.translation_key_hit",
		zap.String("asset_id", report.AssetID),
		zap.String("kind", string(report.Kind)),
		zap.String("target_language", targetLang),
	)
}

func (m *Materializer) classifyLegacyTarget(ctx context.Context, resolver *Resolver, targetLang string) string {
	if legacyExisting, err := resolver.FindExistingTarget(ctx, targetLang); err == nil && legacyExisting != nil {
		return "retranslated"
	}
	return "created"
}

func (m *Materializer) translateSource(ctx context.Context, source *detail.TextTrack, report *MaterializationReport, targetLang string) (translation.TranslationResult, error) {
	cmd := translation.TranslationCommand{
		SourceLang: report.SourceLanguage,
		TargetLang: targetLang,
		Text:       source.TextContent,
		ModelHints: map[string]string{
			"deterministic":          "true",
			"preserve_formatting":    "true",
			"preserve_entities":      "true",
			"preserve_scene_markers": "true",
		},
	}
	if m.resolverCfg.OllamaModel != "" {
		cmd.ModelPolicy = &translation.ModelPolicy{
			Provider: "ollama",
			Model:    m.resolverCfg.OllamaModel,
		}
	}

	translated, err := m.translator.Translate(ctx, cmd)
	if err != nil {
		return translation.TranslationResult{}, &ErrTranslationFailed{
			AssetID:       report.AssetID,
			TargetLang:    targetLang,
			TextKind:      report.Kind,
			Cause:         err,
			AttemptedText: source.TextContent,
		}
	}
	if strings.TrimSpace(translated.TranslatedText) == "" {
		return translation.TranslationResult{}, &ErrTranslationFailed{
			AssetID:    report.AssetID,
			TargetLang: targetLang,
			TextKind:   report.Kind,
			Cause: &ErrEmptyTranslation{
				Provider:   translated.UsedProvider,
				Model:      translated.UsedModel,
				TargetLang: targetLang,
			},
			AttemptedText: source.TextContent,
		}
	}
	return translated, nil
}

func (m *Materializer) persistTranslatedTrack(ctx context.Context, source *detail.TextTrack, report *MaterializationReport, targetLang string, translated translation.TranslationResult) error {
	translationModel, modelVersion := m.translationFingerprint(translated)
	translationKey := detail.TranslationKey(
		report.SourceTextHash,
		targetLang,
		translationModel,
		modelVersion,
		m.resolverCfg.PromptVersion,
	)

	confidence := translated.Confidence
	newTrack := detail.TextTrack{
		AssetID:            report.AssetID,
		LanguageCode:       targetLang,
		TextKind:           report.Kind,
		TextContent:        translated.TranslatedText,
		SourceType:         detail.TextSourceTranslation,
		SourceLanguageCode: report.SourceLanguage,
		IsOriginal:         false,
		Provider:           translated.UsedProvider,
		ModelName:          translated.UsedModel,
		ModelVersion:       modelVersion,
		PromptVersion:      m.resolverCfg.PromptVersion,
		TextHash:           ComputeSourceTextHash(translated.TranslatedText),
		SourceVersion:      source.SourceVersion,
		SourceTextHash:     report.SourceTextHash,
		TranslationKey:     translationKey,
		IsCurrent:          true,
		Confidence:         &confidence,
		Status:             detail.TextTrackReady,
	}
	if confidence == 0 {
		newTrack.Confidence = nil
	}
	if err := m.repo.InsertTranslationWithAuditPredecessor(ctx, newTrack); err != nil {
		return fmt.Errorf("insert translation with audit predecessor: %w", err)
	}
	return nil
}

func (m *Materializer) translationFingerprint(translated translation.TranslationResult) (string, string) {
	translationModel := m.resolverCfg.TranslationModel
	modelVersion := m.resolverCfg.ModelVersion
	if translationModel == translation.ArgosTranslationModel && translated.UsedProvider == translation.ProviderOllama {
		translationModel = translation.ProviderOllama
		modelVersion = translated.UsedModel
		if modelVersion == "" {
			modelVersion = "ollama-server-default"
		}
	}
	return translationModel, modelVersion
}

func (m *Materializer) recordTranslationOutcome(report *MaterializationReport, targetLang, classification string) {
	m.mu.Lock()
	switch classification {
	case "created":
		report.CreatedLanguages = append(report.CreatedLanguages, targetLang)
	case "retranslated":
		report.RetranslatedLanguages = append(report.RetranslatedLanguages, targetLang)
	}
	m.mu.Unlock()
}
