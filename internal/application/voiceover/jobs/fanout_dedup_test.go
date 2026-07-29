package jobs

// PR-VO-FANOUT-SIBLING-COLLAPSE-FIX (live-run audit, 2026-07-04):
// regression test for the voiceover fanout bug where 2 distinct
// per-(language, voice) children collapsed onto the SAME job_id.
//
// Root cause (audit): when FanoutVoiceoversUseCase iterated
// cmd.Items and called EnqueueRequest{Type: VoiceoverGenerateItem,
// ActiveKey: <per-child-unique>} without setting CorrelationID,
// the broker's (type, correlation_id) UNIQUE INDEX
// `idx_jobs_type_correlation` (migration 036) collapsed N
// siblings onto the first one's job_id because all siblings
// inherited the parent's correlation_id through the worker ctx.
//
// Fix (fanout.go): each child now carries
//   CorrelationID: "<parentCorr>:item:<idx>"
// which makes each (Type, CorrelationID) pair distinct under the
// UNIQUE index.
//
// What this test asserts (post-fix invariant):
//   - 2-item fanout → 2 distinct child job IDs (the fix's invariant)
//   - 3-item fanout → 3 distinct child job IDs (scalability pin)
//
// What this test does NOT depend on:
//   - Real SQLite (the memoEnqueuer stub below mimics the
//     production (type, correlation_id) dedup surface in-process)
//   - appjobs.NewService construction (no Dispatcher / lease
//     machinery; the stub implements the same narrow port the
//     use case depends on, so the fan-out path is exercised
//     end-to-end through UseCase.Execute → stub.Enqueue)
//
// Per AGENTS.md Pattern 9 (typed-port discipline): the canonical
// Enqueuer interface is the narrow carrier; the memoEnqueuer
// satisfies it without inheriting *appjobs.Service.
//
// Audit-pinned regression baseline (pre-fix, would FAIL under the
// new code because the default stub-collapsed semantics would
// produce same IDs): the stub enforces the (type, correlation_id)
// UNIQUE semantics in-process, so a future regression that drops
// per-child CorrelationID falls back to the inherited-parent-corr
// path → stub records BOTH calls with the same key → both return
// the same ID → TestFanoutDistinctChildIDs FAILS visibly with the
// exact bug signature.

import (
	"context"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// memoEnqueuer mimics the canonical jobs.Service.Enqueue in-memory:
// the (Type, CorrelationID) pair gates whether the call returns an
// existing job_id (the production (type, correlation_id) UNIQUE
// collapse surface) or creates a new one. Tests can then assert
// that the fan-out N-sibling loop emits N distinct job_ids WITHOUT
// spinning up SQLite + Dispatcher + lease machinery.
//
// This is the typed-port-narrow-enqueuer pattern that
// FanoutVoiceoversUseCase depends on (see fanout.go::Enqueuer
// interface). The compile-time assertion in fanout.go
// (`var _ Enqueuer = (*appjobs.Service)(nil)`) pins that the
// production broker also satisfies this surface; the stub does too
// because the contract is the same 1-method Enqueue signature.
type memoEnqueuer struct {
	// seen keys on (Type, CorrelationID) pairs (mirrors the
	// idx_jobs_type_correlation UNIQUE index).
	seen  map[string]string
	seq   int
	calls []*job.EnqueueRequest // captures every EnqueueRequest the use case constructs (this IS the callCount)
	err   error                 // optional override to fail every call
}

func newMemoEnqueuer() *memoEnqueuer {
	return &memoEnqueuer{seen: make(map[string]string)}
}

// CallCount returns the number of Enqueue invocations the stub has
// seen. Convenience accessor mirroring the production stubEnqueuer
// pattern (parent_state_handler_test.go) so test assertions read
// identically across the subpackage.
func (m *memoEnqueuer) CallCount() int { return len(m.calls) }

func (m *memoEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	m.calls = append(m.calls, req)
	if m.err != nil {
		return nil, m.err
	}
	key := req.Type + "|" + req.CorrelationID
	if existing, hit := m.seen[key]; hit {
		// (type, correlation_id) UNIQUE collapse — returns the same
		// job_id as the first caller with this pair. This is the
		// EXACT production behavior of FindByTypeAndCorrelation
		// (internal/application/jobs/enqueue_service.go:81-89 +
		// repository.go::FindByTypeAndCorrelation).
		return &job.Job{ID: existing, Type: req.Type, CorrelationID: req.CorrelationID, ActiveKey: req.ActiveKey}, nil
	}
	m.seq++
	id := fmt.Sprintf("job_memo_%d", m.seq)
	m.seen[key] = id
	return &job.Job{ID: id, Type: req.Type, CorrelationID: req.CorrelationID, ActiveKey: req.ActiveKey}, nil
}

// dedupCmd builds an N-item voiceover.GenerateVoiceoversCommand that
// passes cmd.Validate(). N distinct (lang, voice) pairs so the
// fanout loop iterates N times and creates N distinct children under
// the post-fix invariant.
//
// Upper bound (n <= len(languages)=5): if a future caller passes
// n>5, the function panics early via require.LessOrEqual rather than
// silently truncating to len(languages) — a silent slice truncation
// would let the test pass on the wrong number of items.
func dedupCmd(t *testing.T, n int) *voiceover.GenerateVoiceoversCommand {
	t.Helper()
	require.GreaterOrEqual(t, n, 2, "dedupCmd requires at least 2 items for the sibling-collapse test")
	const maxItems = 5
	require.LessOrEqualf(t, n, maxItems,
		"dedupCmd supports up to %d items (extend the languages/voices slices to add more)",
		maxItems)
	items := make([]voiceover.VoiceoverItem, n)
	languages := []voiceover.Language{
		"en-US", "it-IT", "pt-BR", "es-ES", "de-DE",
	}
	voices := []string{
		"en-US-Aria", "it-IT-Elsa", "pt-BR-Francisca",
		"es-ES-Elvira", "de-DE-Katja",
	}
	for i := 0; i < n; i++ {
		items[i] = voiceover.VoiceoverItem{
			Text:     fmt.Sprintf("Hello world %d", i),
			Language: languages[i],
			Voice:    voices[i],
		}
	}
	return &voiceover.GenerateVoiceoversCommand{
		RequestID: "req-dedup-test",
		Items:     items,
	}
}

// TestFanoutDistinctChildIDs is the canonical regression pin for
// the sibling-collapse bug. Post-fix invariant: N items → N
// distinct child job_ids. Pre-fix would have produced 1 ID repeated
// N times because all siblings inherited the parent's correlation
// id through the worker ctx (audit-pinned at fanout.go::P0.7 comment).
func TestFanoutDistinctChildIDs(t *testing.T) {
	t.Run("two_items_two_distinct_ids", func(t *testing.T) {
		stub := newMemoEnqueuer()
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{
			Enqueuer: stub,
			Logger:   zap.NewNop(),
		})
		cmd := dedupCmd(t, 2)

		res, err := uc.Execute(context.Background(), "parent-job-1", cmd)
		require.NoError(t, err, "2-item fan-out with valid stub must succeed")
		require.NotNil(t, res, "2-item fan-out must return non-nil FanoutResult")

		require.Equal(t, 2, stub.CallCount(),
			"P0.7 invariant: fan-out loop MUST call Enqueue once per item")
		require.Equal(t, 2, len(res.ChildJobIDs),
			"P0.7 invariant: result.ChildJobIDs MUST carry one entry per item")

		// The audit-pinned invariant: the 2 child job IDs MUST be
		// distinct. Pre-fix this assertion would fail with both IDs
		// equal to the first sibling's ID (the bug observed live on
		// 2026-07-04 — child_job_ids=["job_X", "job_X"]).
		assert.NotEqual(t, res.ChildJobIDs[0], res.ChildJobIDs[1],
			"P0.7 regression-pin: distinct (lang, voice) items MUST produce distinct child job_ids "+
				"(pre-fix bug: 2 items collapsed to [id1, id1] via (type, correlation_id) UNIQUE collapse)")
	})

	t.Run("three_items_three_distinct_ids", func(t *testing.T) {
		stub := newMemoEnqueuer()
		uc := NewFanoutVoiceoversUseCase(FanoutDeps{
			Enqueuer: stub,
			Logger:   zap.NewNop(),
		})
		cmd := dedupCmd(t, 3)

		res, err := uc.Execute(context.Background(), "parent-job-2", cmd)
		require.NoError(t, err)
		require.Equal(t, 3, stub.CallCount(),
			"P0.7 scalability: 3-item fan-out must attempt 3 enqueues")
		require.Equal(t, 3, len(res.ChildJobIDs),
			"P0.7 scalability: 3 child job ids must be recorded")

		// All 3 must be pairwise-distinct (not just the first two).
		assert.NotEqual(t, res.ChildJobIDs[0], res.ChildJobIDs[1])
		assert.NotEqual(t, res.ChildJobIDs[0], res.ChildJobIDs[2])
		assert.NotEqual(t, res.ChildJobIDs[1], res.ChildJobIDs[2])
	})
}

// TestFanoutPerChildCorrelationIDDistinct pins the surfaced value
// on each EnqueueRequest: every child MUST carry a non-empty
// CorrelationID, and every child CorrelationID MUST be distinct
// from every other (so the (type, correlation_id) UNIQUE index
// idx_jobs_type_correlation collapses NOTHING across siblings).
//
// Format contract (godlike/06 SSOT, locked into fanout.go header
// comment): "<parentCorr>:item:<idx>". This substring:
//
//   - carries the parent correlation_id (so operator-side
//     `grep <parent_id>` still hits every child via LIKE)
//   - carries the sibling index (so 2 siblings with the SAME parent
//     correlation_id diverge on the per-child suffix)
//   - is bounded-length (no exponential blowup for deep n-siblings)
func TestFanoutPerChildCorrelationIDDistinct(t *testing.T) {
	stub := newMemoEnqueuer()
	uc := NewFanoutVoiceoversUseCase(FanoutDeps{
		Enqueuer: stub,
		Logger:   zap.NewNop(),
	})
	cmd := dedupCmd(t, 2)

	_, err := uc.Execute(context.Background(), "parent-job-corr", cmd)
	require.NoError(t, err)
	require.Equal(t, 2, len(stub.calls),
		"P0.7: 2 items → stub must capture 2 EnqueueRequest records")

	// Every child CorrelationID must be non-empty (post-fix).
	for i, req := range stub.calls {
		assert.NotEmpty(t, req.CorrelationID,
			"P0.7: child[%d] CorrelationID MUST be non-empty (post-fix invariant)", i)
	}

	// Every child CorrelationID must be distinct (regression pin for
	// the collapse bug).
	assert.NotEqual(t, stub.calls[0].CorrelationID, stub.calls[1].CorrelationID,
		"P0.7: child[0] and child[1] CorrelationIDs MUST be distinct "+
			"(pre-fix: both inherited parent corr via ctx → same value → UNIQUE collapse)")

	// Format contract (lockstep with fanout.go's childCorrID =
	// fmt.Sprintf("%s:item:%d", requestID, idx)): each child
	// CorrelationID must equal the parent's RequestID + ":item:<idx>"
	// with byte-exact equality (not a length-prefix coincidence).
	cmdReqID := cmd.RequestID
	assert.Equal(t, cmdReqID+":item:0", stub.calls[0].CorrelationID,
		"P0.7 format contract: child[0].CorrelationID must equal parent RequestID+\":item:0\" "+
			"(got %q — format spec: <parentCorr>:item:<idx> byte-exact)",
		stub.calls[0].CorrelationID)
	assert.Equal(t, cmdReqID+":item:1", stub.calls[1].CorrelationID,
		"P0.7 format contract: child[1].CorrelationID must equal parent RequestID+\":item:1\" "+
			"(got %q)", stub.calls[1].CorrelationID)
}

// TestMemoEnqueuerStubMirrorsProductionDedupContract pins the
// memoEnqueuer stub's behavior to the EXACT production semantics
// of FindByTypeAndCorrelation + the (type, correlation_id) UNIQUE
// index `idx_jobs_type_correlation` (migration 036). This is a
// STUB-CONTRACT test, NOT a regression test of pre-fix code (the
// pre-fix code no longer exists in the tree). The two halves
// demonstrate:
//
//  1. Pre-fix-equivalent scenario: two siblings with the SAME
//     (type, correlation_id) → stub returns the SAME job_id
//     (mirrors the live-run bug observed on 2026-07-04).
//  2. Post-fix scenario: two siblings with DISTINCT per-child
//     suffixes → stub returns DISTINCT job_ids.
//
// The post-fix-in-FanoutUseCase pin is TestFanoutDistinctChildIDs.
// This file documents WHY the stub's dedup semantics is the right
// thing to model — a future agent swapping the stub for a
// different memoEnqueuer would lose this audit-pin otherwise.
func TestMemoEnqueuerStubMirrorsProductionDedupContract(t *testing.T) {
	stub := newMemoEnqueuer()

	// Simulate two pre-fix child EnqueueRequests — both blank
	// CorrelationID; the ctx-injected fallback lands as the same
	// string on both (mimicking GetCorrelationIDFromCtx under the
	// worker ctx). The production Enqueue auto-injects this
	// id from corid.FromContext — see enqueue_service.go:60-63.
	const ctxInjectedParentCorr = "req-from-ctx-parent"
	reqPreFix1 := &job.EnqueueRequest{
		Type:          job.TypeVoiceoverGenerateItem,
		CorrelationID: ctxInjectedParentCorr,
		ActiveKey:     "voiceover:item:parent:0:hashA:en-US:Aria",
	}
	reqPreFix2 := &job.EnqueueRequest{
		Type:          job.TypeVoiceoverGenerateItem,
		CorrelationID: ctxInjectedParentCorr,                      // same ctx-derived correlation
		ActiveKey:     "voiceover:item:parent:1:hashB:it-IT:Elsa", // distinct ActiveKey — DOES NOT HELP
	}

	// Pre-fix scenario: same (type, correlation_id) → UNIQUE
	// collapse → both calls return the same job_id.
	j1, _ := stub.Enqueue(context.Background(), reqPreFix1)
	j2, _ := stub.Enqueue(context.Background(), reqPreFix2)
	assert.Equal(t, j1.ID, j2.ID,
		"P0.7 simulator: pre-fix EnqueueRequest{BLANK corr} collapses to same job_id "+
			"(the bug signature observed on 2026-07-04 — child_job_ids=['job_X','job_X'])")

	// Post-fix scenario: distinct per-child CorrelationID → distinct
	// job_ids.
	reqPostFix1 := &job.EnqueueRequest{
		Type:          job.TypeVoiceoverGenerateItem,
		CorrelationID: ctxInjectedParentCorr + ":item:0",
		ActiveKey:     "voiceover:item:parent:0:hashA:en-US:Aria",
	}
	reqPostFix2 := &job.EnqueueRequest{
		Type:          job.TypeVoiceoverGenerateItem,
		CorrelationID: ctxInjectedParentCorr + ":item:1",
		ActiveKey:     "voiceover:item:parent:1:hashB:it-IT:Elsa",
	}
	j3, _ := stub.Enqueue(context.Background(), reqPostFix1)
	j4, _ := stub.Enqueue(context.Background(), reqPostFix2)
	assert.NotEqual(t, j3.ID, j4.ID,
		"P0.7 simulator: post-fix EnqueueRequest{<parent>:item:<idx>} produces distinct job_ids "+
			"(the canonical fix signature — per-child CorrelationID bypasses the UNIQUE collapse)")
}
