package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	"go.uber.org/zap"
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
		(observation_id,run_id,job_id,attempt_id,worker_id,host,observed_at,
		 cpu_avg_pct,cpu_peak_pct,rss_avg_bytes,rss_peak_bytes,
		 swap_in_bytes,swap_out_bytes,disk_read_bytes,disk_write_bytes,
		 disk_util_pct,io_wait_pct,disk_queue_depth,
		 gpu_avg_pct,gpu_peak_pct,vram_peak_bytes,encoder_avg_pct,decoder_avg_pct,
		 cpu_temp_peak_c,gpu_temp_peak_c,temperature_peak_c,throttled,
		 network_rx_bytes,network_tx_bytes,metadata_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(observation_id) DO UPDATE SET
		run_id=excluded.run_id,job_id=excluded.job_id,attempt_id=excluded.attempt_id,worker_id=excluded.worker_id,host=excluded.host,observed_at=excluded.observed_at,
		cpu_avg_pct=excluded.cpu_avg_pct,cpu_peak_pct=excluded.cpu_peak_pct,rss_avg_bytes=excluded.rss_avg_bytes,rss_peak_bytes=excluded.rss_peak_bytes,
		swap_in_bytes=excluded.swap_in_bytes,swap_out_bytes=excluded.swap_out_bytes,disk_read_bytes=excluded.disk_read_bytes,disk_write_bytes=excluded.disk_write_bytes,
		disk_util_pct=excluded.disk_util_pct,io_wait_pct=excluded.io_wait_pct,disk_queue_depth=excluded.disk_queue_depth,
		gpu_avg_pct=excluded.gpu_avg_pct,gpu_peak_pct=excluded.gpu_peak_pct,vram_peak_bytes=excluded.vram_peak_bytes,encoder_avg_pct=excluded.encoder_avg_pct,decoder_avg_pct=excluded.decoder_avg_pct,
		cpu_temp_peak_c=excluded.cpu_temp_peak_c,gpu_temp_peak_c=excluded.gpu_temp_peak_c,temperature_peak_c=excluded.temperature_peak_c,throttled=excluded.throttled,
		network_rx_bytes=excluded.network_rx_bytes,network_tx_bytes=excluded.network_tx_bytes,metadata_json=excluded.metadata_json`,
		o.ObservationID, o.RunID, o.JobID, o.AttemptID, o.WorkerID, o.Host, o.ObservedAt,
		floatValue(o.CPUAvgPct), floatValue(o.CPUPeakPct), intValue(o.RSSAvgBytes), intValue(o.RSSPeakBytes),
		intValue(o.SwapInBytes), intValue(o.SwapOutBytes), intValue(o.DiskReadBytes), intValue(o.DiskWriteBytes),
		floatValue(o.DiskUtilPct), floatValue(o.IOWaitPct), floatValue(o.DiskQueueDepth),
		floatValue(o.GPUAvgPct), floatValue(o.GPUPeakPct), intValue(o.VRAMPeakBytes), floatValue(o.EncoderAvgPct), floatValue(o.DecoderAvgPct),
		floatValue(o.CPUTempPeakC), floatValue(o.GPUTempPeakC), floatValue(o.TemperaturePeakC), boolValue(o.Throttled),
		intValue(o.NetworkRXBytes), intValue(o.NetworkTXBytes), nonEmpty(o.MetadataJSON, "{}"))
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
	provider       SampleProvider
	store          capperformance.ResourceObservationStore
	interval       time.Duration
	persistTimeout time.Duration
}

// Interval reports the effective sampling cadence.
func (s *Sampler) Interval() time.Duration {
	if s == nil {
		return 0
	}
	return s.interval
}

// SamplerOptions controls the run sampling loop. Zero Interval selects the
// canonical 500ms cadence. Values below 1ms are rejected to prevent hot loops.
type SamplerOptions struct {
	Interval       time.Duration
	PersistTimeout time.Duration
}

func (o SamplerOptions) normalized() (SamplerOptions, error) {
	if o.Interval == 0 {
		o.Interval = DefaultSampleInterval
	}
	if o.Interval < time.Millisecond {
		return SamplerOptions{}, errors.New("resource sampler: interval must be at least 1ms")
	}
	if o.PersistTimeout == 0 {
		o.PersistTimeout = samplePersistTimeout
	}
	if o.PersistTimeout <= 0 {
		return SamplerOptions{}, errors.New("resource sampler: persist timeout must be positive")
	}
	return o, nil
}

func NewSampler(provider SampleProvider, store capperformance.ResourceObservationStore) (*Sampler, error) {
	return NewSamplerWithOptions(provider, store, SamplerOptions{})
}

func NewSamplerWithOptions(provider SampleProvider, store capperformance.ResourceObservationStore, options SamplerOptions) (*Sampler, error) {
	if provider == nil || store == nil {
		return nil, errors.New("resource sampler: provider and store are required")
	}
	normalized, err := options.normalized()
	if err != nil {
		return nil, err
	}
	return &Sampler{provider: provider, store: store, interval: normalized.Interval, persistTimeout: normalized.PersistTimeout}, nil
}

// DefaultSampleInterval is the canonical resource sampling cadence: one
// observation every 500ms for the lifetime of a run.
const DefaultSampleInterval = 500 * time.Millisecond

// samplePersistTimeout bounds a single collect+persist step so a hung store
// can never stall a run (instrumentation must never change behaviour).
const samplePersistTimeout = 3 * time.Second

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
	o = finalizeObservation(o, identity)
	if err := s.store.RecordResourceObservation(ctx, o); err != nil {
		return capperformance.ResourceObservation{}, err
	}
	return o, nil
}

// SampleLoop starts the run-scoped sampling loop: it samples every
// DefaultSampleInterval until ctx is done or the returned stop function is
// called, persisting each observation bound to the canonical identity
// (run_id/job_id/attempt_id/worker_id/host). Peak fields are rolled up
// across the run's samples so MAX() over the rows yields the true peak.
// Failures are logged and skipped; a run's outcome is never affected.
func (s *Sampler) SampleLoop(ctx context.Context, identity capperformance.SampleIdentity, log *zap.Logger) (stop func()) {
	if s == nil || s.provider == nil || s.store == nil {
		return func() {}
	}
	if identity.RunID == "" || identity.JobID == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		peaks := &peakTracker{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			sampleCtx, sampleCancel := context.WithTimeout(ctx, s.persistTimeout)
			o, err := s.provider.Collect(sampleCtx, identity)
			if err != nil {
				sampleCancel()
				if log != nil {
					log.Warn("resource sampling failed",
						zap.String("run_id", identity.RunID),
						zap.String("job_id", identity.JobID),
						zap.Error(err))
				}
				continue
			}
			o = finalizeObservation(o, identity)
			o = peaks.apply(o)
			if err := s.store.RecordResourceObservation(sampleCtx, o); err != nil {
				if log != nil {
					log.Warn("resource sample persist failed",
						zap.String("run_id", identity.RunID),
						zap.String("job_id", identity.JobID),
						zap.Error(err))
				}
			}
			sampleCancel()
		}
	}()
	return stop
}

// finalizeObservation stamps the canonical identity and observation IDs onto
// a raw provider sample. Shared by the one-shot Sample and the SampleLoop so
// both paths persist identically-shaped rows.
func finalizeObservation(o capperformance.ResourceObservation, identity capperformance.SampleIdentity) capperformance.ResourceObservation {
	if o.ObservationID == "" {
		o.ObservationID = fmt.Sprintf("resource_%d", time.Now().UnixNano())
	}
	o.RunID, o.JobID, o.AttemptID, o.WorkerID, o.Host = identity.RunID, identity.JobID, identity.AttemptID, identity.WorkerID, identity.Host
	if o.ObservedAt == "" {
		o.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return o
}

// peakTracker maintains the run-level rolling peaks applied to every sample
// before persistence. The provider reports instantaneous/interval values; the
// tracker promotes them to "peak so far" so a MAX() across the run's rows is
// the true run peak.
type peakTracker struct {
	cpuPeak     float64
	rssPeak     int64
	gpuPeak     float64
	vramPeak    int64
	cpuTempPeak float64
	gpuTempPeak float64
	tempPeak    float64
}

func (t *peakTracker) apply(o capperformance.ResourceObservation) capperformance.ResourceObservation {
	if o.CPUAvgPct != nil && *o.CPUAvgPct > t.cpuPeak {
		t.cpuPeak = *o.CPUAvgPct
	}
	o.CPUPeakPct = f64Ptr(t.cpuPeak, o.CPUPeakPct)

	rss := maxInt64(ptrInt64(o.RSSAvgBytes), ptrInt64(o.RSSPeakBytes))
	if rss > t.rssPeak {
		t.rssPeak = rss
	}
	if t.rssPeak > 0 {
		o.RSSPeakBytes = i64Ptr(t.rssPeak)
	}

	if o.GPUAvgPct != nil && *o.GPUAvgPct > t.gpuPeak {
		t.gpuPeak = *o.GPUAvgPct
	}
	o.GPUPeakPct = f64Ptr(t.gpuPeak, o.GPUPeakPct)

	if v := ptrInt64(o.VRAMPeakBytes); v > t.vramPeak {
		t.vramPeak = v
	}
	if t.vramPeak > 0 {
		o.VRAMPeakBytes = i64Ptr(t.vramPeak)
	}

	if v := ptrFloat64(o.CPUTempPeakC); v > t.cpuTempPeak {
		t.cpuTempPeak = v
	}
	o.CPUTempPeakC = f64Ptr(t.cpuTempPeak, o.CPUTempPeakC)

	if v := ptrFloat64(o.GPUTempPeakC); v > t.gpuTempPeak {
		t.gpuTempPeak = v
	}
	o.GPUTempPeakC = f64Ptr(t.gpuTempPeak, o.GPUTempPeakC)

	combined := maxFloat(ptrFloat64(o.TemperaturePeakC), maxFloat(ptrFloat64(o.CPUTempPeakC), ptrFloat64(o.GPUTempPeakC)))
	if combined > t.tempPeak {
		t.tempPeak = combined
	}
	o.TemperaturePeakC = f64Ptr(t.tempPeak, o.TemperaturePeakC)

	return o
}

func ptrFloat64(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
func ptrInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func f64Ptr(v float64, fallback *float64) *float64 {
	if v > 0 {
		return &v
	}
	return fallback
}
func i64Ptr(v int64) *int64 { return &v }

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
func boolValue(v *bool) any {
	if v == nil {
		return nil
	}
	return *v
}
