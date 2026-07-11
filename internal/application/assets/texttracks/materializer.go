// Package texttracks — materializer.go: TextTrackMaterializer is the
// canonical application-layer service that materializes a media
// asset's text tracks into all configured target languages.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// Pipeline (canonical):
//	a. Read source text track READY in asset_text_tracks.
//	b. For each candidate target language, find existing target
//	   track. If READY + matching MaterializationKey → SKIP.
//	c. For missing or stale targets, translate via canonical
//	   TranslationPort (full-text).
//	d. Save the new track as READY with provider/model/version
//	   provenance.
//	e. Emit asset.index.requested outbox event for Qdrant reindex.
//	f. On translation error: record in FailedLanguages; loop
//	   continues with the next target language.
package texttracks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"go.uber.org/zap"
)

// OutboxEnqueuer is the narrow port the materializer uses to
// emit the asset.index.requested event.
type OutboxEnqueuer interface {
	Enqueue(
		ctx context.Context,
		tx *sql.Tx,
		eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
	) (*outboxevents.EnqueueResult, error)
}

// MaterializationReport is the canonical return value.
type MaterializationReport struct {
	AssetID               string                `json:"asset_id"`
	Kind                  asset.TextTrackKind   `json:"kind"`
	SourceLanguage        string                `json:"source_language"`
	SourceTextHash        string                `json:"source_text_hash"`
	CreatedLanguages      []string              `json:"created_languages"`
	SkippedLanguages      []string              `json:"skipped_languages"`
	RetranslatedLanguages []string              `json:"retranslated_languages"`
	FailedLanguages       map[string]string     `json:"failed_languages"`
	Duration              time.Duration         `json:"duration"`
}

func (r *MaterializationReport) HasFailures() bool {
	return len(r.FailedLanguages) > 0
}

func (r *MaterializationReport) TotalProcessed() int {
	return len(r.CreatedLanguages) + len(r.SkippedLanguages) +
		len(r.RetranslatedLanguages) + len(r.FailedLanguages)
}

// Materializer is the canonical application-layer service.
type Materializer struct {
	repo       asset.TextTrackRepository
	translator translation.TranslationPort
	outbox     OutboxEnqueuer
	resolverCfg ResolverConfig
	log        *zap.Logger
}

func NewMaterializer(
	repo asset.TextTrackRepository,
	translator translation.TranslationPort,
	outbox OutboxEnqueuer,
	resolverCfg ResolverConfig,
	log *zap.Logger,
) (*Materializer, error) {
	if repo == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: repo is nil")
	}
	if translator == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: translator is nil")
	}
	if outbox == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: outbox is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: log is nil")
	}
	if err := resolverCfg.Validate(); err != nil {
		return nil, fmt.Errorf("texttracks.NewMaterializer: %w", err)
	}
	return &Materializer{
		repo:        repo,
		translator:  translator,
		outbox:      outbox,
		resolverCfg: resolverCfg,
		log:         log,
	}, nil
}

// Materialize runs the (a-f) pipeline for a single (asset, kind) pair.
//
// targetLanguagesOverride is the optional caller override for
// the target language set. Empty / nil means "use the configured
// MultilingualConfig.MaterializeLanguages" (the canonical
// default). A non-empty value REPLACES the configured set
// (operators can backfill into a single language without
// editing config). The source_language is preserved from the
// per-call sourceLanguageCode arg.
func (m *Materializer) Materialize(
	ctx context.Context,
	assetID string,
	sourceLanguageCode string,
	sourceTextHash string,
	kind asset.TextTrackKind,
	targetLanguagesOverride []string,
) (*MaterializationReport, error) {
	start := time.Now()

	if assetID == "" {
		return nil, &ErrInvalidMaterializeRequest{
			Field: "assetID", Reason: "asset_id is required",
		}
	}
	if sourceLanguageCode == "" {
		return nil, &ErrInvalidMaterializeRequest{
			Field: "sourceLanguageCode", Reason: "source_language is required",
		}
	}
	if sourceTextHash == "" {
		return nil, &ErrInvalidMaterializeRequest{
			Field: "sourceTextHash", Reason: "source_text_hash is required",
		}
	}

	perCallCfg := m.resolverCfg
	perCallCfg.SourceLanguage = sourceLanguageCode
	if len(targetLanguagesOverride) > 0 {
		perCallCfg.MaterializeLanguages = append([]string{}, targetLanguagesOverride...)
	}

	report := &MaterializationReport{
		AssetID:               assetID,
		Kind:                  kind,
		SourceLanguage:        sourceLanguageCode,
		SourceTextHash:        sourceTextHash,
		CreatedLanguages:      []string{},
		SkippedLanguages:      []string{},
		RetranslatedLanguages: []string{},
		FailedLanguages:       map[string]string{},
	}

	resolver := NewResolver(perCallCfg, m.repo, assetID, kind)

	// (a) Read the source READY track.
	source, err := resolver.FindSourceTrack(ctx)
	if err != nil {
		return nil, err
	}

	// Caller-provided hash MUST match the source track's hash.
	if source.TextHash != "" && source.TextHash != sourceTextHash {
		return nil, &ErrInvalidMaterializeRequest{
			Field:  "sourceTextHash",
			Reason: fmt.Sprintf("caller-provided hash %q does not match source track hash %q", sourceTextHash, source.TextHash),
		}
	}

	// (b/c/d/e) Fan-out across candidate target languages.
	candidates := resolver.CandidateLanguages()
	m.log.Info("texttracks.materialize.start",
		zap.String("asset_id", assetID),
		zap.String("kind", string(kind)),
		zap.String("source_language", sourceLanguageCode),
		zap.Int("candidate_count", len(candidates)),
	)

	for _, targetLang := range candidates {
		if err := m.materializeOne(ctx, resolver, source, targetLang, report); err != nil {
			report.FailedLanguages[targetLang] = err.Error()
			m.log.Warn("texttracks.materialize.language_failed",
				zap.String("asset_id", assetID),
				zap.String("target_language", targetLang),
				zap.Error(err),
			)
		}
	}

	// (e) Emit asset.index.requested outbox event.
	if len(report.CreatedLanguages)+len(report.RetranslatedLanguages) > 0 {
		if err := m.emitAssetIndexRequested(ctx, assetID, kind); err != nil {
			report.Duration = time.Since(start)
			return report, fmt.Errorf("texttracks.materialize: outbox enqueue: %w", err)
		}
	}

	report.Duration = time.Since(start)
	m.log.Info("texttracks.materialize.done",
		zap.String("asset_id", assetID),
		zap.String("kind", string(kind)),
		zap.Int("created", len(report.CreatedLanguages)),
		zap.Int("skipped", len(report.SkippedLanguages)),
		zap.Int("retranslated", len(report.RetranslatedLanguages)),
		zap.Int("failed", len(report.FailedLanguages)),
		zap.Duration("duration", report.Duration),
	)
	return report, nil
}

// materializeOne handles a single target language.
func (m *Materializer) materializeOne(
	ctx context.Context,
	resolver *Resolver,
	source *asset.TextTrack,
	targetLang string,
	report *MaterializationReport,
) error {
	existing, err := resolver.FindExistingTarget(ctx, targetLang)
	if err != nil {
		return fmt.Errorf("find existing: %w", err)
	}

	key := MaterializationKey{
		SourceVersion:  source.SourceVersion,
		ModelVersion:   m.resolverCfg.ModelVersion,
		PromptVersion:  m.resolverCfg.PromptVersion,
		SourceTextHash: report.SourceTextHash,
	}

	// Classification is decided BEFORE the translation (so the
	// caller can log "skipping IT (matching key)" early), but
	// the Created/Retranslated list append happens AFTER the
	// translation + upsert succeed. A failure between
	// classification and success lands in FailedLanguages.
	classification := "created"
	switch {
	case ShouldSkip(existing, key):
		report.SkippedLanguages = append(report.SkippedLanguages, targetLang)
		return nil
	case ShouldRetranslate(existing, key):
		classification = "retranslated"
	}

	// (d) Translate the source TextContent into targetLang.
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
	// Thread the active TranslationPolicy into the Translate
	// call. "auto" + unknown → nil (server default); the
	// concrete model name comes from the operator-curated
	// TranslationModel.
	if m.resolverCfg.TranslationModel != "" {
		cmd.ModelPolicy = &translation.ModelPolicy{
			Provider: "ollama",
			Model:    m.resolverCfg.TranslationModel,
		}
	}

	translated, err := m.translator.Translate(ctx, cmd)
	if err != nil {
		return &ErrTranslationFailed{
			AssetID:       report.AssetID,
			TargetLang:    targetLang,
			TextKind:      report.Kind,
			Cause:         err,
			AttemptedText: source.TextContent,
		}
	}

	// (e) Build the new READY track.
	confidence := translated.Confidence
	newTrack := asset.TextTrack{
		AssetID:            report.AssetID,
		LanguageCode:       targetLang,
		TextKind:           report.Kind,
		TextContent:        translated.TranslatedText,
		SourceType:         asset.TextSourceTranslation,
		SourceLanguageCode: report.SourceLanguage,
		IsOriginal:         false,
		Provider:           translated.UsedProvider,
		ModelName:          translated.UsedModel,
		ModelVersion:       m.resolverCfg.ModelVersion,
		TextHash:           ComputeSourceTextHash(translated.TranslatedText),
		SourceVersion:      source.SourceVersion,
		Confidence:         &confidence,
		Status:             asset.TextTrackReady,
	}
	if confidence == 0 {
		newTrack.Confidence = nil
	}

	if err := m.repo.UpsertBatch(ctx, []asset.TextTrack{newTrack}); err != nil {
		return fmt.Errorf("upsert READY: %w", err)
	}

	switch classification {
	case "created":
		report.CreatedLanguages = append(report.CreatedLanguages, targetLang)
	case "retranslated":
		report.RetranslatedLanguages = append(report.RetranslatedLanguages, targetLang)
	}
	return nil
}

// emitAssetIndexRequested enqueues the canonical
// asset.index.requested event so the Qdrant reindex pipeline
// picks up the new READY tracks.
func (m *Materializer) emitAssetIndexRequested(
	ctx context.Context,
	assetID string,
	kind asset.TextTrackKind,
) error {
	payload, err := json.Marshal(map[string]string{
		"asset_id": assetID,
		"kind":     string(kind),
		"reason":   "asset.text.materialize complete",
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = m.outbox.Enqueue(
		ctx, nil,
		outboxevents.EventAssetIndexRequested,
		assetID,
		"asset",
		string(payload),
		"",
	)
	return err
}
