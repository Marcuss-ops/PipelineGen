package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type recordingRecorder struct {
	mu    sync.Mutex
	calls []kernobs.MeasuredOperation
	err   error
}

func (r *recordingRecorder) RecordOperationReport(_ context.Context, p kernobs.OperationReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, kernobs.MeasuredOperation{ObservationID: p.ObservationID, Operation: p.Operation, SourceSizeBytes: p.SourceSizeBytes, OutputSizeBytes: p.OutputSizeBytes, CPUUserMS: p.CPUUserMS, CPUSystemMS: p.CPUSystemMS, ElapsedMS: p.DurationMs, CacheHit: p.CacheHit, MetadataJSON: p.MetadataJSON, CreatedAt: p.CreatedAt})
	return r.err
}
func (r *recordingRecorder) measurements() []kernobs.MeasuredOperation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]kernobs.MeasuredOperation(nil), r.calls...)
}

type cannedRunner struct {
	response response
	err      error
}

func (r cannedRunner) Run(_ context.Context, _ string, _ []byte) ([]byte, []byte, error) {
	if r.err != nil {
		return nil, []byte("rust boom"), r.err
	}
	b, e := json.Marshal(r.response)
	return append(b, '\n'), nil, e
}
func writeTestFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}
func observedClient(t *testing.T, recorder *recordingRecorder, runner cannedRunner) *Client {
	t.Helper()
	client := NewClientWithExecutor(nil, nil)
	client.SetObservedExecutor(NewObservedExecutor(client, recorder))
	client.runner = runner
	return client
}

func TestObservedExecutorProjectsRustMetricsWithoutSecondMeasurement(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 1234)
	output := writeTestFile(t, dir, "out.mp4", 4321)
	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{response: response{OK: true, Operation: "normalize", Metrics: &OperationMetrics{WallMS: 50, CPUUserMS: 40, CPUSystemMS: 5, FramesDecoded: 900, FramesEncoded: 900, PeakRSSBytes: 1234, DiskReadBytes: 5678, DiskWriteBytes: 910, NetworkRXBytes: 1112, NetworkTXBytes: 1314}}})
	if _, err := client.call(context.Background(), request{Operation: OperationNormalize, SourcePath: input, OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	calls := recorder.measurements()
	if len(calls) != 1 {
		t.Fatalf("measurements=%d,want 1", len(calls))
	}
	m := calls[0]
	if m.CPUUserMS != 40 || m.CPUSystemMS != 5 || m.SourceSizeBytes != 1234 || m.OutputSizeBytes != 4321 {
		t.Fatalf("canonical facts=%+v", m)
	}
	for _, want := range []string{`"frames_decoded":900`, `"frames_encoded":900`, `"wall_ms":50`, `"peak_rss_bytes":1234`, `"disk_read_bytes":5678`, `"network_tx_bytes":1314`} {
		if !strings.Contains(m.MetadataJSON, want) {
			t.Fatalf("metadata %s missing in %s", want, m.MetadataJSON)
		}
	}
}

func TestObservedExecutorPreservesMeasuredZeroAndNoDataSentinelBoundary(t *testing.T) {
	dir := t.TempDir()
	input := writeTestFile(t, dir, "in.mp4", 10)
	recorder := &recordingRecorder{}
	client := observedClient(t, recorder, cannedRunner{response: response{OK: true, Operation: "probe", Metrics: &OperationMetrics{CPUUserMS: 0, CPUSystemMS: 0, InputBytes: 0, OutputBytes: 0, FramesDecoded: 0, FramesEncoded: 0}}})
	if _, err := client.call(context.Background(), request{Operation: OperationProbe, SourcePath: input}); err != nil {
		t.Fatal(err)
	}
	if got := len(recorder.measurements()); got != 1 {
		t.Fatalf("measurements=%d,want 1", got)
	}
	if _, err := client.call(context.Background(), request{Operation: OperationProbe, SourcePath: input}); err != nil {
		t.Fatal(err)
	}
	if got := len(recorder.measurements()); got != 2 {
		t.Fatalf("measurements=%d,want 2", got)
	}
}
