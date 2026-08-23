// Package scripts — processor_persistence.go is the SINGLE PERSISTENCE
// OWNER (PR 5/6, June 2026). The engine no longer writes to the scripts
// table; this processor is the only writer. Enabled as "persistence"
// in the plan's Postprocessors list.
//
// Idempotency contract:
//
//   - Compute IdempotencyKey = SHA-256(plan.ID + "|" + plan.CacheKey +
//     "|" + plan.PromptVersion + "|" + plan.TargetWords + "|" +
//     plan.Language) → first 16 hex characters.
//   - Look up an existing row by IdempotencyKey via
//     repo.FindScriptByIdempotencyKey. If found, return
//     {ScriptID: existing.ID, AlreadyPersisted: true} — no insert.
//   - If not found, build the ScriptRecord with all canonical fields
//     (FinalWordCount from input.WordCount, SpecScene JSON serialised
//     into the canonical SpecScene column, ModelUsed, CacheStatus) and
//     SaveScript.
//   - The idem key MUST include target_words + language so that
//     callers who change sizing produce distinct rows rather than
//     colliding with prior runs.
//
// PR 6 storage strategy (June 2026): dedicated `idempotency_key TEXT`
// and `specscene TEXT` columns on the scripts table (see
// migrations/sqlite/100_add_idempotency_key_and_specscene_columns.sql)
// are the canonical storage. The legacy Template + TimelineJSON slots
// are intentionally LEFT EMPTY for newly-inserted rows — migration 100
// already backfilled pre-PR-6 rows into the dedicated columns (when
// the legacy shape is identifiable). ListScripts filters that still
// query `WHERE template = ?` keep working on pre-PR-6 rows; new rows
// surface under `WHERE idempotency_key = ?` which is the canonical
// lookup the persistence layer was already wired for.
//
// PersistenceProcessor is the SOLE writer of IdempotencyKey +
// SpecScene (godlike/06 single-owner-per-fact) — the engine, the
// handlers, and every other consumer MUST go through this processor
// for canonical script-row persistence.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// PersistenceProcessor writes the canonical script row. Single
// owner of SQLite scripts-table writes (PR 5, June 2026).
type PersistenceProcessor struct {
	repo ScriptRepository
	log  *zap.Logger
}

const persistenceOperationTimeout = 30 * time.Second

// stagePersistenceSQLite is the canonical STAGE name for the SQLite write
// boundary owned by the persistence processor. It nests under the processor
// stage recorded by the composite runner ("persistence") and is measured on
// the same canonical clock — never with a second ad-hoc timer.
const stagePersistenceSQLite kernobs.StageName = "persistence.sqlite"

// NewPersistenceProcessor creates a PersistenceProcessor.
// repo must be non-nil (enforced at registration time).
func NewPersistenceProcessor(repo ScriptRepository, log *zap.Logger) *PersistenceProcessor {
	return &PersistenceProcessor{repo: repo, log: log}
}

func (p *PersistenceProcessor) Name() ProcessorName { return ProcessorPersistence }

// Policy classifies persistence as ProcessorRequired: a missing-registered
// persistence or a runtime failure or empty ScriptID output is a hard
// failure. Persistence is the SINGLE writer of script-table rows on
// the canonical pipeline (PR 5) — losing it would silently drop scripts.
//
// PR 2 (June 2026): policy introduced together with the registry's
// preflight gate. The plan arg is accepted for interface uniformity
// but ignored — persistence is unconditionally required.
func (p *PersistenceProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

func (p *PersistenceProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.repo == nil {
		return nil, fmt.Errorf("%w: persistence processor: ScriptRepository not configured", scriptpkg.ErrPostprocessFailed)
	}
	if input.Text == "" {
		return &PostProcessResult{}, nil
	}
	operationCtx, cancel := context.WithTimeout(ctx, persistenceOperationTimeout)
	defer cancel()

	// Translation mutates the pipeline input before this processor runs.
	// When a source surface is available, persist it as the requested
	// source-language row first; the existing path below then persists the
	// translated target-language row. Both writes remain owned by this
	// processor and use independent language-aware idempotency keys.
	if strings.TrimSpace(input.OriginalText) != "" && strings.TrimSpace(plan.TranslateTo) != "" {
		originalPlan := *plan
		originalPlan.TranslateTo = ""
		// Keep the repaired source row distinct from legacy rows that were
		// incorrectly persisted under the source language with translated
		// content.
		originalPlan.PromptVersion = plan.PromptVersion + "|source-language"
		originalInput := input
		originalInput.Text = input.OriginalText
		originalInput.WordCount = len(strings.Fields(input.OriginalText))
		originalInput.SpecScene = originalSpecSceneForPersistence(input)
		if _, err := p.persistSourceLanguageRow(operationCtx, &originalPlan, originalInput); err != nil {
			return nil, err
		}
	}
	persistPlan := plan
	if target := strings.TrimSpace(plan.TranslateTo); target != "" {
		translatedPlan := *plan
		translatedPlan.Language = target
		translatedPlan.TranslateTo = ""
		persistPlan = &translatedPlan
	}

	// Compute idempotency key from the reconciliation tuple.
	idemKey := computeIdempotencyKey(persistPlan)

	// Look up the existing row first. Found = skip the insert.
	existing, found, lookupErr := p.repo.FindScriptByIdempotencyKey(operationCtx,
		persistPlan.ID, persistPlan.CacheKey, persistPlan.PromptVersion, persistPlan.TargetWords, persistPlan.Language)
	if lookupErr != nil {
		if p.log != nil {
			p.log.Warn("persistence processor: idempotency lookup failed, falling through to insert",
				zap.String("idem_key", idemKey),
				zap.Error(lookupErr))
		}
		// Continue to insert path — a lookup failure must not
		// abort the canonical write.
	} else if found && existing != nil && existing.ID > 0 {
		if p.log != nil {
			p.log.Info("persistence processor: idempotency hit, replay",
				zap.String("idem_key", idemKey),
				zap.Int64("existing_script_id", existing.ID))
		}
		return &PostProcessResult{
			ScriptID:         existing.ID,
			AlreadyPersisted: true,
		}, nil
	}

	// Persist a response-safe copy of the typed SpecScene. The
	// runtime result already strips local filesystem paths for API
	// responses; the scripts table must store the same sanitized
	// shape so DB readers never observe ephemeral temp paths.
	specScene := sanitizeSpecSceneOutputForPersistence(specSceneWithText(input.SpecScene, input.Text))

	// PR 5 fields: persist the canonical typed output fields on
	// the script row. PR 5 lives in the same migration window as
	// PR 1 (engine decodes payload into canonical V1), so the
	// downstream specscene is fully typed by the time we reach
	// the persistence leg.
	specSceneJSON, specJSONErr := json.Marshal(specScene)
	if specJSONErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: specscene marshal failed: %w", scriptpkg.ErrPostprocessFailed, specJSONErr)
	}

	rec := &ScriptRecord{
		Title:          persistPlan.Title,
		Topic:          persistPlan.Topic,
		Language:       persistPlan.Language,
		Tone:           persistPlan.Tone,
		Model:          persistPlan.Model,
		ModelUsed:      input.ModelUsed,
		Mode:           persistPlan.Mode,
		Status:         "completed",
		TargetWords:    persistPlan.TargetWords,
		FinalWordCount: input.WordCount,
		OutputText:     input.Text,
		NarrativeText:  input.Text,
		FullDocument:   input.Text,
		// PR 6 canonical fields — write directly to the dedicated
		// idempotency_key + specscene columns on the scripts table.
		// godlike/06 single-owner-per-fact: this processor is the
		// SOLE writer; engine / handlers MUST NOT bypass via direct
		// SaveScript calls (forbidden by Check 53-style gate
		// semantics on the persistence seam).
		SpecScene:      string(specSceneJSON),
		IdempotencyKey: idemKey,
		// Legacy slots left empty under PR 6: newly-inserted rows
		// start in the canonical shape from row zero. Pre-PR-6 rows
		// are already backfilled (migration 100) for listable
		// semantics; from this commit forward, ListScripts filters
		// reading `WHERE template = ?` no longer encounter freshly-
		// written idem-key values — they read the dedicated column.
		Template:     "",
		TimelineJSON: "",
		Version:      1,
	}

	sections := buildSectionsFromScenes(input.SpecScene.Scenes)
	var scriptID int64
	if _, stageErr := kernobs.MeasureStageReport(operationCtx, stagePersistenceSQLite, func(stageCtx context.Context) error {
		var saveErr error
		scriptID, saveErr = p.repo.SaveScript(stageCtx, rec, sections, buildStockMatchRowsFromSpecScene(input.SpecScene))
		return saveErr
	}); stageErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: SaveScript failed: %w", scriptpkg.ErrPostprocessFailed, stageErr)
	}
	if len(input.ResearchSources) > 0 {
		researchSources := make([]ports.ScriptResearchSource, 0, len(input.ResearchSources))
		for _, source := range input.ResearchSources {
			researchSources = append(researchSources, ports.ScriptResearchSource{
				ScriptID: scriptID, Source: source.Type, Query: persistPlan.Topic,
				URL: source.URL, Title: source.Title, Snippet: source.Title,
				SourceType: "web_research",
			})
		}
		if err := p.repo.SaveResearchSources(operationCtx, scriptID, researchSources); err != nil {
			return nil, fmt.Errorf("%w: persistence processor: SaveResearchSources failed: %w", scriptpkg.ErrPostprocessFailed, err)
		}
	}

	// PR 1 (SCRIPT-DOWNSTREAM-CUTOVER wave): persist the canonical
	// ManifestV2 envelope after the script row is written. The
	// manifest is the typed NEW-mode surface for Step 11B
	// downstream fan-out (replaces the legacy inline voice/image
	// collection on the script manifest). godlike/07 fail-closed:
	// a SaveManifestV2 failure aborts the postprocessor (NOT a
	// Warn+continue per the canonical surface contract — a
	// silently-dropped manifest would break the Step 11B
	// dispatcher's fan-out). The typed-error contract is
	// preserved: callers can errors.Is(err, ports.ErrSaveManifestV2NilManifest)
	// to surface a typed diagnostic at composition time.
	manifest := buildManifestV2(persistPlan, input)
	manifestBytes, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: manifest_v2 marshal failed: %w", scriptpkg.ErrPostprocessFailed, marshalErr)
	}
	if saveManifestErr := p.repo.SaveManifestV2(operationCtx, scriptID, manifestBytes); saveManifestErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: SaveManifestV2 failed: %w", scriptpkg.ErrPostprocessFailed, saveManifestErr)
	}

	if p.log != nil {
		p.log.Info("persistence processor: script row inserted",
			zap.String("idem_key", idemKey),
			zap.Int64("script_id", scriptID),
			zap.Int("word_count", input.WordCount),
			zap.String("cache_status", input.CacheStatus),
			zap.Int("manifest_items_count", len(manifest.Items)))
	}

	return &PostProcessResult{
		ScriptID: scriptID,
	}, nil
}

func buildStockMatchRowsFromSpecScene(scene scriptpkg.SpecSceneOutput) []ports.ScriptStockMatchRecord {
	rows := make([]ports.ScriptStockMatchRecord, 0)
	for _, sc := range scene.Scenes {
		stock := sc.Bindings.Stock
		if stock == nil {
			continue
		}
		rows = append(rows, ports.ScriptStockMatchRecord{
			ClipID: stock.AssetID, SegmentIndex: sc.Index,
			StockPath: stock.DriveLink, StockSource: stock.Source,
			Score: stock.Score, MatchedTerms: stock.Name,
		})
	}
	return rows
}

// originalSpecSceneForPersistence keeps the source-language row structurally
// aligned with the translated row. Translation can run before clip binding,
// so the original snapshot may have no scenes while the current input already
// has the authoritative scene IDs and clip bindings.
func originalSpecSceneForPersistence(input ProcessInput) scriptpkg.SpecSceneOutput {
	if len(input.OriginalSpecScene.Scenes) > 0 {
		return input.OriginalSpecScene
	}
	out := input.SpecScene
	if len(out.Scenes) == 0 {
		return input.OriginalSpecScene
	}
	parts := strings.Split(strings.TrimSpace(input.OriginalText), "\n\n")
	for i := range out.Scenes {
		if i < len(parts) && strings.TrimSpace(parts[i]) != "" {
			out.Scenes[i].Text = strings.TrimSpace(parts[i])
		}
	}
	return out
}

func specSceneWithText(scene scriptpkg.SpecSceneOutput, text string) scriptpkg.SpecSceneOutput {
	if len(scene.Scenes) == 0 {
		return scene
	}
	parts := strings.Split(strings.TrimSpace(text), "\n\n")
	for i := range scene.Scenes {
		if i < len(parts) && strings.TrimSpace(parts[i]) != "" {
			scene.Scenes[i].Text = strings.TrimSpace(parts[i])
		}
	}
	return scene
}

// persistSourceLanguageRow writes the original language surface without
// emitting a second manifest. The translated row remains the canonical
// manifest owner below.
func (p *PersistenceProcessor) persistSourceLanguageRow(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (int64, error) {
	if _, found, err := p.repo.FindScriptByIdempotencyKey(ctx, plan.ID, plan.CacheKey, plan.PromptVersion, plan.TargetWords, plan.Language); err == nil && found {
		return 0, nil
	}
	specSceneJSON, err := json.Marshal(sanitizeSpecSceneOutputForPersistence(input.SpecScene))
	if err != nil {
		return 0, fmt.Errorf("%w: original persistence: specscene marshal failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}
	key := computeIdempotencyKey(plan)
	rec := &ScriptRecord{
		Title: plan.Title, Topic: plan.Topic, Language: plan.Language,
		Tone: plan.Tone, Model: plan.Model, ModelUsed: input.ModelUsed,
		Mode: plan.Mode, Status: "completed", TargetWords: plan.TargetWords,
		FinalWordCount: input.WordCount, OutputText: input.Text,
		NarrativeText: input.Text, FullDocument: input.Text,
		SpecScene: string(specSceneJSON), IdempotencyKey: key, Version: 1,
	}
	id, err := p.repo.SaveScript(ctx, rec, buildSectionsFromScenes(input.SpecScene.Scenes), nil)
	if err != nil {
		return 0, fmt.Errorf("%w: original persistence: SaveScript failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}
	return id, nil
}

// computeIdempotencyKey returns the 16-hex-char SHA-256 prefix of
// the reconciliation tuple (plan.ID, plan.CacheKey, plan.PromptVersion,
// plan.TargetWords, plan.Language). Including target_words + language
// ensures that replays with different sizing produce a fresh row
// instead of colliding with a previous run.
// buildSectionsFromScenes converts SpecScene scenes into ScriptSectionRecord
// slices, preserving voiceover links from the enriched scene bindings.
// Used by PersistenceProcessor to populate script_sections rows so the
// GET /api/scripts/:id response exposes per-scene voiceover links.
func buildSectionsFromScenes(scenes []scriptpkg.SpecScene) []ports.ScriptSectionRecord {
	if len(scenes) == 0 {
		return nil
	}
	out := make([]ports.ScriptSectionRecord, 0, len(scenes))
	for _, sc := range scenes {
		voc := sc.Bindings.Voiceover
		vl := ""
		if voc != nil {
			vl = voc.Link
		}
		wc := len(strings.Fields(sc.Text))
		out = append(out, ports.ScriptSectionRecord{
			SectionType:   string(sc.Kind),
			SectionTitle:  sc.Title,
			Content:       sc.Text,
			ContentText:   sc.Text,
			SortOrder:     sc.Index,
			Index:         sc.Index,
			WordCount:     wc,
			Status:        "completed",
			VoiceoverLink: vl,
		})
	}
	return out
}

func computeIdempotencyKey(plan *scriptpkg.ResolvedGenerationPlan) string {
	tuple := fmt.Sprintf("%s|%s|%s|%d|%s",
		plan.ID,
		plan.CacheKey,
		plan.PromptVersion,
		plan.TargetWords,
		plan.Language,
	)
	sum := digest.SHA256Bytes([]byte(tuple))
	return sum[:16]
}

// sanitizeSpecSceneOutputForPersistence returns a deep copy of the
// typed specscene with ephemeral local filesystem paths removed from
// image and voiceover bindings before persistence.
func sanitizeSpecSceneOutputForPersistence(in scriptpkg.SpecSceneOutput) scriptpkg.SpecSceneOutput {
	out := in
	if len(in.Scenes) == 0 {
		return out
	}
	out.Scenes = make([]scriptpkg.SpecScene, len(in.Scenes))
	for i, scene := range in.Scenes {
		outScene := scene
		if scene.Bindings.Voiceover != nil {
			v := *scene.Bindings.Voiceover
			v.LocalPath = ""
			outScene.Bindings.Voiceover = &v
		}
		if scene.Bindings.Image != nil {
			img := *scene.Bindings.Image
			img.LocalPath = ""
			outScene.Bindings.Image = &img
		}
		out.Scenes[i] = outScene
	}
	return out
}
