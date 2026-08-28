package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

// ResourceStore persists raw resource observations. It is projection-only:
// this type never samples the machine and never computes aggregates.
type ResourceStore struct{ db *sql.DB }

func NewResourceStore(db *sql.DB) (*ResourceStore, error) {
	if db == nil {
		return nil, errors.New("resource store: nil database")
	}
	return &ResourceStore{db: db}, nil
}

var _ capperformance.ResourceObservationStore = (*ResourceStore)(nil)

func (s *ResourceStore) RecordResourceObservation(ctx context.Context, o capperformance.ResourceObservation) error {
	if s == nil || s.db == nil {
		return errors.New("resource store: not configured")
	}
	if strings.TrimSpace(o.ObservationID) == "" || strings.TrimSpace(o.RunID) == "" || strings.TrimSpace(o.JobID) == "" {
		return errors.New("resource store: observation, run and job identity are required")
	}
	if strings.TrimSpace(o.ObservedAt) == "" {
		o.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resource_observations
		(observation_id,run_id,job_id,worker_id,host,observed_at,cpu_avg_pct,cpu_peak_pct,rss_avg_bytes,rss_peak_bytes,gpu_avg_pct,gpu_peak_pct,vram_peak_bytes,encoder_avg_pct,temperature_peak_c,disk_read_bytes,disk_write_bytes,network_rx_bytes,network_tx_bytes,metadata_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(observation_id) DO UPDATE SET
		run_id=excluded.run_id,job_id=excluded.job_id,worker_id=excluded.worker_id,host=excluded.host,observed_at=excluded.observed_at,
		cpu_avg_pct=excluded.cpu_avg_pct,cpu_peak_pct=excluded.cpu_peak_pct,rss_avg_bytes=excluded.rss_avg_bytes,rss_peak_bytes=excluded.rss_peak_bytes,
		gpu_avg_pct=excluded.gpu_avg_pct,gpu_peak_pct=excluded.gpu_peak_pct,vram_peak_bytes=excluded.vram_peak_bytes,encoder_avg_pct=excluded.encoder_avg_pct,
		temperature_peak_c=excluded.temperature_peak_c,disk_read_bytes=excluded.disk_read_bytes,disk_write_bytes=excluded.disk_write_bytes,
		network_rx_bytes=excluded.network_rx_bytes,network_tx_bytes=excluded.network_tx_bytes,metadata_json=excluded.metadata_json`,
		o.ObservationID, o.RunID, o.JobID, o.WorkerID, o.Host, o.ObservedAt,
		floatValue(o.CPUAvgPct), floatValue(o.CPUPeakPct), intValue(o.RSSAvgBytes), intValue(o.RSSPeakBytes),
		floatValue(o.GPUAvgPct), floatValue(o.GPUPeakPct), intValue(o.VRAMPeakBytes), floatValue(o.EncoderAvgPct), floatValue(o.TemperaturePeakC),
		intValue(o.DiskReadBytes), intValue(o.DiskWriteBytes), intValue(o.NetworkRXBytes), intValue(o.NetworkTXBytes), nonEmpty(o.MetadataJSON, "{}"))
	if err != nil {
		return fmt.Errorf("record resource observation %q: %w", o.ObservationID, err)
	}
	return nil
}

// SampleProvider isolates OS/GPU-specific collection from orchestration. A
// provider may leave unsupported fields nil.
type SampleProvider interface {
	Collect(context.Context, capperformance.SampleIdentity) (capperformance.ResourceObservation, error)
}

type Sampler struct {
	provider SampleProvider
	store    capperformance.ResourceObservationStore
}

func NewSampler(provider SampleProvider, store capperformance.ResourceObservationStore) (*Sampler, error) {
	if provider == nil || store == nil {
		return nil, errors.New("resource sampler: provider and store are required")
	}
	return &Sampler{provider: provider, store: store}, nil
}

func (s *Sampler) Sample(ctx context.Context, identity capperformance.SampleIdentity) (capperformance.ResourceObservation, error) {
	if s == nil || s.provider == nil || s.store == nil {
		return capperformance.ResourceObservation{}, errors.New("resource sampler: not configured")
	}
	if identity.RunID == "" || identity.JobID == "" {
		return capperformance.ResourceObservation{}, errors.New("resource sampler: run and job identity are required")
	}
	o, err := s.provider.Collect(ctx, identity)
	if err != nil {
		return capperformance.ResourceObservation{}, fmt.Errorf("collect resource observation: %w", err)
	}
	if o.ObservationID == "" {
		o.ObservationID = fmt.Sprintf("resource_%d", time.Now().UnixNano())
	}
	o.RunID, o.JobID, o.WorkerID, o.Host = identity.RunID, identity.JobID, identity.WorkerID, identity.Host
	if o.ObservedAt == "" {
		o.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := s.store.RecordResourceObservation(ctx, o); err != nil {
		return capperformance.ResourceObservation{}, err
	}
	return o, nil
}

func floatValue(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
func intValue(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
