// Package scripts — voiceover_group_resolver_test.go pins the
// fix/voiceover-group-resolver contract end-to-end at the unit
// level. Each test constructs a GenerationItemV2 by hand (no
// fixtures / DBs / composition root) so the round-trip is observable
// without depending on the broken tree state.
//
// Coverage matrix:
//
//   - happy path            (Jackie Chan → folder-jack-id)
//   - miss → silent fallthrough
//     (DB returns ErrGroupNotFound, no error
//     propagated, VoiceoverFolderID remains
//     empty so the processor's default-folder
//     fallback path is preserved)
//   - explicit folder id wins
//     (caller already set VoiceoverFolderID;
//     resolver MUST NOT be consulted)
//   - nil resolver → no-op
//     (test fixtures and compositions without
//     routing support continue to pass)
//   - empty group → no-op
//     (VoiceoverGroup == "" short-circuits
//     before the resolver)
//   - round-trip into ResolveGenerationPlan
//     (resolved folder id flows from
//     item.Output.VoiceoverFolderID into
//     plan.VoiceoverFolderID via BuildPlan; a
//     reader sees the canonical contract at
//     both the input and the produced plan)
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// stubVoiceoverGroupResolver is an in-memory VoiceoverGroupResolver
// used by the round-trip tests. The behaviour is controlled via the
// map (group name → folder id) and the err field; the call counter
// lets tests assert that the resolver was NOT consulted in the
// precedence / no-op cases.
type stubVoiceoverGroupResolver struct {
	folderByName map[string]string
	err          error
	calls        int
	lastParent   string
	lastName     string
}

func (s *stubVoiceoverGroupResolver) ResolveGroup(_ context.Context, parentID, name string) (string, error) {
	s.calls++
	s.lastParent = parentID
	s.lastName = name
	if s.err != nil {
		return "", s.err
	}
	if id, ok := s.folderByName[name]; ok {
		return id, nil
	}
	return "", scriptports.ErrVoiceoverGroupNotFound
}

const (
	testVOGroupParent   = "voRoot-parent-id"
	testVOGroupNameJC   = "Jackie Chan"
	testVOGroupFolderJC = "folder-jackie-chan-id"
)

// mkItemWithGroup builds a GenerationItemV2 carrying only a group
// name, no explicit folder id. The rest of the Output fields are
// zero-valued; that's fine for ResolveVoiceoverFolderForItem
// because the helper reads only VoiceoverGroup + VoiceoverFolderID.
func mkItemWithGroup(groupName string) scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:    "item-vog",
		Title: "Jackie Chan top 10",
		Output: scriptpkg.OutputSpec{
			VoiceoverGroup: groupName,
		},
	}
}

// ── Round-trip: group → folder id (happy path) ────────────────────────

// TestResolveVoiceoverFolderForItem_Roundtrip_HappyPath is the
// canonical contract pin: caller passes only `voiceover_group`,
// resolver returns the matching folder id, and BuildPlan copies it
// into the resulting plan.VoiceoverFolderID so the voiceover
// processor switches from default-folder fallback to
// GenerateWithDestination.
func TestResolveVoiceoverFolderForItem_Roundtrip_HappyPath(t *testing.T) {
	t.Parallel()

	resolver := &stubVoiceoverGroupResolver{
		folderByName: map[string]string{
			testVOGroupNameJC: testVOGroupFolderJC,
		},
	}
	item := mkItemWithGroup(testVOGroupNameJC)

	resolvedItem, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, resolver, testVOGroupParent, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, testVOGroupFolderJC, resolvedItem.Output.VoiceoverFolderID,
		"VoiceoverFolderID must be populated from the resolver")

	plan := BuildPlan(resolvedItem)
	assert.Equal(t, testVOGroupFolderJC, plan.VoiceoverFolderID,
		"BuildPlan must copy the resolved folder id onto the plan")
	assert.Equal(t, testVOGroupNameJC, plan.VoiceoverGroup,
		"group name stays on the plan for diagnostics / processor warn-path")
}

// ── Round-trip: miss = silent fallthrough ─────────────────────────────

// TestResolveVoiceoverFolderForItem_MissFallsThrough pins the
// behaviour parity with BuildVoiceoverDestination: an unknown
// group name returns ErrVoiceoverGroupNotFound which the helper
// swallows (no PlanInvalidError) — the processor's existing
// "voiceover_group set but folder missing" warning + default-folder
// fallback path remains intact. VoiceoverFolderID stays empty so
// downstream BuildPlan → processor semantics are unchanged for
// the miss case.
func TestResolveVoiceoverFolderForItem_MissFallsThrough(t *testing.T) {
	t.Parallel()

	resolver := &stubVoiceoverGroupResolver{
		folderByName: map[string]string{}, // empty → always ErrVoiceoverGroupNotFound
	}
	item := mkItemWithGroup("DoesNotExist")

	resolvedItem, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, resolver, testVOGroupParent, zap.NewNop(),
	)
	require.NoError(t, err, "miss must NOT propagate as PlanInvalid")
	assert.Empty(t, resolvedItem.Output.VoiceoverFolderID,
		"miss leaves VoiceoverFolderID empty so processor's default-folder fallback still runs")
}

// ── Precedence: explicit folder id wins ───────────────────────────────

// TestResolveVoiceoverFolderForItem_ExplicitFolderIdWins pins the
// caller-intent precedence: when both VoiceoverFolderID AND
// VoiceoverGroup are set, the resolver MUST NOT be consulted and
// the explicit folder id MUST be preserved verbatim on the plan.
// A silent override of an explicit folder id would surprise callers
// ("I asked for X but I got Y because the group name happened to
// map to a different folder").
func TestResolveVoiceoverFolderForItem_ExplicitFolderIdWins(t *testing.T) {
	t.Parallel()

	resolver := &stubVoiceoverGroupResolver{
		folderByName: map[string]string{
			testVOGroupNameJC: testVOGroupFolderJC, // would resolve to this if consulted
		},
	}
	item := mkItemWithGroup(testVOGroupNameJC)
	item.Output.VoiceoverFolderID = "explicit-folder-id"

	resolvedItem, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, resolver, testVOGroupParent, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Equal(t, "explicit-folder-id", resolvedItem.Output.VoiceoverFolderID,
		"explicit folder id must be preserved verbatim")
	assert.Equal(t, 0, resolver.calls,
		"resolver must NOT be consulted when caller already set the folder id")
}

// ── Idempotent no-op: nil resolver / empty group ──────────────────────

// TestResolveVoiceoverFolderForItem_NilResolverNoOp pins the
// composition-without-routing contract: a use case built without
// a groups resolver (test fixtures, default compositions) MUST
// continue to execute without error. This is the regression shape
// that fix/voiceover-group-resolver introduces but does NOT break.
func TestResolveVoiceoverFolderForItem_NilResolverNoOp(t *testing.T) {
	t.Parallel()

	item := mkItemWithGroup(testVOGroupNameJC)
	resolvedItem, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, nil, testVOGroupParent, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Empty(t, resolvedItem.Output.VoiceoverFolderID,
		"nil resolver must leave VoiceoverFolderID empty")
}

// TestResolveVoiceoverFolderForItem_EmptyGroupNoOp pins the
// short-circuit behaviour for callers that don't set a group at
// all (text-only scripts, voiceover disabled, etc.). The resolver
// MUST NOT be consulted because there's no group to resolve.
func TestResolveVoiceoverFolderForItem_EmptyGroupNoOp(t *testing.T) {
	t.Parallel()

	resolver := &stubVoiceoverGroupResolver{
		folderByName: map[string]string{
			testVOGroupNameJC: testVOGroupFolderJC,
		},
	}
	item := mkItemWithGroup("") // empty VoiceoverGroup

	resolvedItem, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, resolver, testVOGroupParent, zap.NewNop(),
	)
	require.NoError(t, err)
	assert.Empty(t, resolvedItem.Output.VoiceoverFolderID,
		"empty group must NOT be resolved")
	assert.Equal(t, 0, resolver.calls,
		"resolver must NOT be consulted when group name is empty")
}

// ── Infrastructure failures propagate ────────────────────────────────

// TestResolveVoiceoverFolderForItem_InfrastructureErrorPropagates
// pins that non-"not found" errors (DB outage, etc.) propagate as
// GenerationError{Phase: "voiceover_group_resolution"} so the
// operator sees the failure loudly AND the error lands in the
// same envelope used by engine failures downstream (consistency
// with GenerateOneUseCase.Execute's engine-phase error). This is
// the failure-mode distinction: a missing group → silent
// fallthrough (regression parity with BuildVoiceoverDestination);
// an infrastructure failure → fail-closed with a typed envelope.
//
// Why GenerationError, NOT ErrPlanInvalid:
//   - ErrPlanInvalid is reserved for "the plan itself was malformed"
//     (validation, structural).
//   - An infra fault during plan-build (DB lookup failing) is a
//     runtime condition, not a plan-level defect. GenerationError
//     carries Phase + ItemID + Inner so callers can route the
//     failure to retry / cancel without swallowing context.
func TestResolveVoiceoverFolderForItem_InfrastructureErrorPropagates(t *testing.T) {
	t.Parallel()

	resolver := &stubVoiceoverGroupResolver{
		err: errors.New("sqlite: database is locked"),
	}
	item := mkItemWithGroup(testVOGroupNameJC)

	got, err := ResolveVoiceoverFolderForItem(
		context.Background(), item, resolver, testVOGroupParent, zap.NewNop(),
	)
	require.Error(t, err)
	var genErr *scriptpkg.GenerationError
	require.True(t, errors.As(err, &genErr),
		"infrastructure errors must wrap as *scriptpkg.GenerationError (got %T: %v)", err, err)
	assert.Equal(t, "voiceover_group_resolution", genErr.Phase,
		"GenerationError.Phase must identify the failing phase for ops dashboards")
	assert.Equal(t, item.ID, genErr.ItemID,
		"GenerationError.ItemID must carry the item identity for retry tracking")
	assert.Contains(t, err.Error(), "sqlite: database is locked",
		"underlying infrastructure error must reach the operator via err.Error()")
	assert.Empty(t, got.Output.VoiceoverFolderID,
		"VoiceoverFolderID stays empty when the resolver fails — the caller never sees a partial mutation")
}
