// Package jobs — parent_aggregator_state_machine_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// STATE_MACHINE test surface (mirror of parent_state_machine.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the 3 Test funcs
// that exercise the domainToVoiceoverParentState mapping function:
//
//   - PermanentVoiceError_RequiredChildFailsParent
//     (REQUIRED child permanent-fail → voiceover.ParentFailed)
//
//   - OptionalVoiceError
//     (OPTIONAL child permanent-fail → voiceover.ParentSucceeded or
//     ParentPartialSuccess, NEVER ParentFailed)
//
//   - RequiredFlagPropagatedFromChildPayload
//     (Payload.Required flag on child propagates to StateMachine and
//     thus to the voiceover wire-shape mapping)
//
// All test funcs exercise the STATE_MACHINE mapping only
// (parent_state_machine.go::domainToVoiceoverParentState). They use
// the domain job.StateMachine directly (not the aggregator's Tick API)
// to pinpoint the mapping logic in isolation. godlike/07 minimum-
// blast-radius: pure code-motion, no logic change.
package jobs

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────
// P0 #3 (FASE 2): REQUIRED child permanent-fail → ParentFailed.
// The StateMachine's REQUIRED-fail short-circuit maps to
// domain.ParentStateFailedTerminal → voiceover.ParentFailed.
// ─────────────────────────────────────────────────────────────────

// TestPermanentVoiceError_RequiredChildFailsParent pins the
// domainToVoiceoverParentState mapping for the REQUIRED-fail
// short-circuit path. A REQUIRED child whose outcome is permanent
// failure (status=StatusFailed, Required=true) MUST cascade to
// the parent as voiceover.ParentFailed — the canonical
// "all children definitively failed" terminal.
//
// Construction: a StateMachine with 1 child that has been
// transitioned with (Succeeded=false, Required=true, Status=StatusFailed
// + permanent-error message). sm.Compute() promotes the StateMachine
// to FailedTerminal. domainToVoiceoverParentState MUST return
// voiceover.ParentFailed.
func TestPermanentVoiceError_RequiredChildFailsParent(t *testing.T) {
	sm := job.NewStateMachine("parent-required-fail", 1)
	require.NoError(t, sm.TransitionToWaitingChildren([]string{"c-required"}))

	// 1 REQUIRED child, broker-FAILED with a permanent error message.
	// Permanent = broker-StatusFailed (terminal) + a non-empty error.
	require.NoError(t, sm.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-required-fail",
		ChildJobID:  "c-required",
		Outcome: job.ChildOutcome{
			JobID:     "c-required",
			Succeeded: false,
			Required:  true,
			Error:     "tts_failed: Edge TTS connection refused (permanent)",
			Status:    string(job.StatusFailed),
		},
	}))
	require.NoError(t, sm.Compute())

	got := domainToVoiceoverParentState(sm)
	assert.Equal(t, voiceover.ParentFailed, got,
		"P0 #3 (FASE 2): REQUIRED child permanent-fail MUST cascade to voiceover.ParentFailed (canonical all-children-definitively-failed terminal)")
}

// ─────────────────────────────────────────────────────────────────
// P0 #3 (FASE 2): OPTIONAL child permanent-fail → NOT ParentFailed.
// The StateMachine distinguishes REQUIRED vs OPTIONAL via the
// job.ChildOutcome.Required flag. An OPTIONAL failure does NOT
// short-circuit the parent to FailedTerminal; instead it
// transitions the StateMachine to ParentStateSucceeded +
// len(Failed())>0 → voiceover.ParentPartialSuccess (succeeded-with-
// warnings semantics).
// ─────────────────────────────────────────────────────────────────

// TestOptionalVoiceError pins the OPTIONAL-fail mapping path. An
// OPTIONAL child whose outcome is permanent failure MUST NOT
// cascade to voiceover.ParentFailed. The mapping MUST yield either
// voiceover.ParentPartialSuccess (mixed: succeeded child + optional-fail
// child) or voiceover.ParentSucceeded (single optional child + parent
// decides optional doesn't count as failure).
//
// Construction: 2 children, 1 succeeded REQUIRED + 1 failed OPTIONAL.
// The StateMachine transitions to ParentStateSucceeded with a non-empty
// Failed() list. domainToVoiceoverParentState MUST return
// voiceover.ParentPartialSuccess per the
// "Succeeded + ≥1 failure → partial_success" contract documented
// in parent_state_machine.go.
func TestOptionalVoiceError(t *testing.T) {
	sm := job.NewStateMachine("parent-optional-fail", 2)
	require.NoError(t, sm.TransitionToWaitingChildren([]string{"c-it", "c-en-optional"}))

	// 1 REQUIRED child, SUCCEEDED.
	require.NoError(t, sm.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-optional-fail",
		ChildJobID:  "c-it",
		Outcome: job.ChildOutcome{
			JobID:     "c-it",
			Succeeded: true,
			Required:  true,
			Status:    string(job.StatusSucceeded),
		},
	}))

	// 1 OPTIONAL child, broker-FAILED with a permanent error.
	// OPTIONAL failures are tolerated — they don't crash the parent.
	require.NoError(t, sm.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-optional-fail",
		ChildJobID:  "c-en-optional",
		Outcome: job.ChildOutcome{
			JobID:     "c-en-optional",
			Succeeded: false,
			Required:  false, // OPTIONAL — parent tolerates failure
			Error:     "tts_failed: Deepgram rate-limit (permanent)",
			Status:    string(job.StatusFailed),
		},
	}))
	require.NoError(t, sm.Compute())

	got := domainToVoiceoverParentState(sm)

	// Critical: MUST NOT be ParentFailed (OPTIONAL failure is tolerated).
	assert.NotEqual(t, voiceover.ParentFailed, got,
		"P0 #3 (FASE 2): OPTIONAL failure MUST NOT cascade to voiceover.ParentFailed (optional-fail is tolerated)")

	// Expected: ParentPartialSuccess (mixed) per the wire-shape contract
	// "Succeeded + ≥1 failure → partial_success".
	assert.Equal(t, voiceover.ParentPartialSuccess, got,
		"P0 #3 (FASE 2): REQUIRED succeeded + OPTIONAL failed → voiceover.ParentPartialSuccess (succeeded-with-warnings)")
}

// ─────────────────────────────────────────────────────────────────
// FASE 2 contract pin: Payload.Required flag on child payload must
// reach the StateMachine. Asymmetric REQUIRED/OPTIONAL outcomes
// MUST yield different parent classifications.
// ─────────────────────────────────────────────────────────────────

// TestRequiredFlagPropagatedFromChildPayload pins the REQUIRED-flag
// propagation contract across the FASE 2 typed-DTO pipeline:
//   - internal/application/voiceover/jobs/parent_aggregator_aggregate.go
//     unmarshals child.Payload into VoiceoverChildPayload and reads
//     the Required flag.
//   - The flag is then passed to job.ChildOutcome.Required on the
//     Transition event.
//   - domainToVoiceoverParentState uses sm.Failed() length + sm.State()
//     to decide the classification.
//
// The asymmetry: same child outcome pattern (1 REQUIRED succeed +
// 1 FAILED child) with the FAILED child marked Required=false (OPTIONAL)
// MUST classify differently from the same pattern with Required=true.
//
// This test makes the propagation contract observable by constructing
// 2 StateMachines with mirror image sibling + child outcome patterns
// and asserting that flipping the Required bit on the failed child
// flips the parent classification from ParentFailed (no siblings)
// to ParentPartialSuccess (mixed with REQUIRED sibling succeeded).
func TestRequiredFlagPropagatedFromChildPayload(t *testing.T) {
	// Case A: 1 REQUIRED child, broker-FAILED. No siblings.
	// Expected classification: voiceover.ParentFailed.
	smA := job.NewStateMachine("parent-A-required-only", 1)
	require.NoError(t, smA.TransitionToWaitingChildren([]string{"cA"}))
	require.NoError(t, smA.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-A-required-only",
		ChildJobID:  "cA",
		Outcome: job.ChildOutcome{
			JobID:     "cA",
			Succeeded: false,
			Required:  true,
			Error:     "tts_failed: Edge TTS refused (permanent)",
			Status:    string(job.StatusFailed),
		},
	}))
	require.NoError(t, smA.Compute())
	gotA := domainToVoiceoverParentState(smA)
	assert.Equal(t, voiceover.ParentFailed, gotA,
		"FASE 2 propagation: single REQUIRED-fail → ParentFailed (no siblings to absorb the failure)")

	// Case B: 2 children, 1 REQUIRED SUCCEEDED + 1 REQUIRED FAILED.
	// Expected classification: MUST also be ParentFailed because
	// the failed child is REQUIRED — REQUIRED failures are
	// never absorbed by sibling-success in the wire-shape contract.
	smB := job.NewStateMachine("parent-B-required-mixed", 2)
	require.NoError(t, smB.TransitionToWaitingChildren([]string{"cB-it", "cB-en"}))
	require.NoError(t, smB.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-B-required-mixed",
		ChildJobID:  "cB-it",
		Outcome: job.ChildOutcome{
			JobID:     "cB-it",
			Succeeded: true,
			Required:  true,
			Status:    string(job.StatusSucceeded),
		},
	}))
	require.NoError(t, smB.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-B-required-mixed",
		ChildJobID:  "cB-en",
		Outcome: job.ChildOutcome{
			JobID:     "cB-en",
			Succeeded: false,
			Required:  true, // REQUIRED (not OPTIONAL!)
			Error:     "tts_failed: Edge TTS refused (permanent)",
			Status:    string(job.StatusFailed),
		},
	}))
	require.NoError(t, smB.Compute())
	gotB := domainToVoiceoverParentState(smB)

	// Critical: REQUIRED-fail is NOT absorbed by sibling SUCCESS
	// in the wire-shape contract. When ANY REQUIRED child failed,
	// the parent MUST still be ParentFailed (REQUIRED-fail
	// short-circuit takes precedence over sibling-success pattern).
	assert.Equal(t, voiceover.ParentFailed, gotB,
		"FASE 2 propagation: REQUIRED succeeded + REQUIRED failed → ParentFailed (REQUIRED-fail short-circuit takes precedence)")

	// Case C: 2 children, 1 REQUIRED SUCCEEDED + 1 OPTIONAL FAILED.
	// Expected: ParentPartialSuccess (mixed-and-tolerated).
	smC := job.NewStateMachine("parent-C-mixed-tolerated", 2)
	require.NoError(t, smC.TransitionToWaitingChildren([]string{"cC-it", "cC-en"}))
	require.NoError(t, smC.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-C-mixed-tolerated",
		ChildJobID:  "cC-it",
		Outcome: job.ChildOutcome{
			JobID:     "cC-it",
			Succeeded: true,
			Required:  true,
			Status:    string(job.StatusSucceeded),
		},
	}))
	require.NoError(t, smC.Transition(job.ChildTerminatedEvent{
		ParentJobID: "parent-C-mixed-tolerated",
		ChildJobID:  "cC-en",
		Outcome: job.ChildOutcome{
			JobID:     "cC-en",
			Succeeded: false,
			Required:  false, // OPTIONAL — tolerated
			Error:     "tts_failed: not critical for English subtitling",
			Status:    string(job.StatusFailed),
		},
	}))
	require.NoError(t, smC.Compute())
	gotC := domainToVoiceoverParentState(smC)
	assert.Equal(t, voiceover.ParentPartialSuccess, gotC,
		"FASE 2 propagation: REQUIRED succeeded + OPTIONAL failed → ParentPartialSuccess (OPTIONAL failure is absorbed by REQUIRED-success sibling)")

	// Sanity: Case A == Case B (REQUIRED-only-fail aliases);
	// Case C must differ from B (REQUIRED vs OPTIONAL flips the
	// classification observably).
	assert.Equal(t, gotA, gotB,
		"FASE 2: REQUIRED-only-fail (Case A) MUST equal REQUIRED-sibling-mixed (Case B) — both REQUIRED-fail to ParentFailed")
	assert.NotEqual(t, gotB, gotC,
		"FASE 2 propagation observability REQUIRED-bit MUST flip classification: REQUIRED-fail (B) ≠ OPTIONAL-fail (C)")
}
