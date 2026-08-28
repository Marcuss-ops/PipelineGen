package performance

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	_ "github.com/mattn/go-sqlite3"
)

const resourceDDL = `CREATE TABLE resource_observations (
 observation_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, job_id TEXT NOT NULL,
 attempt_id TEXT NOT NULL DEFAULT '', worker_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', observed_at TEXT NOT NULL,
 cpu_avg_pct REAL, cpu_peak_pct REAL, rss_avg_bytes INTEGER, rss_peak_bytes INTEGER,
 swap_in_bytes INTEGER, swap_out_bytes INTEGER, disk_read_bytes INTEGER, disk_write_bytes INTEGER,
 disk_util_pct REAL, io_wait_pct REAL, disk_queue_depth REAL,
 gpu_avg_pct REAL, gpu_peak_pct REAL, vram_peak_bytes INTEGER, encoder_avg_pct REAL, decoder_avg_pct REAL,
 cpu_temp_peak_c REAL, gpu_temp_peak_c REAL, temperature_peak_c REAL, throttled INTEGER,
 network_rx_bytes INTEGER, network_tx_bytes INTEGER, metadata_json TEXT NOT NULL DEFAULT '{}')`

type fakeProvider struct {
	observation capperformance.ResourceObservation
}

func (p fakeProvider) Collect(context.Context, capperformance.SampleIdentity) (capperformance.ResourceObservation, error) {
	return p.observation, nil
}

type fakeStore struct {
	observation capperformance.ResourceObservation
}

func (s *fakeStore) RecordResourceObservation(_ context.Context, o capperformance.ResourceObservation) error {
	s.observation = o
	return nil
}

func TestResourceStorePersistsNullableObservation(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(resourceDDL); err != nil {
		t.Fatal(err)
	}
	store, err := NewResourceStore(db)
	if err != nil {
		t.Fatal(err)
	}
	cpu := 74.5
	o := capperformance.ResourceObservation{ObservationID: "obs-1", RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1", WorkerID: "worker-1", Host: "host-1", ObservedAt: "2026-08-28T00:00:00Z", CPUAvgPct: &cpu}
	if err := store.RecordResourceObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var gotCPU sql.NullFloat64
	var gotGPU sql.NullFloat64
	if err := db.QueryRow(`SELECT cpu_avg_pct, gpu_avg_pct FROM resource_observations WHERE observation_id='obs-1'`).Scan(&gotCPU, &gotGPU); err != nil {
		t.Fatal(err)
	}
	if !gotCPU.Valid || gotCPU.Float64 != cpu {
		t.Fatalf("cpu=%v, want %v", gotCPU, cpu)
	}
	if gotGPU.Valid {
		t.Fatalf("unsupported GPU metric should remain NULL: %v", gotGPU)
	}
}

func TestResourceStorePersistsExtendedFields(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(resourceDDL); err != nil {
		t.Fatal(err)
	}
	store, err := NewResourceStore(db)
	if err != nil {
		t.Fatal(err)
	}
	swapIn := int64(4096)
	diskUtil := 31.5
	throttled := true
	o := capperformance.ResourceObservation{
		ObservationID: "obs-ext", RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1",
		WorkerID: "worker-1", Host: "host-1", ObservedAt: "2026-08-28T00:00:00Z",
		SwapInBytes: &swapIn, DiskUtilPct: &diskUtil, Throttled: &throttled,
	}
	if err := store.RecordResourceObservation(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var gotAttempt string
	var gotSwapIn, gotThrottled sql.NullInt64
	var gotDiskUtil sql.NullFloat64
	if err := db.QueryRow(`SELECT attempt_id, swap_in_bytes, disk_util_pct, throttled FROM resource_observations WHERE observation_id='obs-ext'`).
		Scan(&gotAttempt, &gotSwapIn, &gotDiskUtil, &gotThrottled); err != nil {
		t.Fatal(err)
	}
	if gotAttempt != "attempt-1" {
		t.Fatalf("attempt_id=%q", gotAttempt)
	}
	if !gotSwapIn.Valid || gotSwapIn.Int64 != swapIn {
		t.Fatalf("swap_in_bytes=%v", gotSwapIn)
	}
	if !gotDiskUtil.Valid || gotDiskUtil.Float64 != diskUtil {
		t.Fatalf("disk_util_pct=%v", gotDiskUtil)
	}
	if !gotThrottled.Valid || gotThrottled.Int64 != 1 {
		t.Fatalf("throttled=%v", gotThrottled)
	}
}

func TestSamplerPersistsCanonicalIdentity(t *testing.T) {
	store := &fakeStore{}
	sampler, err := NewSampler(fakeProvider{observation: capperformance.ResourceObservation{CPUAvgPct: func() *float64 { v := 12.0; return &v }()}}, store)
	if err != nil {
		t.Fatal(err)
	}
	o, err := sampler.Sample(context.Background(), capperformance.SampleIdentity{RunID: "run-2", JobID: "job-2", AttemptID: "attempt-2", WorkerID: "worker-2", Host: "host-2"})
	if err != nil {
		t.Fatal(err)
	}
	if o.RunID != "run-2" || o.JobID != "job-2" || o.AttemptID != "attempt-2" || o.WorkerID != "worker-2" || o.Host != "host-2" {
		t.Fatalf("identity=%+v", o)
	}
	if store.observation.ObservationID == "" {
		t.Fatal("sampler must assign observation id")
	}
}

// recordingStore captures every observation the loop persists.
type recordingStore struct {
	mu           sync.Mutex
	observations []capperformance.ResourceObservation
}

func (s *recordingStore) RecordResourceObservation(_ context.Context, o capperformance.ResourceObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, o)
	return nil
}

func (s *recordingStore) snapshot() []capperformance.ResourceObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capperformance.ResourceObservation(nil), s.observations...)
}

// seqProvider returns increasing CPU samples so the rolling peak is
// distinguishable from the last interval value.
type seqProvider struct {
	mu sync.Mutex
	n  float64
}

func (p *seqProvider) Collect(context.Context, capperformance.SampleIdentity) (capperformance.ResourceObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n += 10
	v := p.n
	return capperformance.ResourceObservation{CPUAvgPct: &v}, nil
}

func TestSampleLoopPersistsCanonicalIdentityAndRollingPeaks(t *testing.T) {
	store := &recordingStore{}
	sampler, err := NewSamplerWithOptions(&seqProvider{}, store, SamplerOptions{Interval: 5 * time.Millisecond, PersistTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	stop := sampler.SampleLoop(context.Background(), capperformance.SampleIdentity{
		RunID: "run-loop", JobID: "job-loop", AttemptID: "attempt-loop", WorkerID: "worker-loop", Host: "host-loop",
	}, nil)
	time.Sleep(60 * time.Millisecond)
	stop()

	obs := store.snapshot()
	if len(obs) == 0 {
		t.Fatal("loop persisted no observations")
	}
	var maxAvg float64
	for _, o := range obs {
		if o.RunID != "run-loop" || o.JobID != "job-loop" || o.AttemptID != "attempt-loop" || o.WorkerID != "worker-loop" || o.Host != "host-loop" {
			t.Fatalf("identity not stamped: %+v", o)
		}
		if o.ObservationID == "" || o.ObservedAt == "" {
			t.Fatalf("observation missing ids: %+v", o)
		}
		if o.CPUAvgPct != nil && *o.CPUAvgPct > maxAvg {
			maxAvg = *o.CPUAvgPct
		}
	}
	last := obs[len(obs)-1]
	if last.CPUPeakPct == nil || *last.CPUPeakPct != maxAvg {
		t.Fatalf("rolling peak=%v, want %v (last observation must carry the run peak so far)", last.CPUPeakPct, maxAvg)
	}

	// stop() must actually halt the loop: the observation count is frozen.
	before := len(store.snapshot())
	time.Sleep(20 * time.Millisecond)
	if after := len(store.snapshot()); after != before {
		t.Fatalf("loop kept sampling after stop: before=%d after=%d", before, after)
	}
}

func TestSampleLoopStopsOnContextCancel(t *testing.T) {
	store := &recordingStore{}
	sampler, err := NewSamplerWithOptions(&seqProvider{}, store, SamplerOptions{Interval: 5 * time.Millisecond, PersistTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := sampler.SampleLoop(ctx, capperformance.SampleIdentity{RunID: "run-cancel", JobID: "job-cancel"}, nil)
	time.Sleep(30 * time.Millisecond)
	cancel()
	stop() // must return promptly and be idempotent
	stop()
	before := len(store.snapshot())
	time.Sleep(20 * time.Millisecond)
	if after := len(store.snapshot()); after != before {
		t.Fatalf("loop kept sampling after ctx cancel: before=%d after=%d", before, after)
	}
}
