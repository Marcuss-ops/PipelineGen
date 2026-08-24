// Package scripts — voiceover_group_resolver.go is the canonical
// pre-BuildPlan step that maps item.Output.VoiceoverGroup to a
// folder ID via the VoiceoverGroupResolver port and mutates the
// item so BuildPlan reflects the resolved value on
// plan.VoiceoverFolderID.
//
// fix/voiceover-group-resolver (June 2026):
//
//	Before: callers that supplied only `voiceover_group`
//	        produced a plan with VoiceoverFolderID="" and
//	        VoiceoverGroup="X"; the processor logged a warning
//	        and fell back to the default folder, silently
//	        routing the new voiceover to the wrong place.
//
//	After:  GenerateOneUseCase.Execute calls
//	        ResolveVoiceoverFolderForItem right between
//	        PhaseBuildPlan and BuildPlan. When item.Output
//	        carries only a group name (no explicit folder ID),
//	        the port is consulted and the resolved folder ID
//	        is written into item.Output.VoiceoverFolderID
//	        BEFORE BuildPlan copies it into plan.VoiceoverFolderID.
//
// The mutation shape (in-place on a copy of the item struct) is
// deliberate: GenerationItemV2 is a value type, callers pass it
// by value, and the use case already mutates other Output fields
// (NormalizeItem mutates &item directly). Returning the possibly-
// mutated item keeps the call site symmetric with NormalizeItem.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// ResolveVoiceoverFolderForItem maps item.Output.VoiceoverGroup to
// item.Output.VoiceoverFolderID via the supplied resolver port,
// mutating the item in place. Returns the (possibly mutated) item
// and a non-nil error ONLY on infrastructure-level failures; the
// "group not found" sentinel is intentionally NOT propagated so the
// processor's existing warning path (OperationMode: fallback to
// default folder) is preserved unchanged.
//
// Precedence rules (caller intent wins):
//
//   - resolver == nil                → no-op (DOES NOT FAIL — the
//     pipeline is configured without
//     routing support, which is the
//     test-fixture default).
//   - group name empty/blank         → no-op.
//   - explicit folder ID set         → no-op (caller-supplied folder
//     id wins; resolver is
//     intentionally NOT consulted).
//   - resolver returns not-found     → no-op + warn; the processor
//     falls back to the default
//     folder (matches
//     BuildVoiceoverDestination in
//     jobs/job_helpers.go, which
//     also warns and falls through
//     on ErrGroupNotFound).
//   - resolver returns non-nil error → propagate as PlanInvalid so
//     the operator sees the
//     underlying failure loudly
//     (DB outage, etc.).
//   - resolver returns folder ID     → set item.Output.VoiceoverFolderID.
//
// On success the mutated item has item.Output.VoiceoverFolderID
// populated so BuildPlan copies it downstream and the voiceover
// processor switches from default-folder fallback to
// GenerateWithDestination(...).
func ResolveVoiceoverFolderForItem(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	resolver scriptports.VoiceoverGroupResolver,
	parentID string,
	log *zap.Logger,
) (scriptpkg.GenerationItemV2, error) {
	if resolver == nil {
		return item, nil
	}

	groupName := strings.TrimSpace(item.Output.VoiceoverGroup)
	if groupName == "" {
		return item, nil
	}

	// Caller-supplied explicit folder ID wins. The resolver is
	// intentionally NOT consulted — the user spelled out the
	// folder they wanted, and silently overriding with a
	// group-derived folder would be surprising.
	if strings.TrimSpace(item.Output.VoiceoverFolderID) != "" {
		if log != nil {
			log.Debug("voiceover_group resolution skipped — caller set voiceover_folder_id explicitly",
				zap.String("voiceover_group", groupName),
				zap.String("voiceover_folder_id", item.Output.VoiceoverFolderID))
		}
		return item, nil
	}

	folderID, err := resolver.ResolveGroup(ctx, parentID, groupName)
	if err != nil {
		if errors.Is(err, scriptports.ErrVoiceoverGroupNotFound) {
			// Match BuildVoiceoverDestination's behaviour: warn and
			// fall through. The voiceover processor will emit its own
			// "voiceover_group set but not resolved" warning and use
			// the configured default folder.
			if log != nil {
				log.Warn("voiceover_group not found under parent — falling back to default folder",
					zap.String("voiceover_group", groupName),
					zap.String("parent_id", parentID))
			}
			return item, nil
		}
		// Infra-level failure (DB outage, parent missing, etc.):
		// propagate as GenerationError so the failure lands in the
		// same error envelope used by engine failures further down
		// the pipeline (consistent with `engineErr` handling in
		// GenerateOneUseCase.Execute). Plan-level validation errors
		// (ErrPlanInvalid) are reserved for "the plan itself is
		// malformed" — an infra fault resolving output fields is a
		// runtime condition, not a validation one.
		return item, &scriptpkg.GenerationError{
			ItemID: item.ID,
			Phase:  "voiceover_group_resolution",
			Inner:  fmt.Errorf("resolve voiceover_group %q under %q: %w", groupName, parentID, err),
		}
	}

	item.Output.VoiceoverFolderID = folderID
	if log != nil {
		log.Info("voiceover_group resolved to folder id",
			zap.String("voiceover_group", groupName),
			zap.String("folder_id", folderID),
			zap.String("parent_id", parentID))
	}
	return item, nil
}
