// Package scripts — processor_persistence.go is the SINGLE PERSISTENCE
// OWNER (PR 6, June 2026). The engine no longer writes to the
// scripts table; this processor is the only writer. Enabled as
// "persistence" in the plan's Postprocessors list.
//
// Idempotency contract:
//
//   - Compute IdempotencyKey = SHA-256(plan.ID + "|" + plan.CacheKey +
//     "|" + plan.PromptVersion + "|" + plan.TargetWords + "|" +
//     plan.Language) → first 16 hex characters.
//   - Look up an existing row by IdempotencyKey via
//     repo.FindScriptByIdempotencyKey. If found, return
//     {ScriptID: existing.ID} (an INFO log records the hit — no
//     downstream signal propagates, single-writer contract).
//   - If not found, build the ScriptRecord with all canonical fields
//     (FinalWordCount from model.WordCount, SpecScene JSON marshalled,
//     ModelUsed, CacheStatus) and SaveScript.
//   - The idem key MUST include target_words + language so that
//     callers who change sizing produce distinct rows rather than
//     colliding with prior runs.
//
// PR 6 (June 2026) — Storage strategy: the IdempotencyKey is now
// stored on the dedicated `idempotency_key TEXT` column (no longer
// stuffed into the multi-purpose `template` slot). Likewise the
// SpecScene JSON is stored on the dedicated `specscene TEXT`
// column (no longer stuffed into the multi-purpose `timeline_json`
// slot). The Template and TimelineJSON slots remain populated on
// new rows for backward compatibility with ListScripts filters.
//
// PR 3 (June 2026): the typed PostProcessArtifact replaces the
// pre-PR-3 aggregate shape. The ScriptID flows through
// PostProcessArtifact.ScriptID. Idempotency-hit operators see an
// INFO log only (no extra postprocessing flag).
package scripts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PersistenceProcessor writes the canonical script row.
// PR 6 (June 2026): idempotency_key and specscene columns on the
// scripts table replace the pre-PR-6 dual-purpose Template /
// TimelineJSON slots.
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

// Process looks up the existing row by IdempotencyKey. On hit
// returns *PostProcessArtifact{ScriptID: existing.ID} (the
// AlreadyPersisted signal is logged but not propagated downstream).
// On miss, builds the ScriptRecord with all canonical fields and
// SaveScript with idem-key dedup.
func (p *PersistenceProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if p.repo == nil {
		return nil, fmt.Errorf("%w: persistence processor: ScriptRepository not configured", scriptpkg.ErrPostprocessFailed)
	}
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}
	if model.Text == "" {
		return &PostProcessArtifact{}, nil
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
		// PR 3 contract: idempotency hit is logged here only —
		// the result is *PostProcessArtifact{ScriptID} (the
		// pre-PR-3 replay flag is gone).
		return &PostProcessArtifact{
			ScriptID: existing.ID,
		}, nil
	}

	// Build the canonical record. PR 6: the specscene column is
	// populated with the canonical SpecSceneOutput JSON directly
	// (no longer parked in the multi-purpose timeline_json slot).
	specSceneJSON, specJSONErr := json.Marshal(model.SpecScene)
	if specJSONErr != nil {
		return nil, fmt.Errorf("%w: persistence processor: specscene marshal failed: %w", scriptpkg.ErrPostprocessFailed, specJSONErr)
	}

	rec := &ScriptRecord{
		Title:          plan.Title,
		Topic:          plan.Topic,
		Language:       plan.Language,
		Tone:           plan.Tone,
		Model:          plan.Model,
		ModelUsed:      model.ModelUsed,
		Mode:           plan.Mode,
		Status:         "completed",
		TargetWords:    plan.TargetWords,
		FinalWordCount: model.WordCount,
		OutputText:     model.Text,
		NarrativeText:  model.Text,
		FullDocument:   model.Text,
		// PR 6: dedicated idempotency_key column. The pre-PR-6
		// strategy of writing the idem key into the Template slot
		// is retired; Template is left empty so ListScripts filters
		// using `WHERE template = 'book'` (semantic template
		// values) keep working as before.
		IdempotencyKey: idemKey,
		// PR 6: dedicated specscene column. The pre-PR-6 strategy
		// of writing SpecScene JSON into the TimelineJSON slot is
		// retired.
		SpecScene: string(specSceneJSON),
		Version:   1,
	}

	scriptID, err := p.repo.SaveScript(ctx, rec, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: persistence processor: SaveScript failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}

	if p.log != nil {
		p.log.Info("persistence processor: script row inserted",
			zap.String("idem_key", idemKey),
			zap.Int64("script_id", scriptID),
			zap.Int("word_count", model.WordCount),
			zap.String("cache_status", model.CacheStatus))
	}

	return &PostProcessArtifact{
		ScriptID: scriptID,
	}, nil
}

// computeIdempotencyKey returns the 16-hex-char SHA-256 prefix of
// the reconciliation tuple (plan.ID, plan.CacheKey, plan.PromptVersion,
// plan.TargetWords, plan.Language). Including target_words + language
// ensures that replays with different sizing produce a fresh row
// instead of colliding with a previous run.
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
