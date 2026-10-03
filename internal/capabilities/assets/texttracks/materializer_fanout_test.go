package texttracks

// A2 (October 2026) acceptance tests for the per-language translation
// fan-out inside one asset.text.materialize job. The parallelism proofs use
// a BARRIER in the fake translator, not wall-clock assertions: with N
// candidates and fan-out >= N every Translate call must be IN FLIGHT at the
// same time or the barrier never releases — a sequential fan-out provably
// fails the test instead of racing a timing budget.

import (
	"context"
	"sync"
	"testing"
	"time"

	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// fanoutProbe tracks concurrent in-flight Translate calls and can hold every
// caller on a barrier until `barrierOn` are simultaneously inside.
type fanoutProbe struct {
	mu        sync.Mutex
	inFlight  int
	peak      int
	barrierOn int           // 0 = no barrier
	arrived   chan struct{} // signalled per arrival
	released  chan struct{} // closed once barrierOn calls are inside
	dropOnce  sync.Once
	delays    map[string]time.Duration // per-target-lang delay, applied under mu
}

func newFanoutProbe() *fanoutProbe {
	return &fanoutProbe{arrived: make(chan struct{}, 64), released: make(chan struct{})}
}

// hook returns the fakeTranslator hook implementing the probe.
func (p *fanoutProbe) hook(cmd translation.TranslationCommand) error {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
	delay := p.delays[cmd.TargetLang]
	p.mu.Unlock()
	p.arrived <- struct{}{}

	if p.barrierOn > 0 {
		// Release as soon as barrierOn calls are in flight; the timeout
		// fail-safe keeps a WRONG (sequential) implementation from hanging
		// the test — the peak assertion then fails instead.
		p.dropOnce.Do(func() {
			go func() {
				count := 0
				for count < p.barrierOn {
					select {
					case <-p.arrived:
						count++
					case <-time.After(5 * time.Second):
						close(p.released)
						return
					}
				}
				close(p.released)
			}()
		})
		select {
		case <-p.released:
		case <-time.After(6 * time.Second):
		}
	}

	if delay > 0 {
		time.Sleep(delay)
	}
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	return nil
}

func (p *fanoutProbe) peakInFlight() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

func fanoutTestMaterializer(t *testing.T, tr *fakeTranslator, targets []string, concurrency int) *Materializer {
	t.Helper()
	repo := newFakeRepo()
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", "hello world")
	ob := &fakeOutbox{}
	m := newTestMaterializer(t, repo, tr, ob, "en", targets, "model-v1", "prompt-v1")
	m.SetConcurrency(concurrency)
	return m
}

// TestMaterialize_ParallelFanoutRunsAllLanguagesConcurrently is the A2 core
// proof: with 6 candidates and fan-out 6, all six Translate calls are in
// flight SIMULTANEOUSLY (barrier releases only at 6 concurrent callers) and
// all six languages are created.
func TestMaterialize_ParallelFanoutRunsAllLanguagesConcurrently(t *testing.T) {
	const n = 6
	probe := newFanoutProbe()
	probe.barrierOn = n
	tr := &fakeTranslator{hook: probe.hook}
	targets := []string{"en", "it", "es", "de", "fr", "pt-BR", "ru"} // 6 candidates after source exclusion

	m := fanoutTestMaterializer(t, tr, targets, n)

	rep, err := m.Materialize(context.Background(), "asset-1", "en", ComputeSourceTextHash("hello world"), detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := len(rep.CreatedLanguages); got != n {
		t.Fatalf("created %d languages, want %d (report: %+v)", got, n, rep)
	}
	if peak := probe.peakInFlight(); peak != n {
		t.Fatalf("peak in-flight Translate calls = %d, want %d — the fan-out did not run the languages in parallel", peak, n)
	}
}

// TestMaterialize_FanoutLimitCapsInFlight pins the bound: with fan-out 3 and
// 8 candidates, in-flight calls never exceed 3 (the errgroup SetLimit
// contract) while every language still completes.
func TestMaterialize_FanoutLimitCapsInFlight(t *testing.T) {
	const candidates = 8
	const limit = 3
	probe := newFanoutProbe()
	tr := &fakeTranslator{hook: probe.hook}
	targets := []string{"en", "it", "es", "de", "fr", "pt-BR", "ru", "pl", "tr"} // 8 candidates

	m := fanoutTestMaterializer(t, tr, targets, limit)

	rep, err := m.Materialize(context.Background(), "asset-1", "en", ComputeSourceTextHash("hello world"), detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := len(rep.CreatedLanguages); got != candidates {
		t.Fatalf("created %d languages, want %d", got, candidates)
	}
	if peak := probe.peakInFlight(); peak > limit {
		t.Fatalf("peak in-flight = %d, must never exceed the fan-out limit %d", peak, limit)
	}
}

// TestMaterialize_ReportOrderDeterministicUnderParallelFanout pins the A2
// determinism contract: completion order varies with translator latency
// (here deliberately REVERSE of candidate order), but the report lists must
// come back in the canonical candidate order so the same input always
// produces the same report bytes.
func TestMaterialize_ReportOrderDeterministicUnderParallelFanout(t *testing.T) {
	// Candidates c1..c6; c1 is the SLOWEST so completion order is reversed.
	candidates := []string{"it", "es", "de", "fr", "pt-BR", "ru"}
	probe := newFanoutProbe()
	probe.delays = map[string]time.Duration{
		"it": 90 * time.Millisecond, "es": 75 * time.Millisecond, "de": 60 * time.Millisecond,
		"fr": 45 * time.Millisecond, "pt-BR": 30 * time.Millisecond, "ru": 15 * time.Millisecond,
	}
	tr := &fakeTranslator{hook: probe.hook}
	targets := append([]string{"en"}, candidates...)

	m := fanoutTestMaterializer(t, tr, targets, len(candidates))

	rep, err := m.Materialize(context.Background(), "asset-1", "en", ComputeSourceTextHash("hello world"), detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rep.CreatedLanguages) != len(candidates) {
		t.Fatalf("created %v, want all %d candidates", rep.CreatedLanguages, len(candidates))
	}
	for i, want := range candidates {
		if rep.CreatedLanguages[i] != want {
			t.Fatalf("CreatedLanguages = %v, want canonical candidate order %v (report order drifted with completion order)", rep.CreatedLanguages, candidates)
		}
	}
}

// TestMaterialize_RollbackConcurrencyOneIsSequential pins the documented
// rollback: fan-out 1 must behave exactly like the historical sequential
// path — peak in-flight 1, every language still created.
func TestMaterialize_RollbackConcurrencyOneIsSequential(t *testing.T) {
	probe := newFanoutProbe()
	tr := &fakeTranslator{hook: probe.hook}
	targets := []string{"en", "it", "es", "de", "fr"}

	m := fanoutTestMaterializer(t, tr, targets, 1)

	rep, err := m.Materialize(context.Background(), "asset-1", "en", ComputeSourceTextHash("hello world"), detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := len(rep.CreatedLanguages); got != 4 {
		t.Fatalf("created %d languages, want 4 (rollback must still complete the fan-out)", got)
	}
	if peak := probe.peakInFlight(); peak != 1 {
		t.Fatalf("peak in-flight = %d, want 1 (rollback = strictly sequential)", peak)
	}
}
