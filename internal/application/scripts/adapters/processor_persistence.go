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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PersistenceProcessor writes the canonical script row. Single
// owner of SQLite scripts-table writes (PR 5, June 2026).
type PersistenceProcessor struct {
	repo ScriptRepository
	log  *zap.Logger
}

// NewPersistenceProcessor creates a PersistenceProcessor.
// repo must be non-nil (enforced at registration time).
func NewPersistenceProcessor(repo ScriptRepository, log *zap.Logger) *PersistenceProcessor {
	return &PersistenceProcessor{repo: repo, log: log}
}

func (p *PersistenceProcessor) Name() string { return "persistence" }

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

	// Compute idempotency key from the reconciliation tuple.
	idemKey := computeIdempotencyKey(plan)

	// Look up the existing row first. Found = skip the insert.
	existing, found, lookupErr := p.repo.FindScriptByIdempotencyKey(ctx,
		plan.ID, plan.CacheKey, plan.PromptVersion, plan.TargetWords, plan.Language)
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

	// PR 5 fields: persist the canonical typed output fields on
	// the script row. PR 5 lives in the same migration window as
	// PR 1 (engine decodes payload into canonical V1), so the
	// downstream specscene is fully typed by the time we reach
	// the persistence leg.
	specSceneJSON, specJSONErr := json.Marshal(input.SpecScene)
	if specJSONErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: specscene marshal failed: %w", scriptpkg.ErrPostprocessFailed, specJSONErr)
	}

	rec := &ScriptRecord{
		Title:          plan.Title,
		Topic:          plan.Topic,
		Language:       plan.Language,
		Tone:           plan.Tone,
		Model:          plan.Model,
		ModelUsed:      input.ModelUsed,
		Mode:           plan.Mode,
		Status:         "completed",
		TargetWords:    plan.TargetWords,
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
	scriptID, err := p.repo.SaveScript(ctx, rec, sections, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: persistence processor: SaveScript failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}

	if p.log != nil {
		p.log.Info("persistence processor: script row inserted",
			zap.String("idem_key", idemKey),
			zap.Int64("script_id", scriptID),
			zap.Int("word_count", input.WordCount),
			zap.String("cache_status", input.CacheStatus))
	}

	return &PostProcessResult{
		ScriptID: scriptID,
	}, nil
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
	sum := sha256.Sum256([]byte(tuple))
	return hex.EncodeToString(sum[:])[:16]
}
