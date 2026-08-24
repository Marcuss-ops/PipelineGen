package jobs

// PR-VOICEOVER-PROJECT-THREADING (canonical regression pin, 2026-07-08):
// closes the audit-pin on GenerateVoiceoversCommand.Project →
// GenerateVoiceoverItemCommand.Project threading through the fanout
// loop. Without this test, a future refactor that drops the
// `Project: cmd.Project` line in fanout.go::Execute would silently
// regress voiceover publishes onto the pre-P12 legacy FolderID
// fallback (godlike/07 minimum-blast-radius default) instead of
// the canonical `{project}/{language}/` Drive subdir layout.
//
// What this test ASSERTS (single canonical contract):
//
//	cmd.Project="X" → 2-item fan-out → both child
//	  GenerateVoiceoverItemCommand.Project values equal "X" (1:1)
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - the typed-port Enqueuer interface (fanout.go) is the
//     canonical narrow carrier; memoEnqueuer (fanout_dedup_test.go)
//     satisfies it without inheriting *appjobs.Service.
//   - GenerateVoiceoverItemCommand.Project is the canonical SOLE
//     owner of the project slot in the per-item payload; the
//     field already exists (PR-P12-VOICEOVER-SEMANTIC-FIELDS,
//     July 2026); this test pins that the FANOUT loop ACTUALLY
//     populates it (which was the audit finding).
//
// godlike/07 minimum-blast-radius: hermetic, zero DB, zero SQLite,
// zero live-stack dependency. The memoEnqueuer stub mirrors the
// production (type, correlation_id) UNIQUE surface so the fan-out
// path is exercised end-to-end through UseCase.Execute →
// stub.Enqueue → captured Payload.
//
// Type-assertion note (Go interface boxing): fanout.go writes
// `Payload: item` where `item` is a value (not pointer). Go stores
// the VALUE inside the `any` interface. Type-asserting to
// `*T` (pointer) fails because the boxed type is T (value). The
// canonical assertion is `rawCall.Payload.(T)` then take `&val`
// to access fields. This matches how `delivery.Publisher` receives
// the envelope after broker JSON round-trip.
//
// Per AGENTS.md "rebuild helpers" principle + reviewer item 1:
// `propagationCmd` was deduplicated to `dedupCmdProject(t, project)`
// — a thin wrapper that delegates the N-item build to the
// canonical `dedupCmd` in fanout_dedup_test.go and sets the
// Project field on the produced command. One helper source-of-truth;
// one wrapper; two call sites (which already differ in N).

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// dedupCmdProject wraps the canonical dedupCmd (fanout_dedup_test.go)
// with an additional Project field on the produced command. Project
// is the canonical semantic-location identifier threaded down to
// every child GenerateVoiceoverItemCommand via the fanout loop.
//
// Why a wrapper (not extend dedupCmd): keeps the existing 2-item
// regression tests in fanout_dedup_test.go untouched (no scope creep)
// while consolidating the 2-item-shape command construction. If a
// future caller needs >2 items for the Project propagation assertion,
// extend dedupCmd's languages/voices slices to >5 and the wrapper
// is forward-compatible.
func dedupCmdProject(t *testing.T, project string) *voiceover.GenerateVoiceoversCommand {
	t.Helper()
	cmd := dedupCmd(t, 2) // canonical 2-item shape (matches sibling-collapse regression pins)
	cmd.Project = project
	return cmd
}

// TestFanoutVoiceoversUseCase_PropagatesProjectPerItem is the
// canonical regression pin for the Project threading contract.
// Post-fix invariant: every child GenerateVoiceoverItemCommand
// MUST carry cmd.Project byte-equivalent. Pre-fix (the audit finding
// that surfaced this PR): Project was absent on the parent
// GenerateVoiceoversCommand struct → the fanout loop could not
// propagate it → the delivery.Publisher path builder fell through
// to the pre-P12 legacy FolderID fallback on EVERY voiceover batch.
func TestFanoutVoiceoversUseCase_PropagatesProjectPerItem(t *testing.T) {
	stub := newMemoEnqueuer()
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{
		Enqueuer: stub,
		Logger:   zap.NewNop(),
	})
	const wantProject = "storia-boxe"
	cmd := dedupCmdProject(t, wantProject)

	res, err := uc.Execute(context.Background(), "parent-job-proj", cmd)
	require.NoError(t, err, "2-item fan-out with valid Project must succeed")
	require.NotNil(t, res, "fan-out must return non-nil FanoutResult")
	require.Equal(t, 2, stub.CallCount(),
		"ThreadingCampaign: fan-out loop MUST call Enqueue once per item")

	// Per-child Project assertion: type-assert as the VALUE type (the
	// canonical Go interface boxing for `Payload: item` in fanout.go).
	// Asserting the pointer type would fail because the interface
	// boxes the value, not the address of the original local.
	require.Len(t, stub.calls, 2,
		"memoEnqueuer capture invariant: 2 items → 2 captured EnqueueRequest records")
	for i, rawCall := range stub.calls {
		val, ok := rawCall.Payload.(voiceover.GenerateVoiceoverItemCommand)
		require.Truef(t, ok,
			"ThreadingCampaign: child[%d] Payload MUST be the canonical typed envelope voiceover.GenerateVoiceoverItemCommand (got %T — fanout.go would silently regress if the broker dispatch surface changed)",
			i, rawCall.Payload)
		item := &val
		assert.Equalf(t, wantProject, item.Project,
			"ThreadingCampaign: child[%d].Project MUST equal cmd.Project byte-equivalent (got %q, want %q) — the delivery.Publisher.VoiceoverPath reads this field to compute the `{project}/{language}/` Drive subdir; a mismatch would silently degrade to the legacy fallback (FolderID or canonical voiceover ID per child goddoc)",
			i, item.Project, wantProject)
	}
}

// TestFanoutVoiceoversUseCase_EmptyProjectPassesThrough locks the
// backward-compatible fallback: a caller that does NOT set the new
// Project field (existing pre-fix callers) must continue to work —
// the per-item child carries the empty string verbatim, the
// delivery.Publisher's fallback resolves the canonical voiceover ID,
// no breaking-change regression for unmigrated callers.
func TestFanoutVoiceoversUseCase_EmptyProjectPassesThrough(t *testing.T) {
	stub := newMemoEnqueuer()
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{
		Enqueuer: stub,
		Logger:   zap.NewNop(),
	})
	const unsetProject = "" // existing pre-fix callers — graceful degradation sanity
	cmd := dedupCmdProject(t, unsetProject)

	res, err := uc.Execute(context.Background(), "parent-job-empty-proj", cmd)
	require.NoError(t, err, "2-item fan-out with empty Project must succeed (graceful degradation)")
	require.NotNil(t, res, "empty-Project fan-out must return non-nil FanoutResult")
	require.Equal(t, 2, stub.CallCount(), "empty-Project fan-out still calls Enqueue once per item")

	require.Len(t, stub.calls, 2,
		"empty-Project capture invariant: 2 items → 2 captured EnqueueRequest records")
	for i, rawCall := range stub.calls {
		val, ok := rawCall.Payload.(voiceover.GenerateVoiceoverItemCommand)
		require.Truef(t, ok,
			"empty-Project invariant: child[%d] Payload must be the canonical voiceover.GenerateVoiceoverItemCommand envelope", i)
		item := &val
		assert.Equalf(t, "", item.Project,
			"empty-Project invariant: child[%d].Project MUST stay empty (graceful-degradation path; delivery.Publisher's pre-P12 fallback resolves the canonical voiceover ID)",
			i)
	}
}
