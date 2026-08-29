package jobs

import (
	"context"
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestPreparationWorkEstimator_KindAverage(t *testing.T) {
	e := NewPreparationWorkEstimator(0.5)
	if _, ok := e.Expect("tts.synthesize"); ok {
		t.Fatal("expect before any observation must be !ok")
	}
	e.Observe(job.WorkObservation{Kind: "tts.synthesize", WallMS: 1000})
	e.Observe(job.WorkObservation{Kind: "tts.synthesize", WallMS: 3000})

	est, ok := e.Expect("tts.synthesize")
	if !ok {
		t.Fatal("expected an estimate after two observations")
	}
	// EMA(alpha=0.5): seed 1000, then 0.5*3000 + 0.5*1000 = 2000.
	if est.ExpectedWorkMS != 2000 {
		t.Fatalf("ExpectedWorkMS = %d, want 2000 (EMA of 1000,3000 at alpha .5)", est.ExpectedWorkMS)
	}
	if est.Source != job.WorkloadNone {
		t.Fatalf("Source = %q, want none (kind average)", est.Source)
	}
}

func TestPreparationWorkEstimator_ScaledByWorkloadDriver(t *testing.T) {
	e := NewPreparationWorkEstimator(1.0)
	// TTS: 1000ms for a 100-char input → rate 10ms/char.
	e.Observe(job.WorkObservation{Kind: "tts.synthesize", WallMS: 1000, Dimension: job.WorkloadChars, Amount: 100})
	// A 250-char unit should therefore estimate ~2500ms.
	u := job.PreparationUnit{Kind: "tts.synthesize", Inputs: job.InputManifest{"char_count": 250}}
	est, ok := e.ExpectUnit(u)
	if !ok {
		t.Fatal("expected a scaled estimate")
	}
	if est.ExpectedWorkMS != 2500 {
		t.Fatalf("scaled ExpectedWorkMS = %d, want 2500 (10ms/char * 250)", est.ExpectedWorkMS)
	}
	if est.Source != job.WorkloadChars {
		t.Fatalf("Source = %q, want chars", est.Source)
	}
}

// TestPreparationWorkEstimator_NoWorkloadFallsBackToKind checks that a unit
// without a discoverable workload amount falls back to the kind average EMA.
func TestPreparationWorkEstimator_NoWorkloadFallsBackToKind(t *testing.T) {
	e := NewPreparationWorkEstimator(1.0)
	e.Observe(job.WorkObservation{Kind: "research.llm", WallMS: 2000, Dimension: job.WorkloadTokens, Amount: 100})
	e.Observe(job.WorkObservation{Kind: "research.llm", WallMS: 5000})

	u := job.PreparationUnit{Kind: "research.llm", Inputs: job.InputManifest{}} // no tokens in manifest
	est, ok := e.ExpectUnit(u)
	if !ok {
		t.Fatal("expected kind-avg fallback")
	}
	if est.Source != job.WorkloadNone {
		t.Fatalf("Source = %q, want none (fallback)", est.Source)
	}
}

func TestPreparationWorkEstimator_Transform(t *testing.T) {
	e := NewPreparationWorkEstimator(0.6)
	e.Observe(job.WorkObservation{Kind: "clip.process", WallMS: 5000, Dimension: job.WorkloadFrames, Amount: 100})
	e.Observe(job.WorkObservation{Kind: "clip.process", WallMS: 9000, Dimension: job.WorkloadFrames, Amount: 100})

	est, ok := e.Expect("clip.process")
	if !ok {
		t.Fatal("expected estimate")
	}
	// alpha=.6 rate EMA: seed 50, then .6*90 + .4*50 = 74 → 74ms/frame.
	u := job.PreparationUnit{Kind: "clip.process", Inputs: job.InputManifest{"frames": 200}}
	scaled, ok := e.ExpectUnit(u)
	if !ok {
		t.Fatal("expected scaled estimate")
	}
	if scaled.ExpectedWorkMS != 14800 { // 74 * 200
		t.Fatalf("ExpectedWorkMS = %d, want 14800 (74ms/frame * 200)", scaled.ExpectedWorkMS)
	}
	_ = est
}

type fakeObsReader struct {
	obs []job.WorkObservation
	err error
}

func (f *fakeObsReader) ListPreparationWorkObservations(_ context.Context, _ int) ([]job.WorkObservation, error) {
	return f.obs, f.err
}

func TestPreparationWorkEstimator_Bootstrap(t *testing.T) {
	e := NewPreparationWorkEstimator(0.5)
	err := e.Bootstrap(context.Background(), &fakeObsReader{obs: []job.WorkObservation{
		{Kind: "asset.download", WallMS: 4000},
		{Kind: "asset.download", WallMS: 6000},
	}}, 10)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	est, ok := e.Expect("asset.download")
	if !ok {
		t.Fatal("bootstrap should have produced an estimate")
	}
	if est.ExpectedWorkMS != 5000 {
		t.Fatalf("ExpectedWorkMS = %d, want 5000 (kind EMA of 4000,6000)", est.ExpectedWorkMS)
	}
}

func TestPreparationWorkEstimator_BootstrapSources(t *testing.T) {
	// Two independent feeds (preparation_attempts + performance history)
	// fold into ONE per-kind EMA: seed 1000, then 0.5*3000 + 0.5*1000 = 2000.
	e := NewPreparationWorkEstimator(0.5)
	err := e.BootstrapSources(context.Background(), 10,
		&fakeObsReader{obs: []job.WorkObservation{{Kind: "chronon.render", WallMS: 1000}}},
		nil, // must be skipped
		&fakeObsReader{obs: []job.WorkObservation{{Kind: "chronon.render", WallMS: 3000}}},
	)
	if err != nil {
		t.Fatalf("BootstrapSources: %v", err)
	}
	est, ok := e.Expect("chronon.render")
	if !ok {
		t.Fatal("multi-source bootstrap should have produced an estimate")
	}
	if est.ExpectedWorkMS != 2000 {
		t.Fatalf("ExpectedWorkMS = %d, want 2000 (EMA of 1000,3000 across both sources)", est.ExpectedWorkMS)
	}
}

func TestPreparationWorkEstimator_BootstrapSourcesFailOpen(t *testing.T) {
	// A failing source returns its error but the good source still folds:
	// the estimator keeps whatever it learned (speculation never blocked).
	e := NewPreparationWorkEstimator(0.5)
	err := e.BootstrapSources(context.Background(), 10,
		&fakeObsReader{err: errors.New("history read failed")},
		&fakeObsReader{obs: []job.WorkObservation{{Kind: "probe", WallMS: 2500}}},
	)
	if err == nil {
		t.Fatal("source error must be returned")
	}
	est, ok := e.Expect("probe")
	if !ok || est.ExpectedWorkMS != 2500 {
		t.Fatalf("good source not folded: est=%+v ok=%v", est, ok)
	}
}

func TestPreparationWorkEstimator_Driver(t *testing.T) {
	cases := []struct {
		u       job.PreparationUnit
		wantDim job.WorkloadDimension
		wantAmt float64
	}{
		{job.PreparationUnit{Kind: "tts.synthesize", Inputs: job.InputManifest{"char_count": 120}}, job.WorkloadChars, 120},
		{job.PreparationUnit{Kind: "clip.process", Inputs: job.InputManifest{"frames": 90}}, job.WorkloadFrames, 90},
		{job.PreparationUnit{Kind: "script.generate", Inputs: job.InputManifest{"token_count": 2048}}, job.WorkloadTokens, 2048},
		{job.PreparationUnit{Kind: "tts.synthesize", Inputs: job.InputManifest{}}, job.WorkloadNone, 0},
	}
	for _, c := range cases {
		d := c.u.Driver()
		if d.Dimension != c.wantDim || d.Amount != c.wantAmt {
			t.Fatalf("Driver(%s) = %+v, want dim=%s amt=%v", c.u.Kind, d, c.wantDim, c.wantAmt)
		}
	}
}
