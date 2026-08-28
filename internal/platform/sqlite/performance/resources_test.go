package performance

import (
	"context"
	"database/sql"
	"testing"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	_ "github.com/mattn/go-sqlite3"
)

const resourceDDL = `CREATE TABLE resource_observations (
 observation_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, job_id TEXT NOT NULL,
 worker_id TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', observed_at TEXT NOT NULL,
 cpu_avg_pct REAL, cpu_peak_pct REAL, rss_avg_bytes INTEGER, rss_peak_bytes INTEGER,
 gpu_avg_pct REAL, gpu_peak_pct REAL, vram_peak_bytes INTEGER, encoder_avg_pct REAL,
 temperature_peak_c REAL, disk_read_bytes INTEGER, disk_write_bytes INTEGER,
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
	o := capperformance.ResourceObservation{ObservationID: "obs-1", RunID: "run-1", JobID: "job-1", WorkerID: "worker-1", Host: "host-1", ObservedAt: "2026-08-28T00:00:00Z", CPUAvgPct: &cpu}
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

func TestSamplerPersistsCanonicalIdentity(t *testing.T) {
	store := &fakeStore{}
	sampler, err := NewSampler(fakeProvider{observation: capperformance.ResourceObservation{CPUAvgPct: func() *float64 { v := 12.0; return &v }()}}, store)
	if err != nil {
		t.Fatal(err)
	}
	o, err := sampler.Sample(context.Background(), capperformance.SampleIdentity{RunID: "run-2", JobID: "job-2", WorkerID: "worker-2", Host: "host-2"})
	if err != nil {
		t.Fatal(err)
	}
	if o.RunID != "run-2" || o.JobID != "job-2" || o.WorkerID != "worker-2" || o.Host != "host-2" {
		t.Fatalf("identity=%+v", o)
	}
	if store.observation.ObservationID == "" {
		t.Fatal("sampler must assign observation id")
	}
}
