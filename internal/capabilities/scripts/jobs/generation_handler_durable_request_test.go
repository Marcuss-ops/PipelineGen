package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildDurableRunRequestUsesPersistedRequest(t *testing.T) {
	run := &scriptgen.GenerationRun{Request: scriptgen.GenerateRequest{Title: "persisted"}}
	got, err := buildDurableRunRequest(&domainScript.GenerationEnvelopeV2{}, run)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "persisted" {
		t.Fatalf("durable request = %+v, want the persisted payload", got)
	}
}

func TestStalledRunErrorClassifiesExpiredRunningRun(t *testing.T) {
	now := time.Now().UTC()
	run := &scriptgen.GenerationRun{ID: "run-stalled", Status: scriptgen.RunStatusRunning, UpdatedAt: now.Add(-2 * runStalledAfter)}
	err := stalledRunError(run, now)
	var stalled *RunStalledError
	if !errors.As(err, &stalled) || !strings.Contains(err.Error(), "RUN_STALLED") {
		t.Fatalf("stalledRunError = %v, want typed RUN_STALLED", err)
	}
	run.UpdatedAt = now.Add(-time.Minute)
	if err := stalledRunError(run, now); err != nil {
		t.Fatalf("live run classified as stalled: %v", err)
	}
}
