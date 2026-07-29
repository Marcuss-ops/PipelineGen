// Package texttracks — materializer.go: TextTrackMaterializer is the
// canonical application-layer service that materializes a media
// asset's text tracks into all configured target languages.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// Pipeline (canonical):
//
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
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
	AssetID               string              `json:"asset_id"`
	Kind                  asset.TextTrackKind `json:"kind"`
	SourceLanguage        string              `json:"source_language"`
	SourceTextHash        string              `json:"source_text_hash"`
	CreatedLanguages      []string            `json:"created_languages"`
	SkippedLanguages      []string            `json:"skipped_languages"`
	RetranslatedLanguages []string            `json:"retranslated_languages"`
	FailedLanguages       map[string]string   `json:"failed_languages"`
	Duration              time.Duration       `json:"duration"`
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
	repo        asset.TextTrackRepository
	translator  translation.TranslationPort
	outbox      OutboxEnqueuer
	resolverCfg ResolverConfig
	log         *zap.Logger
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
// registry languages (TranslateClips=true)" (the canonical
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
		// Operator override path: route through
		// OverrideTargetLanguages (NOT the Registry). The
		// resolver does not validate overrides against the
		// registry — backfill operations need to drill into
		// codes the operator has since removed from
		// cfg.MaterializeLanguages.
		perCallCfg.OverrideTargetLanguages = append([]string{}, targetLanguagesOverride...)
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
//
// PR-CATALOG-MULTILINGUA step 4 (July 2026): the
// lookup-before-translate gate runs FIRST via
// FindCurrentForTranslation. If a row already exists with
// matching translation_key + status=READY + is_current=1, the
// translation is REUSED and the LLM call is skipped (no LLM
// cost, no row insert). Otherwise the request is translated via
// TranslationPort and persisted via
// InsertTranslationWithAuditPredecessor (which atomically
// flips the prior is_current=1 row to 0 before inserting the
// new row, preserving the audit trail).
//
// Legacy MaterializationKey / ShouldRetranslate logic is
// retained as a soft-fallback legacy classifier (it logs the
// classification AFTER the new gate fires) so existing
// MaterializationReport classification (created vs
// retranslated) survives the migration cutover.
func (m *Materializer) materializeOne(
	ctx context.Context,
	resolver *Resolver,
	source *asset.TextTrack,
	targetLang string,
	report *MaterializationReport,
) error {
	// (Step 4) Lookup-before-translate gate. The port computes the
	// 5-tuple SHA-256 translation_key internally via
	// asset.TranslationKey (godlike/06 SSOT — one canonical
	// formula owner). If a READY + is_current=1 row already
	// exists for this exact 6-tuple (asset + kind + target lang +
	// source_text_hash + translation_model + model_version +
	// prompt_version), the translation is REUSED and the LLM
	// call is skipped. The existing track stays is_current=1;
	// no row insert, no audit flip — godlike/07 honest lock.
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
		return fmt.Errorf("find current for translation: %w", err)
	}
	if existing != nil {
		report.SkippedLanguages = append(report.SkippedLanguages, targetLang)
		m.log.Info("texttracks.materialize.translation_key_hit",
			zap.String("asset_id", report.AssetID),
			zap.String("kind", string(report.Kind)),
			zap.String("target_language", targetLang),
		)
		return nil
	}

	// (Step 4) Compute the deterministic translation fingerprint
	// for the insert path. Same canonical formula; this copy is
	// persisted on the new row via InsertTranslationWithAuditPredecessor
	// below. The lookup above and the insert here MUST share the
	// same formula values, so both go through asset.TranslationKey.
	translationKey := asset.TranslationKey(
		report.SourceTextHash,
		targetLang,
		m.resolverCfg.TranslationModel,
		m.resolverCfg.ModelVersion,
		m.resolverCfg.PromptVersion,
	)

	// Soft-fallback classifier: keep the legacy
	// created-vs-retranslated split so callers can see "this
	// row was the FIRST translation" vs "this row replaced a
	// stale prior translation" in the report. Hits go to
	// "retranslated" because auditing an old (legacy, no
	// translation_key) row as historical and a new keyed row
	// as current is exactly the audit-trail-maximising
	// shape.
	classification := "created"
	if legacyExisting, err := resolver.FindExistingTarget(ctx, targetLang); err == nil && legacyExisting != nil {
		// classifier emits "retranslated" whenever a prior
		// row is present and the new row carries a fresh
		// translation_key. The discriminator-flip side
		// effect happens in
		// InsertTranslationWithAuditPredecessor below.
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

	// (e) Build the new READY track. The translation_key
	// persisted here matches the lookup probe; the partial
	// UNIQUE INDEX WHERE is_current=1 invariant guarantees
	// no predecessor row with the same key remains is_current=1
	// (the InsertTranslationWithAuditPredecessor began with a
	// targeted UPDATE).
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
		PromptVersion:      m.resolverCfg.PromptVersion,
		TextHash:           ComputeSourceTextHash(translated.TranslatedText),
		SourceVersion:      source.SourceVersion,
		// PR-CLIPINGEST-PIPELINE step 11 (July 2026): propagate
		// the parent source_text_hash onto the fan-out target
		// row so the "multilingua — segmenti/timestamp
		// invariati" invariant is enforced end-to-end. The
		// lookup probe at the top of materializeOne already
		// feeds SourceTextHash into translation_key; without
		// this insertion-side propagation the lookup hits
		// would miss on subsequent Materialize calls for the
		// same source (the row is unreadable without a
		// SourceTextHash). One canonical owner — the
		// MaterializationReport is the SSOT for source_hash
		// and the new row copies it verbatim.
		SourceTextHash: report.SourceTextHash,
		TranslationKey: translationKey,
		IsCurrent:      true,
		Confidence:     &confidence,
		Status:         asset.TextTrackReady,
	}
	if confidence == 0 {
		newTrack.Confidence = nil
	}

	// (Step 4) Insert via the flip-and-insert path. Replaces
	// the legacy UpsertBatch call (which silently overwrote
	// rows on UNIQUE(asset_id, language_code, text_kind) and
	// lost the audit trail). The atomic flip inside the
	// transaction guarantees exactly one is_current=1 row per
	// context after the call returns.
	if err := m.repo.InsertTranslationWithAuditPredecessor(ctx, newTrack); err != nil {
		return fmt.Errorf("insert translation with audit predecessor: %w", err)
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
