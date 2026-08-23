package rustexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// recordingRecorder captures measurements; an optional error injects a
// metric-write failure.
type recordingRecorder struct {
	mu    sync.Mutex
	calls []kernobs.MeasuredOperation
	err   error
}

func (r *recordingRecorder) RecordOperationReport(_ context.Context, p kernobs.OperationReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, kernobs.MeasuredOperation{
		ObservationID: p.ObservationID, Operation: p.Operation, Width: p.Width, Height: p.Height,
		FPS: p.FPS, OutputCodec: p.OutputCodec, SourceSizeBytes: p.SourceSizeBytes,
		OutputSizeBytes: p.OutputSizeBytes, CPUUserMS: p.CPUUserMS, CPUSystemMS: p.CPUSystemMS,
		ElapsedMS: p.DurationMs, CacheHit: p.CacheHit, MetadataJSON: p.MetadataJSON, CreatedAt: p.CreatedAt,
	})
	return r.err
}

func (r *recordingRecorder) measurements() []kernobs.MeasuredOperation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]kernobs.MeasuredOperation(nil), r.calls...)
}

// cannedRunner is the narrow commandRunner test seam returning a canned
// response or error.
type cannedRunner struct {
	response response
	err      error
}

func (r cannedRunner) Run(_ context.Context, _ string, _ []byte) ([]byte, []byte, error) {
	if r.err != nil {
		return nil, []byte("rust boom"), r.err
	}
	payload, err := json.Marshal(r.response)
	if err != nil {
		return nil, nil, err
	}
	return append(payload, '\n'), nil, nil
}

func writeTestFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// observedClient builds a Client with the decorator wired and a canned
// runner.
func observedClient(t *testing.T, recorder *recordingRecorder, runner cannedRunner) *Client {
	t.Helper()
	client := NewClientWithExecutor(nil, nil)
	observed := NewObservedExecutor(client, recorder)
	client.SetObservedExecutor(observed)
	client.runner = runner
	return client
}

func TestObservedExecutorRecordsExactlyOneMeasurementPerOperation(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 1234)
	output := writeTestFile(t, dir, "out.mp4", 4321)

	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{response: response{
		OK: true, Operation: "normalize",
		Metrics: &OperationMetrics{WallMS: 50, CPUUserMS: 40, CPUSystemMS: 5, FramesDecoded: 900, FramesEncoded: 900},
	}})

	result, err := client.call(context.Background(), request{Operation: OperationNormalize, SourcePath: input, OutputPath: output, Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Codec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("operation must succeed")
	}
	calls := recorder.measurements()
	if len(calls) != 1 {
		t.Fatalf("exactly one measurement per operation, got %d", len(calls))
	}
	m := calls[0]
	if m.Operation != "normalize" {
		t.Fatalf("operation = %q", m.Operation)
	}
	if m.Width != 1920 || m.Height != 1080 || m.FPS != 30 || m.OutputCodec != "h264" {
		t.Fatalf("request facts not captured: %+v", m)
	}
	if m.SourceSizeBytes != 1234 {
		t.Fatalf("source bytes = %d, want 1234", m.SourceSizeBytes)
	}
	if m.OutputSizeBytes != 4321 {
		t.Fatalf("output bytes = %d, want 4321", m.OutputSizeBytes)
	}
	if m.CPUUserMS != 40 || m.CPUSystemMS != 5 {
		t.Fatalf("rust cpu metrics not captured: %+v", m)
	}
	if m.ElapsedMS < 0 {
		t.Fatalf("elapsed must be >= 0: %d", m.ElapsedMS)
	}
	if !strings.Contains(m.MetadataJSON, `"frames_decoded":900`) || !strings.Contains(m.MetadataJSON, `"frames_encoded":900`) || !strings.Contains(m.MetadataJSON, `"wall_ms":50`) {
		t.Fatalf("frames/wall not projected into metadata: %s", m.MetadataJSON)
	}
	if m.CreatedAt == "" {
		t.Fatal("created_at must be set")
	}
}

func TestObservedExecutorRustMetricsOverrideBoundaryStats(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 100)
	output := writeTestFile(t, dir, "out.mp4", 200)

	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{response: response{
		OK: true, Operation: "normalize",
		Metrics: &OperationMetrics{InputBytes: 999, OutputBytes: 888, CacheHit: true},
	}})
	if _, err := client.call(context.Background(), request{Operation: OperationNormalize, SourcePath: input, OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	m := recorder.measurements()[0]
	if m.SourceSizeBytes != 999 || m.OutputSizeBytes != 888 {
		t.Fatalf("rust-measured bytes must win: %+v", m)
	}
	if !m.CacheHit {
		t.Fatal("rust cache_hit must be captured")
	}
}

func TestObservedExecutorRecordsMeasurementOnError(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 10)

	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{err: errors.New("rust crashed")})
	if _, err := client.call(context.Background(), request{Operation: OperationProbe, SourcePath: input}); err == nil {
		t.Fatal("operation error must propagate")
	}
	calls := recorder.measurements()
	if len(calls) != 1 {
		t.Fatalf("failed operations must still be measured, got %d", len(calls))
	}
	if calls[0].Operation != "probe" || calls[0].ElapsedMS < 0 {
		t.Fatalf("unexpected failed measurement: %+v", calls[0])
	}
}

func TestObservedExecutorRecorderFailureNeverFailsOperation(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 10)

	recorder := &recordingRecorder{err: errors.New("metric db down")}
	client := observedClient(t, recorder, cannedRunner{response: response{OK: true, Operation: "probe"}})
	result, err := client.call(context.Background(), request{Operation: OperationProbe, SourcePath: input})
	if err != nil {
		t.Fatalf("metric write failure must not fail the operation: %v", err)
	}
	if !result.OK {
		t.Fatal("operation must succeed")
	}
}

func TestClientWithoutObservedExecutorDoesNotRecord(t *testing.T) {
	recorder := &recordingRecorder{}
	client := NewClientWithExecutor(nil, nil)
	client.runner = cannedRunner{response: response{OK: true, Operation: "health"}}
	if _, err := client.call(context.Background(), request{Operation: OperationHealth}); err != nil {
		t.Fatal(err)
	}
	if got := recorder.measurements(); len(got) != 0 {
		t.Fatalf("unwired decorator must not record, got %d measurements", len(got))
	}
}

func TestObservedExecutorSkipsOutputStatForMissingFile(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 10)
	recorder := &recordingRecorder{}
	// OutputPath is required by Validate but the file need not exist yet for
	// a probe-style run; missing outputs contribute 0 bytes without failing.
	client := observedClient(t, recorder, cannedRunner{response: response{OK: true, Operation: "normalize"}})
	if _, err := client.call(context.Background(), request{Operation: OperationNormalize, SourcePath: input, OutputPath: filepath.Join(dir, "missing.mp4")}); err != nil {
		t.Fatal(err)
	}
	m := recorder.measurements()[0]
	if m.OutputSizeBytes != 0 {
		t.Fatalf("missing output must contribute 0 bytes, got %d", m.OutputSizeBytes)
	}
	if m.SourceSizeBytes != 10 {
		t.Fatalf("source bytes = %d, want 10", m.SourceSizeBytes)
	}
}

func TestObservedExecutorSumsBatchInputsAndOutputs(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 1000)
	out1 := writeTestFile(t, dir, "out1.mp4", 111)
	out2 := writeTestFile(t, dir, "out2.mp4", 222)

	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{response: response{OK: true, Operation: "cut_batch"}})
	req := request{Operation: OperationCutBatch, SourcePath: input, Jobs: []cutRequestJob{{JobID: "a", OutputPath: out1}, {JobID: "b", OutputPath: out2}}}
	if _, err := client.call(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	m := recorder.measurements()[0]
	if m.SourceSizeBytes != 1000 {
		t.Fatalf("batch source bytes = %d, want 1000", m.SourceSizeBytes)
	}
	if m.OutputSizeBytes != 333 {
		t.Fatalf("batch output bytes = %d, want 333", m.OutputSizeBytes)
	}
}
