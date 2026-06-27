// Package scripts — processor_persistence.go is the SINGLE PERSISTENCE
// OWNER (PR 5, June 2026). The engine no longer writes to the scripts
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
//     into TimelineJSON, ModelUsed, CacheStatus) and SaveScript.
//   - The idem key MUST include target_words + language so that
//     callers who change sizing produce distinct rows rather than
//     colliding with prior runs.
//
// PR 5 storage strategy: the IdempotencyKey is currently stored on
// the existing ScriptRecord.Template slot. A dedicated
// `idempotency_key TEXT` column is on the PR 6 migration plan (the
// schema-independent resolver short-circuits a one-off migration in
// this PR window while keeping the contract uniform).
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
		// PR 5: store the canonical SpecScene serialised into the
		// existing TimelineJSON slot — saves a schema migration
		// (PR 6 territory when a dedicated specscene column lands).
		TimelineJSON: string(specSceneJSON),
		// IdempotencyKey stored on Template slot. Concrete repo
		// FindByIdempotencyKey reads this back. Same migration-
		// deferral reasoning as TimelineJSON — PR 6 introduces a
		// dedicated idempotency_key column.
		Template: idemKey,
		Version:  1,
	}

	scriptID, err := p.repo.SaveScript(ctx, rec, nil, nil)
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
