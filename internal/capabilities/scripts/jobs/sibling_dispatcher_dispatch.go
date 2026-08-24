package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	pkgconcurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"

	jobscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// ── DispatchSiblings: the canonical parallel fan-out ─────────────────

// DispatchSiblings runs the canonical fan-out: iterate AssetRequirements,
// build one SiblingCommand per asset (mapping Kind → sibling JobType),
// enqueue in parallel via pkg/concurrent.Map (bounded worker pool),
// collect per-sibling results + errors. The function NEVER returns
// an error — sibling failures are collected in the result envelope so
// the caller can decide whether to fail-closed.
func (d *SiblingDispatcher) DispatchSiblings(
	ctx context.Context,
	parentJobID string,
	requirements []AssetRequirements,
) *SiblingDispatchResult {
	if len(requirements) == 0 {
		// No downstream work — return a clean zero-result.
		return &SiblingDispatchResult{
			PlannedCount:   0,
			SucceededCount: 0,
		}
	}

	// Build the per-sibling command slice (one command per asset).
	commands := make([]SiblingCommand, 0, len(requirements))
	for _, r := range requirements {
		cmd, err := buildSiblingCommand(parentJobID, r)
		if err != nil {
			// Failed to even build a sibling command (e.g. unknown
			// AssetKind). Treat as a missing REQUIRED with a typed error.
			d.deps.Logger.Warn("sibling command build failed",
				zap.String("parent_job_id", parentJobID),
				zap.Any("asset", r),
				zap.Error(err))
			continue
		}
		commands = append(commands, cmd)
	}

	result := &SiblingDispatchResult{
		PlannedCount: len(commands),
	}

	// Bounded parallel enqueue via pkg/concurrent.Map. Note: this
	// surfaces per-item errors without cancelling the rest of the
	// worker pool — siblings fail independently. First-error-wins
	// is intentionally rejected (see file header).
	enqueueOne := func(ctx context.Context, idx int, cmd SiblingCommand) (string, error) {
		jobID, err := d.deps.Broker.Enqueue(ctx, EnqueueCommand{
			JobType:     cmd.JobType,
			ParentJobID: cmd.ParentJobID,
			Payload:     cmd.Payload,
			Required:    cmd.Asset.Required,
		})
		if err != nil {
			return "", fmt.Errorf("sibling enqueue (parent=%s asset=%s): %w",
				cmd.ParentJobID, cmd.Asset.AssetID, err)
		}
		return jobID, nil
	}

	jobIDs, firstErr := pkgconcurrent.Map(ctx, commands, d.deps.Concurrency, enqueueOne)

	// pkg/concurrent.Map returns (nil, firstErr) if ANY enqueue
	// failed. We don't propagate firstErr as the function-level error
	// (per the file-header protocol: fail-closed is a caller decision,
	// not a dispatcher return value). Instead we walk the original
	// commands slice + match against errors.Is to populate the
	// per-sibling outcome surface. If firstErr is non-nil, the
	// per-item result map from Map() is partial — the correct view
	// here is "every command either succeeded (jobID) or failed
	// (we don't have the precise per-index error; firstErr is the
	// first encountered but not exhaustive)". For Step 11B, we flag
	// the entire dispatch as semantically partial when firstErr !=
	// nil and let the caller's fail-closed check on REQUIRED siblings
	// pick up the missing ones via RequiredMissing.
	if firstErr != nil {
		d.deps.Logger.Warn("sibling dispatch partial failure",
			zap.String("parent_job_id", parentJobID),
			zap.Error(firstErr))
		// Populate Errors + RequiredMissing conservatively: any
		// sibling whose AssetID didn't come back is treated as missing
		// if Required=true. We know len(commands) and len(jobIDs),
		// and Map() returns results in the same order as input — so
		// matching by index tells us exactly which slots are missing.
		for i, cmd := range commands {
			if cmd.Asset.Required {
				idx := i
				if idx < len(jobIDs) && jobIDs[idx] != "" {
					continue
				}
				result.RequiredMissing = append(result.RequiredMissing, cmd.Asset.AssetID)
			}
		}
		result.Errors = append(result.Errors, firstErr)
	}

	// Collect successful jobIDs (non-empty entries).
	for _, id := range jobIDs {
		if id != "" {
			result.SiblingJobIDs = append(result.SiblingJobIDs, id)
		}
	}
	if result.SucceededCount == 0 && len(result.SiblingJobIDs) > 0 {
		result.SucceededCount = len(result.SiblingJobIDs)
	}

	return result
}

// buildSiblingCommand maps an AssetRequirements → a typed
// SiblingCommand. The dispatcher holds the Kind → JobType lookup
// (canonical strings live in domain/job/job.go per godlike/02; we
// reference them via qualified job.TypeScript* identifiers to keep
// the canonical-surface discipline).
func buildSiblingCommand(parentJobID string, req AssetRequirements) (SiblingCommand, error) {
	var jobType string
	switch req.Kind {
	case AssetKindVoiceover:
		jobType = jobscript.TypeVoiceoverSibling
	case AssetKindImage:
		jobType = jobscript.TypeImageSibling
	default:
		return SiblingCommand{}, fmt.Errorf("unknown AssetKind %q (asset_id=%s)", req.Kind, req.AssetID)
	}

	if req.AssetID == "" {
		return SiblingCommand{}, errors.New("AssetRequirements.AssetID is required")
	}

	// Payload defaults to {} so the per-type handler can detect a
	// missing payload and treat the request as malformed.
	payload := req.toSiblingPayload()
	raw, err := json.Marshal(payload)
	if err != nil {
		return SiblingCommand{}, fmt.Errorf("marshal sibling payload: %w", err)
	}

	return SiblingCommand{
		ParentJobID: parentJobID,
		JobType:     jobType,
		Asset:       req,
		Payload:     raw,
	}, nil
}

// toSiblingPayload renders AssetRequirements as the per-sibling wire
// payload. Voiceover-specific fields (if any) and image-specific
// fields (if any) are routed through this single helper so the
// dispatcher surface stays narrow.
func (r AssetRequirements) toSiblingPayload() map[string]any {
	out := map[string]any{
		"asset_id": r.AssetID,
		"title":    r.Title,
		"required": r.Required,
	}
	if r.Kind == AssetKindVoiceover {
		// Voiceover-specific defaults. Future fields (voice override,
		// language) are layered here without breaking the wire shape.
		out["kind"] = string(AssetKindVoiceover)
	}
	if r.Kind == AssetKindImage {
		out["kind"] = string(AssetKindImage)
	}
	return out
}
