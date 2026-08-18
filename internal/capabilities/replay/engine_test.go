package replay_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/replay"
)

// ── fakes ────────────────────────────────────────────────────────────

type fakeBundles struct {
	bundles map[string]replay.ReplayBundle
}

func (f *fakeBundles) Save(_ context.Context, b replay.ReplayBundle) error {
	if f.bundles == nil {
		f.bundles = map[string]replay.ReplayBundle{}
	}
	f.bundles[b.OriginalJobID] = b
	return nil
}

func (f *fakeBundles) Get(_ context.Context, id string) (*replay.ReplayBundle, error) {
	b, ok := f.bundles[id]
	if !ok {
		return nil, nil
	}
	return &b, nil
}

type fakeAssets struct {
	err error
}

func (f *fakeAssets) Materialize(_ context.Context, a replay.ReplayAsset) (replay.MaterializedAsset, error) {
	if f.err != nil {
		return replay.MaterializedAsset{}, f.err
	}
	return replay.MaterializedAsset{AssetID: a.AssetID, SHA256: a.SHA256, LocalPath: "/staging/" + a.AssetID, SizeBytes: 1}, nil
}

type fakeStrategy struct {
	report render.CompatibilityReport
	err    error
}

func (f *fakeStrategy) Resolve(_ context.Context, _ render.RenderPlan) (render.CompatibilityReport, error) {
	return f.report, f.err
}

func newEngine(bundles *fakeBundles, assets *fakeAssets, strategy *fakeStrategy) *replay.Engine {
	var s render.StrategyResolver
	if strategy != nil {
		s = strategy
	}
	e := replay.NewEngine(bundles, assets, s)
	e.SetIDGenerator(func(original string) string { return original + "-replay-001" })
	return e
}

func matchingEnv() replay.Environment {
	return replay.Environment{RendererVersion: "rust-render/v3", RustProtocolVersion: "1.4", FFmpegVersion: "6.1", EncoderPolicyHash: hash64('e')}
}

// ── ParseMode ────────────────────────────────────────────────────────

func TestParseMode(t *testing.T) {
	cases := map[string]replay.Mode{
		"":        replay.ModeExact,
		"exact":   replay.ModeExact,
		"EXACT":   replay.ModeExact,
		"current": replay.ModeCurrent,
		"Current": replay.ModeCurrent,
	}
	for raw, want := range cases {
		got, err := replay.ParseMode(raw)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := replay.ParseMode("bogus"); !errors.Is(err, replay.ErrInvalidMode) {
		t.Fatalf("bogus mode: want ErrInvalidMode, got %v", err)
	}
}

// ── Engine.Prepare ───────────────────────────────────────────────────

func TestPrepareExactMatch(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{}, &fakeStrategy{report: render.CompatibilityReport{Mode: render.ExecutionCopy}})

	prepared, err := e.Prepare(context.Background(), "job-1", replay.ModeExact, matchingEnv())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ReplayJobID != "job-1-replay-001" || prepared.ReplayJobID == prepared.OriginalJobID {
		t.Fatalf("replay id must be distinct: %+v", prepared)
	}
	if prepared.PlanSHA256 == "" || prepared.Mode != replay.ModeExact {
		t.Fatalf("prepared replay missing identity: %+v", prepared)
	}
	if len(prepared.Materialized) != 2 {
		t.Fatalf("expected 2 materialized assets, got %d", len(prepared.Materialized))
	}
	if prepared.Strategy.Mode != render.ExecutionCopy {
		t.Fatalf("strategy must be resolved, got %+v", prepared.Strategy)
	}
}

func TestPrepareExactVersionMismatchFails(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{}, &fakeStrategy{})
	mismatch := matchingEnv()
	mismatch.RendererVersion = "rust-render/v9"
	_, err := e.Prepare(context.Background(), "job-1", replay.ModeExact, mismatch)
	if !errors.Is(err, replay.ErrExactVersionMismatch) {
		t.Fatalf("exact mismatch: want ErrExactVersionMismatch, got %v", err)
	}
}

func TestPrepareCurrentIgnoresVersionMismatch(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{}, &fakeStrategy{})
	mismatch := matchingEnv()
	mismatch.FFmpegVersion = "7.0"
	prepared, err := e.Prepare(context.Background(), "job-1", replay.ModeCurrent, mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Mode != replay.ModeCurrent {
		t.Fatalf("expected current mode, got %+v", prepared)
	}
}

func TestPrepareBundleNotFound(t *testing.T) {
	e := newEngine(&fakeBundles{}, &fakeAssets{}, &fakeStrategy{})
	_, err := e.Prepare(context.Background(), "job-missing", replay.ModeExact, matchingEnv())
	if !errors.Is(err, replay.ErrBundleNotFound) {
		t.Fatalf("missing bundle: want ErrBundleNotFound, got %v", err)
	}
}

func TestPrepareMaterializeFailureFails(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{err: errors.New("cas down")}, &fakeStrategy{})
	if _, err := e.Prepare(context.Background(), "job-1", replay.ModeCurrent, matchingEnv()); err == nil {
		t.Fatal("materialization failure must fail the replay")
	}
}

func TestPrepareStrategyErrorPropagates(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{}, &fakeStrategy{err: errors.New("strategy boom")})
	if _, err := e.Prepare(context.Background(), "job-1", replay.ModeCurrent, matchingEnv()); err == nil {
		t.Fatal("strategy error must fail the replay")
	}
}

func TestPrepareDegradesWithoutStrategyResolver(t *testing.T) {
	bundles := &fakeBundles{}
	if err := bundles.Save(context.Background(), testBundle(t)); err != nil {
		t.Fatal(err)
	}
	e := newEngine(bundles, &fakeAssets{}, nil)
	prepared, err := e.Prepare(context.Background(), "job-1", replay.ModeCurrent, matchingEnv())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Strategy.Mode != render.ExecutionRender {
		t.Fatalf("nil resolver must degrade to FULL_RENDER, got %+v", prepared.Strategy)
	}
}

func TestPrepareRejectsEmptyOriginalID(t *testing.T) {
	e := newEngine(&fakeBundles{}, &fakeAssets{}, &fakeStrategy{})
	if _, err := e.Prepare(context.Background(), "  ", replay.ModeExact, matchingEnv()); err == nil {
		t.Fatal("empty original id must be rejected")
	}
}

func TestPrepareRejectsInvalidMode(t *testing.T) {
	e := newEngine(&fakeBundles{}, &fakeAssets{}, &fakeStrategy{})
	if _, err := e.Prepare(context.Background(), "job-1", replay.Mode("nope"), matchingEnv()); !errors.Is(err, replay.ErrInvalidMode) {
		t.Fatalf("invalid mode: want ErrInvalidMode, got %v", err)
	}
}

var _ replay.BundleStore = (*fakeBundles)(nil)
var _ replay.AssetSource = (*fakeAssets)(nil)
var _ render.StrategyResolver = (*fakeStrategy)(nil)
