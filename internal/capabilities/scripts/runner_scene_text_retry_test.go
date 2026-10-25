package scriptgeneration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// flakyBatchGenerator implements ONLY TextGenerator (no trace/stream
// variants) so generateSceneText takes the batch branches — the only ones
// the I2 in-phase retry covers. Streaming commits incrementally and must
// never replay.
type flakyBatchGenerator struct {
	mu        sync.Mutex
	calls     int
	failFirst error
	failAll   error
	scenes    []Scene
}

func (f *flakyBatchGenerator) GenerateSceneText(_ context.Context, _ GenerateRequest) ([]Scene, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failAll != nil {
		return nil, f.failAll
	}
	if f.calls == 1 && f.failFirst != nil {
		return nil, f.failFirst
	}
	return f.scenes, nil
}

func retryTestScenes() []Scene {
	return []Scene{{ID: "s1", Index: 0, Text: map[Language]string{"en": "The harvest collapsed because the drought lasted months."}}}
}

func TestGenerateSceneTextRetriesTransientOnce(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	gen := &flakyBatchGenerator{
		failFirst: &retry.TransientInfrastructureError{Err: errors.New("ollama: connection reset")},
		scenes:    retryTestScenes(),
	}
	runner.textGen = gen
	exec := ExecutionContext{RootJobID: "root-retry", JobID: "job-retry-1", ProjectID: "p1", VideoID: "v1", CorrelationID: "c1", Attempt: 1}
	scenes, _, _, err := runner.generateSceneText(context.Background(), "run-retry-1", defaultTestRequest(), exec, sceneTextGenerationPath{})
	if err != nil {
		t.Fatalf("transient first failure must be retried, got err=%v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("expected 1 scene after retry, got %d", len(scenes))
	}
	gen.mu.Lock()
	defer gen.mu.Unlock()
	if gen.calls != 2 {
		t.Fatalf("expected exactly 2 calls (1 retry), got %d", gen.calls)
	}
}

func TestGenerateSceneTextDoesNotRetryPermanent(t *testing.T) {
	runner, _, _, _, _, _ := newTestRunner()
	gen := &flakyBatchGenerator{failAll: errors.New("generate scene text returned zero scenes")}
	runner.textGen = gen
	exec := ExecutionContext{RootJobID: "root-retry", JobID: "job-retry-2", ProjectID: "p1", VideoID: "v1", CorrelationID: "c2", Attempt: 1}
	_, _, _, err := runner.generateSceneText(context.Background(), "run-retry-2", defaultTestRequest(), exec, sceneTextGenerationPath{})
	if err == nil {
		t.Fatal("permanent failure must still fail")
	}
	gen.mu.Lock()
	defer gen.mu.Unlock()
	if gen.calls != 1 {
		t.Fatalf("permanent failure must not be retried, got %d calls", gen.calls)
	}
}
