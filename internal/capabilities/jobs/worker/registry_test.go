// Package worker — AZIONE 7 (July 2026): ProducesArtifacts unit tests.
//
// Pins the SetProducesArtifacts → ProducesArtifacts round-trip and the nil-safe
// boundary contracts (nil receiver, unknown job type, overwrite).
//
// Also pins the per-stage reporting derivation (stageStatusFromBroker +
// translateToolsToExecutionTools): the durable stage table is enabled by the
// WIRED BROKER's capabilities, so a deployment upgrades by upgrading the
// broker, not by remembering to wire a new port.
package worker

import (
	"context"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stageAwareBroker is the workers' view of the production SQLite jobs plane:
// a full jobs.Broker that ALSO implements the optional durable per-stage sink.
type stageAwareBroker struct {
	*stubLeaseBroker
	upserts int
}

func (b *stageAwareBroker) UpsertJobStageStatus(context.Context, job.JobStageStatus) error {
	b.upserts++
	return nil
}

func (b *stageAwareBroker) ListJobStageStatuses(context.Context, string) ([]job.JobStageStatus, error) {
	return nil, nil
}

var (
	_ appjobs.Broker          = (*stageAwareBroker)(nil)
	_ job.JobStageStatusStore = (*stageAwareBroker)(nil)
)

// TestStageStatusIsDerivedFromTheWiredBroker pins the derivation that makes the
// stage table work in production without any new wiring: a broker WITHOUT the
// port reports no sink (godlike/07 no-fake-availability — the worker must not
// invent a store it was not given), and a broker WITH it exposes the sink to
// handlers through JobExecutionTools.
func TestStageStatusIsDerivedFromTheWiredBroker(t *testing.T) {
	if got := stageStatusFromBroker(nil); got != nil {
		t.Fatal("a nil broker must not produce a stage sink")
	}

	plain := &stubLeaseBroker{}
	if got := stageStatusFromBroker(plain); got != nil {
		t.Fatal("a broker without the stage port must not report a sink")
	}

	aware := &stageAwareBroker{stubLeaseBroker: &stubLeaseBroker{}}
	if got := stageStatusFromBroker(aware); got == nil {
		t.Fatal("a stage-aware broker must expose its sink")
	}

	tools := translateToolsToExecutionTools(context.Background(), &Tools{broker: aware}, appjobs.TypeVideoCreate)
	if tools == nil || tools.StageStatus == nil {
		t.Fatal("JobExecutionTools must expose the stage sink to handlers")
	}

	notAware := translateToolsToExecutionTools(context.Background(), &Tools{broker: plain}, appjobs.TypeVideoCreate)
	if notAware == nil || notAware.StageStatus != nil {
		t.Fatal("handlers on a broker without the port must get a nil sink, not a broken one")
	}

	// The nil-Tools branch (no broker at all) must stay nil-safe.
	noTools := translateToolsToExecutionTools(context.Background(), nil, "")
	if noTools == nil || noTools.StageStatus != nil || noTools.Progress == nil || noTools.Event == nil {
		t.Fatalf("nil Tools translation = %+v, want noop callbacks and a nil stage sink", noTools)
	}
}

// TestRegistry_ProducesArtifacts_TrueAfterSet pins the canonical
// AZIONE 7 contract: SetProducesArtifacts(jobType, true) → ProducesArtifacts
// returns true for the same jobType.
func TestRegistry_ProducesArtifacts_TrueAfterSet(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("script.generate", true)

	if !reg.ProducesArtifacts("script.generate") {
		t.Error("ProducesArtifacts(script.generate) = false after SetProducesArtifacts(true), want true")
	}
}

// TestRegistry_ProducesArtifacts_FalseByDefault pins the zero-value
// contract: a never-set job type returns false (the default).
func TestRegistry_ProducesArtifacts_FalseByDefault(t *testing.T) {
	reg := NewRegistry()

	if reg.ProducesArtifacts("unknown.job.type") {
		t.Error("ProducesArtifacts(unknown) = true for never-set type, want false (zero-value default)")
	}
}

// TestRegistry_ProducesArtifacts_ExplicitFalseOverwrites pins the
// overwrite contract: SetProducesArtifacts(jobType, false) after a prior
// Set(true) must return false.
func TestRegistry_ProducesArtifacts_ExplicitFalseOverwrites(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("script.generate", true)
	reg.SetProducesArtifacts("script.generate", false)

	if reg.ProducesArtifacts("script.generate") {
		t.Error("ProducesArtifacts(script.generate) = true after Set(false), want false (overwrite honoured)")
	}
}

// TestRegistry_ProducesArtifacts_NilReceiver pins the nil-safe contract:
// ProducesArtifacts on a nil *Registry must return false without panicking.
func TestRegistry_ProducesArtifacts_NilReceiver(t *testing.T) {
	var reg *Registry
	if reg.ProducesArtifacts("any.type") {
		t.Error("ProducesArtifacts on nil receiver returned true, want false (nil-safe)")
	}
}

// TestRegistry_SetProducesArtifacts_NilReceiver pins the nil-safe contract
// for SetProducesArtifacts: calling it on a nil receiver is a no-op (no panic).
func TestRegistry_SetProducesArtifacts_NilReceiver(t *testing.T) {
	var reg *Registry
	result := reg.SetProducesArtifacts("any.type", true)
	if result != nil {
		t.Error("SetProducesArtifacts on nil receiver returned non-nil, want nil (fluent nil-safe no-op)")
	}
}

// TestRegistry_SetProducesArtifacts_FluentChain pins the fluent builder
// contract: SetProducesArtifacts returns the receiver for chaining.
func TestRegistry_SetProducesArtifacts_FluentChain(t *testing.T) {
	reg := NewRegistry()
	result := reg.SetProducesArtifacts("a", true)
	if result != reg {
		t.Error("SetProducesArtifacts must return the receiver for fluent chaining")
	}
}

// TestRegistry_ProducesArtifacts_ConcurrentSafety exercises the RWMutex
// contract: concurrent reads of ProducesArtifacts must not race with a
// concurrent SetProducesArtifacts write.
func TestRegistry_ProducesArtifacts_ConcurrentSafety(t *testing.T) {
	reg := NewRegistry()
	reg.SetProducesArtifacts("concurrent.test", true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			reg.SetProducesArtifacts("concurrent.test", true)
		}
	}()

	// Read concurrently while the writer goroutine is still active.
	for i := 0; i < 100; i++ {
		_ = reg.ProducesArtifacts("concurrent.test")
	}
	<-done
}
