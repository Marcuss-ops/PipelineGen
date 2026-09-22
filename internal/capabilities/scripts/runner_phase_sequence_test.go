package scriptgeneration

import (
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestRunnerPhaseSequenceIsCanonical pins that every phase the Runner declares
// is a registered work phase, that none is declared twice, and that no
// milestone has been smuggled in as a phase.
func TestRunnerPhaseSequenceIsCanonical(t *testing.T) {
	sequence := RunnerPhaseSequence()
	if len(sequence) == 0 {
		t.Fatal("RunnerPhaseSequence is empty")
	}

	registered := make(map[kernobs.StageName]struct{}, len(kernobs.AllStages()))
	for _, stage := range kernobs.AllStages() {
		registered[stage] = struct{}{}
	}

	seen := make(map[kernobs.StageName]int, len(sequence))
	for i, phase := range sequence {
		if prev, dup := seen[phase]; dup {
			t.Errorf("phase %q listed twice (indexes %d and %d)", phase, prev, i)
		}
		seen[phase] = i
		if _, ok := registered[phase]; !ok {
			t.Errorf("phase %q is not in the observability registry (AllStages)", phase)
		}
		for _, milestone := range kernobs.StageMilestones() {
			if milestone == phase {
				t.Errorf("milestone %q must not be part of the phase sequence", phase)
			}
		}
	}
}

// TestRunnerPhaseSequenceMatchesRunStageOrder pins the two orders together: the
// durable run stage order (model_run.go, which drives resume) and the phase
// sequence the pipeline executes must not disagree. A stage whose phase appears
// earlier than its predecessor's phase would mean the run reports progress the
// pipeline has not reached.
func TestRunnerPhaseSequenceMatchesRunStageOrder(t *testing.T) {
	position := make(map[kernobs.StageName]int, len(RunnerPhaseSequence()))
	for i, phase := range RunnerPhaseSequence() {
		position[phase] = i
	}

	previous := -1
	for _, stage := range stageOrder {
		phase, ok := runStagePhase(stage)
		if !ok {
			t.Fatalf("durable run stage %q has no observability phase", stage)
		}
		index, ok := position[phase]
		if !ok {
			t.Fatalf("phase %q for run stage %q is missing from RunnerPhaseSequence()", phase, stage)
		}
		if index <= previous {
			t.Fatalf("run stage %q (phase %q) is out of order: index %d after %d", stage, phase, index, previous)
		}
		previous = index
	}
}

// TestRunStagePhaseExcludesMilestonesAndTerminals pins that the bridge never
// maps a stage that is not a phase: CORE_READY has no work of its own, and the
// terminal stages are outcomes, not boundaries.
func TestRunStagePhaseExcludesMilestonesAndTerminals(t *testing.T) {
	for _, stage := range []Stage{StageCompleted, StageFailed, StageCoreReady} {
		if phase, ok := runStagePhase(stage); ok {
			t.Errorf("stage %q mapped to phase %q; milestones and terminals are not phases", stage, phase)
		}
	}
}
