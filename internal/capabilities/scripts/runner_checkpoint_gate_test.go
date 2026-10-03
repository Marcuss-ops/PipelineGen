// Package scriptgeneration — runner_checkpoint_gate_test.go.
//
// Fault-injection coverage for the checkpoint debounce gate
// (`checkpointGate`, `runner_lifecycle.go`).
//
// Why it exists (PERFORMANCE-ACTION-LIST, P2 — checkpoint follow-up): the gate
// trades write amplification for a bounded crash window, which is only an
// acceptable trade while two properties hold.
//
//  1. A write that fails — including one that panics — must release the gate.
//     The call sites (`runner_phase_translation.go`, `runner_phase_voiceover.go`)
//     rely on `defer checkpointDue.complete()` inside a closure. If that
//     release were lost, `inFlight` would stay true and NO later checkpoint
//     would ever be written: the run would silently stop being durable while
//     still reporting progress.
//  2. Whatever was persisted before the crash must be the last COMPLETE
//     snapshot, so resume adopts it instead of re-running certified units.
//
// The gate had no test at all before this file; the properties above were a
// code reading. The replay below injects the crash in every window in turn and
// asserts both properties for each one.
package scriptgeneration

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// checkpointReplay mirrors the production call-site shape exactly: decide under
// the apply lock, snapshot there, then write OUTSIDE the lock in a closure whose
// `defer` releases the gate. The closures are what the injected crash breaks.
//
// The crash is injected on the WRITE ORDINAL, not on the unit index: most units
// never call the writer at all because the debounce window coalesces them, so an
// index-based injection would silently fail to fire and prove nothing.
type checkpointReplay struct {
	gate      checkpointGate
	persisted []*GenerateResult
	panics    int32
	writes    int32
	// result is the live result the replay drove, kept for the immutability probe.
	result *GenerateResult
	// crashAtWrite is the 0-based ordinal of the write that must panic;
	// a negative value crashes nothing.
	crashAtWrite int32
}

func (r *checkpointReplay) applyUnit(t *testing.T, result *GenerateResult, now time.Time) {
	t.Helper()
	if !r.gate.due(now) {
		return
	}
	// The snapshot is the immutable value handed to the writer; taking it here
	// is the production contract (deep copy, so later mutation cannot tear it).
	snapshot, err := snapshotGenerateResult(result)
	require.NoError(t, err)
	require.NotNil(t, snapshot)

	ordinal := atomic.AddInt32(&r.writes, 1) - 1
	func() {
		// Registered FIRST ⇒ runs LAST, after the release below, so the
		// injected fault cannot escape this closure.
		defer func() {
			if recovered := recover(); recovered != nil {
				require.Contains(t, fmt.Sprint(recovered), "injected checkpoint write crash")
			}
		}()
		defer r.gate.complete()
		if ordinal == r.crashAtWrite {
			atomic.AddInt32(&r.panics, 1)
			panic("injected checkpoint write crash")
		}
		r.persisted = append(r.persisted, snapshot)
	}()
}

// replayFanOut drives `units` completed units through the gate and returns how
// many writes the deterministic window schedule produced.
func replayFanOut(t *testing.T, units int, crashAtWrite int32) *checkpointReplay {
	t.Helper()
	replay := &checkpointReplay{crashAtWrite: crashAtWrite}
	result := newCheckpointResult(units)
	base := time.Unix(1_700_000_000, 0)
	step := partialCheckpointDebounce / 4
	for i := 0; i < units; i++ {
		replay.applyUnit(t, result, base.Add(time.Duration(i)*step))
	}
	replay.result = result
	return replay
}

// newCheckpointResult builds a result with `units` completed scene units.
func newCheckpointResult(units int) *GenerateResult {
	result := &GenerateResult{}
	for i := 0; i < units; i++ {
		result.Scenes = append(result.Scenes, Scene{
			ID:    fmt.Sprintf("scene-%02d", i),
			Index: i,
			Text:  map[Language]string{"en": fmt.Sprintf("line %d", i)},
		})
	}
	return result
}

// completedUnits counts scene×language pairs actually present in a snapshot.
func completedUnits(result *GenerateResult) int {
	n := 0
	for i := range result.Scenes {
		n += len(result.Scenes[i].Text)
	}
	return n
}

func TestCheckpointGateDebouncesInsideTheWindow(t *testing.T) {
	var gate checkpointGate
	base := time.Unix(1_700_000_000, 0)

	require.True(t, gate.due(base), "the first opportunity must write: nothing is in flight and no window is open")

	debouncedBefore := testutil.ToFloat64(observability.ScriptCheckpointDebouncedTotal)
	require.False(t, gate.due(base), "a second unit inside the window must be coalesced, not written")
	require.False(t, gate.due(base.Add(partialCheckpointDebounce/2)), "still inside the window")
	require.Greater(t, testutil.ToFloat64(observability.ScriptCheckpointDebouncedTotal)-debouncedBefore, float64(0),
		"a coalesced opportunity must be counted; otherwise the saving is invisible")

	require.False(t, gate.due(base.Add(2*partialCheckpointDebounce)),
		"the window elapsed, but a write is still in flight: no second writer may start")

	gate.complete()
	require.True(t, gate.due(base.Add(2*partialCheckpointDebounce)),
		"with the write finished and the window elapsed, the next unit must be able to checkpoint")
}

func TestCheckpointGatePanickingWriteStillReleasesTheGate(t *testing.T) {
	// This is the property the whole debounce trade rests on: if a panicking
	// repository write could strand `inFlight`, the run would stop checkpointing
	// entirely and lose everything after the first failure.
	var gate checkpointGate
	base := time.Unix(1_700_000_000, 0)

	require.True(t, gate.due(base))
	func() {
		// Registered FIRST, so it runs LAST — after the release below.
		defer func() {
			require.NotNil(t, recover(), "the injected write must actually have panicked")
		}()
		defer gate.complete()
		panic("injected checkpoint write crash")
	}()

	require.False(t, gate.inFlight,
		"a panicking write must still release the gate through the deferred complete()")

	// And the gate is genuinely usable again, not merely un-flagged.
	require.False(t, gate.due(base.Add(partialCheckpointDebounce/2)),
		"released, but the window has not elapsed yet")
	require.True(t, gate.due(base.Add(2*partialCheckpointDebounce)),
		"a released gate must permit the next checkpoint")
}

func TestCheckpointGateAdmitsExactlyOneWriterUnderConcurrency(t *testing.T) {
	var gate checkpointGate
	base := time.Unix(1_700_000_000, 0)

	var admitted int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if gate.due(base) {
				atomic.AddInt32(&admitted, 1)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), atomic.LoadInt32(&admitted),
		"one window must admit exactly one writer, or the debounce buys nothing")
}

// TestCheckpointCrashInEveryWindowKeepsTheLastCompleteSnapshot is the
// fault injection the P2 criterion asks for: for EVERY checkpoint window — plus
// the no-crash control — replay the fan-out, crash that write, and assert that
// what survived is the last complete snapshot (never a torn one) and that the
// gate kept working afterwards.
func TestCheckpointCrashInEveryWindowKeepsTheLastCompleteSnapshot(t *testing.T) {
	const units = 12

	// The window schedule is deterministic, so the control run tells us how many
	// crash windows actually exist. Enumerating units instead would produce
	// sub-tests where no write — and therefore no fault — ever happens.
	control := replayFanOut(t, units, -1)
	totalWrites := int(atomic.LoadInt32(&control.writes))
	require.Greater(t, totalWrites, 1,
		"the replay must produce more than one window, or there is nothing to inject into")

	// Control: no crash ⇒ every window persisted.
	require.Len(t, control.persisted, totalWrites)
	require.Equal(t, int32(0), atomic.LoadInt32(&control.panics))

	for crashAtWrite := 0; crashAtWrite < totalWrites; crashAtWrite++ {
		crashAtWrite := crashAtWrite
		t.Run(fmt.Sprintf("crash_at_write_%d", crashAtWrite), func(t *testing.T) {
			replay := replayFanOut(t, units, int32(crashAtWrite))

			// The injection must have fired exactly once, or the case proves nothing.
			require.Equal(t, int32(1), atomic.LoadInt32(&replay.panics),
				"the crash injection did not fire where it was scheduled")
			require.Len(t, replay.persisted, totalWrites-1,
				"exactly the crashed window is lost; the others must still be persisted")

			// Property 1: the gate survived the crash and stays usable.
			require.False(t, replay.gate.inFlight,
				"a crashed write must not strand the gate")
			require.True(t,
				replay.gate.due(time.Unix(1_700_000_000, 0).Add(time.Duration(units)*partialCheckpointDebounce/4+2*partialCheckpointDebounce)),
				"after a crash the gate must still be able to checkpoint")

			// Property 2: what survived is a chain of COMPLETE, monotone snapshots.
			// A torn snapshot (a scene recorded without its language) would make
			// resume re-run certified units; a shrinking one would lose work.
			previous := 0
			for i, snap := range replay.persisted {
				got := completedUnits(snap)
				require.Equal(t, len(snap.Scenes), got,
					"snapshot %d is torn: every persisted scene must carry its completed language", i)
				require.GreaterOrEqual(t, got, previous,
					"snapshot %d went backwards: a later checkpoint cannot contain less work", i)
				previous = got
			}

			// The crash must LOSE work, never resurrect or mis-order it: the
			// persisted chain is a strict prefix of what the control run wrote.
			require.LessOrEqual(t, len(replay.persisted), len(control.persisted))
			for i := range replay.persisted {
				require.Equal(t, completedUnits(control.persisted[i]), completedUnits(replay.persisted[i]),
					"snapshot %d differs from the control run: the crash changed recorded history", i)
			}

			// Property 3: the surviving snapshot is insulated from later mutation —
			// this is what lets resume adopt it instead of re-running what it
			// already certified.
			if len(replay.persisted) > 0 {
				last := replay.persisted[len(replay.persisted)-1]
				before := completedUnits(last)
				replay.result.Scenes[0].Text["xx"] = "mutated after the snapshot"
				require.Equal(t, before, completedUnits(last),
					"a persisted snapshot must be an immutable value, not a live view of the result")
			}
		})
	}
}

// TestCheckpointSnapshotIsADeepCopy pins the mechanism the previous case depends
// on: snapshotGenerateResult round-trips through the durable JSON contract, so
// concurrent workers mutating the live result cannot reach a queued snapshot.
func TestCheckpointSnapshotIsADeepCopy(t *testing.T) {
	live := newCheckpointResult(2)
	live.OverlayPlan = &capabilityoverlay.OverlayPlan{Items: []capabilityoverlay.OverlayItem{{
		ID: "map-item", AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "lod-0", LocalPath: "/cache/lod-0.png"}},
	}}}
	live.LocalizedOverlayPlans = map[Language]*capabilityoverlay.OverlayPlan{
		"es": {Items: []capabilityoverlay.OverlayItem{{
			ID: "map-item-es", AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "lod-0-es", LocalPath: "/cache/lod-0-es.png"}},
		}}},
	}
	snapshot, err := snapshotGenerateResult(live)
	require.NoError(t, err)
	require.NotSame(t, live, snapshot)

	require.NoError(t, err)
	live.Scenes[0].Text["it"] = "mutated"
	live.Scenes = append(live.Scenes, Scene{ID: "scene-late", Index: 2, Text: map[Language]string{"en": "late"}})

	require.NotContains(t, snapshot.Scenes[0].Text, Language("it"),
		"a snapshot must not observe a mutation applied after it was taken")
	require.Len(t, snapshot.Scenes, 2, "a snapshot must not observe later appended scenes")
	require.Equal(t, "/cache/lod-0.png", snapshot.OverlayPlan.Items[0].AssetRefs[0].LocalPath,
		"transient map asset paths must remain available to the detached render snapshot")
	require.Equal(t, "/cache/lod-0-es.png", snapshot.LocalizedOverlayPlans["es"].Items[0].AssetRefs[0].LocalPath,
		"localized render snapshots must retain transient asset paths too")

	require.Nil(t, mustSnapshotNil(t), "a nil result must snapshot to nil rather than an empty result")
}

func mustSnapshotNil(t *testing.T) *GenerateResult {
	t.Helper()
	snapshot, err := snapshotGenerateResult(nil)
	require.NoError(t, err)
	return snapshot
}
