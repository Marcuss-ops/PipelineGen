package performance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

type loopProvider struct {
	mu       sync.Mutex
	calls    int
	failNext bool
}

func (p *loopProvider) Collect(context.Context, capperformance.SampleIdentity) (capperformance.ResourceObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.failNext {
		p.failNext = false
		return capperformance.ResourceObservation{}, errors.New("hardware unavailable")
	}
	cpu := float64(p.calls)
	return capperformance.ResourceObservation{CPUAvgPct: &cpu}, nil
}

func (p *loopProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type loopStore struct {
	mu      sync.Mutex
	rows    []capperformance.ResourceObservation
	fail    bool
	started chan struct{}
}

func (s *loopStore) RecordResourceObservation(_ context.Context, o capperformance.ResourceObservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("store unavailable")
	}
	s.rows = append(s.rows, o)
	if s.started != nil {
		select {
		case <-s.started:
		default:
			close(s.started)
		}
	}
	return nil
}

func (s *loopStore) Rows() []capperformance.ResourceObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capperformance.ResourceObservation(nil), s.rows...)
}

func TestNewSamplerWithOptionsDefaultsTo500ms(t *testing.T) {
	p := &loopProvider{}
	s, err := NewSamplerWithOptions(p, &loopStore{}, SamplerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Interval() != DefaultSampleInterval || s.Interval() != 500*time.Millisecond {
		t.Fatalf("interval=%s, want 500ms", s.Interval())
	}
}

func TestNewSamplerWithOptionsRejectsInvalidInterval(t *testing.T) {
	_, err := NewSamplerWithOptions(&loopProvider{}, &loopStore{}, SamplerOptions{Interval: 500 * time.Microsecond})
	if err == nil {
		t.Fatal("sub-millisecond interval must be rejected")
	}
}

func TestSamplerLoopUsesConfiguredIntervalAndStops(t *testing.T) {
	p := &loopProvider{}
	store := &loopStore{started: make(chan struct{})}
	s, err := NewSamplerWithOptions(p, store, SamplerOptions{Interval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	stop := s.SampleLoop(context.Background(), capperformance.SampleIdentity{
		RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1", WorkerID: "worker-1", Host: "host-1",
	}, nil)
	select {
	case <-store.started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("sampler did not persist a sample")
	}
	stop()
	calls := p.Calls()
	if calls < 1 || calls > 10 {
		t.Fatalf("provider calls=%d, expected bounded configured loop", calls)
	}
	for _, row := range store.Rows() {
		if row.RunID != "run-1" || row.JobID != "job-1" || row.AttemptID != "attempt-1" || row.ObservationID == "" || row.ObservedAt == "" {
			t.Fatalf("row identity=%+v", row)
		}
	}
	stop() // idempotent
}

func TestSamplerLoopContinuesAfterProviderAndStoreFailures(t *testing.T) {
	p := &loopProvider{failNext: true}
	store := &loopStore{started: make(chan struct{})}
	s, err := NewSamplerWithOptions(p, store, SamplerOptions{Interval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	stop := s.SampleLoop(context.Background(), capperformance.SampleIdentity{RunID: "run-2", JobID: "job-2"}, nil)
	select {
	case <-store.started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("sampler did not recover after provider failure")
	}
	stop()
	if len(store.Rows()) == 0 || p.Calls() < 2 {
		t.Fatalf("provider/store recovery failed: calls=%d rows=%d", p.Calls(), len(store.Rows()))
	}
}

func TestSamplerLoopDoesNotInventUnsupportedHardwareFacts(t *testing.T) {
	p := &loopProvider{}
	store := &loopStore{started: make(chan struct{})}
	store.fail = false
	s, err := NewSamplerWithOptions(p, store, SamplerOptions{Interval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	stop := s.SampleLoop(context.Background(), capperformance.SampleIdentity{RunID: "run-3", JobID: "job-3"}, nil)
	select {
	case <-store.started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("sampler did not persist a sample")
	}
	stop()
	for _, row := range store.Rows() {
		if row.GPUAvgPct != nil || row.GPUPeakPct != nil || row.VRAMPeakBytes != nil || row.CPUTempPeakC != nil || row.GPUTempPeakC != nil {
			t.Fatalf("unsupported hardware fact was invented: %+v", row)
		}
	}
}
