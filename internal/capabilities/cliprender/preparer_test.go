package cliprender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ── Fakes ────────────────────────────────────────────────────────────

type fakeAssetResolver struct {
	assets map[string]AssetRef
	err    error
	mu     sync.Mutex
	calls  []string
}

func newFakeAssetResolver(assets map[string]AssetRef) *fakeAssetResolver {
	return &fakeAssetResolver{assets: assets}
}

func (f *fakeAssetResolver) ResolveAsset(_ context.Context, id string) (*AssetRef, error) {
	f.mu.Lock()
	f.calls = append(f.calls, id)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	ref, ok := f.assets[id]
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", id)
	}
	return &ref, nil
}

type fakeMaterializer struct {
	err   error
	delay time.Duration
	mu    sync.Mutex
	calls []string
	// barrier proves concurrent invocation: when non-nil, each Materialize
	// signals started and blocks until release fires twice.
	started chan string
	release chan struct{}
}

func (f *fakeMaterializer) Materialize(_ context.Context, ref AssetRef) (*MaterializedAsset, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ref.AssetID)
	f.mu.Unlock()
	if f.started != nil {
		f.started <- ref.AssetID
		<-f.release
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	sum := sha256.Sum256([]byte(ref.AssetID))
	return &MaterializedAsset{
		AssetID:   ref.AssetID,
		LocalPath: "/scratch/" + ref.AssetID + ".mp4",
		SHA256:    hex.EncodeToString(sum[:]),
		SizeBytes: 1024,
		FromCache: ref.LocalPath != "",
	}, nil
}

type fakeTranscriptResolver struct {
	existing    *TranscriptResult
	existingOK  bool
	lookupErr   error
	genResult   *TranscriptResult
	genErr      error
	mu          sync.Mutex
	lookupCalls int
	genSources  []*MaterializedAsset
	genInputs   []TranscriptInput
}

func (f *fakeTranscriptResolver) Lookup(_ context.Context, in TranscriptInput) (*TranscriptResult, bool, error) {
	f.mu.Lock()
	f.lookupCalls++
	f.mu.Unlock()
	if f.lookupErr != nil {
		return nil, false, f.lookupErr
	}
	return f.existing, f.existingOK, nil
}

func (f *fakeTranscriptResolver) Generate(_ context.Context, in TranscriptInput, source *MaterializedAsset) (*TranscriptResult, error) {
	f.mu.Lock()
	f.genSources = append(f.genSources, source)
	f.genInputs = append(f.genInputs, in)
	f.mu.Unlock()
	if f.genErr != nil {
		return nil, f.genErr
	}
	return f.genResult, nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func baseRenderRequest() *RenderRequest {
	req := &RenderRequest{SourceAssetID: "asset-source"}
	req.Normalize()
	return req
}

func newTestPreparer(assets *fakeAssetResolver, mat *fakeMaterializer, tr *fakeTranscriptResolver) *Preparer {
	contract := NewContractResolver()
	p, err := NewPreparer(assets, mat, tr, contract, zap.NewNop())
	if err != nil {
		panic(err)
	}
	return p
}

// ── Tests ────────────────────────────────────────────────────────────

// TestPrepare_ConcurrentMaterialization proves the anti-serial-barrier
// contract deterministically: source and watermark materialization must run
// concurrently (both signal started before either is released), and the
// aggregate timing must report parallel=true with work > wall.
func TestPrepare_ConcurrentMaterialization(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
		"asset-wm":     {AssetID: "asset-wm"},
	})
	mat := &fakeMaterializer{started: make(chan string, 2), release: make(chan struct{}), delay: 50 * time.Millisecond}
	tr := &fakeTranscriptResolver{existing: &TranscriptResult{Text: "existing", Cues: []Cue{{StartMs: 0, EndMs: 100, Text: "hi"}}}, existingOK: true}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Watermark = &WatermarkSpec{Enabled: true, AssetID: "asset-wm"}

	type result struct {
		prepared *Prepared
		err      error
	}
	done := make(chan result, 1)
	go func() {
		prepared, err := p.Prepare(context.Background(), req, "run-1")
		done <- result{prepared, err}
	}()

	// Both source + watermark must signal started before we release — a
	// serial implementation would signal only source and deadlock here.
	started := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-mat.started:
			started[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("expected 2 concurrent materializations, got %d before timeout (serial barrier detected)", len(started))
		}
	}
	if !started["asset-source"] || !started["asset-wm"] {
		t.Fatalf("expected source + watermark materialization to start concurrently, got %v", started)
	}
	close(mat.release)

	res := <-done
	if res.err != nil {
		t.Fatalf("Prepare failed: %v", res.err)
	}
	prepared := res.prepared
	if prepared.Source == nil || prepared.Watermark == nil {
		t.Fatalf("expected source + watermark materialized, got source=%v watermark=%v", prepared.Source, prepared.Watermark)
	}
	if !prepared.Timings.Parallel {
		t.Fatalf("expected parallel=true (work=%d wall=%d)", prepared.Timings.TotalWorkMS, prepared.Timings.TotalWallMS)
	}
	if prepared.Timings.TotalWorkMS <= prepared.Timings.TotalWallMS {
		t.Fatalf("expected accumulated work > wall for overlapping phases (work=%d wall=%d)", prepared.Timings.TotalWorkMS, prepared.Timings.TotalWallMS)
	}
}

// TestPrepare_TranscriptReuse verifies the reuse fast path: when a READY
// track exists, Generate is never invoked and Reused=true is reported.
func TestPrepare_TranscriptReuse(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing:   &TranscriptResult{AssetID: "asset-source", Language: "en", Text: "existing transcript", Reused: true},
		existingOK: true,
	}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest() // reuse_or_generate default
	prepared, err := p.Prepare(context.Background(), req, "run-1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if prepared.Transcript == nil || !prepared.Transcript.Reused {
		t.Fatalf("expected reused transcript, got %+v", prepared.Transcript)
	}
	if len(tr.genSources) != 0 {
		t.Fatalf("expected zero Generate calls on reuse, got %d", len(tr.genSources))
	}
	if prepared.Transcript.Text != "existing transcript" {
		t.Fatalf("expected existing text, got %q", prepared.Transcript.Text)
	}
}

// TestPrepare_TranscriptGenerateOnMiss verifies generation: on a reuse miss
// the Generate port is invoked with the materialized source and the result
// is returned.
func TestPrepare_TranscriptGenerateOnMiss(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		genResult: &TranscriptResult{AssetID: "asset-source", Language: "en", Text: "generated", Cues: []Cue{{StartMs: 0, EndMs: 500, Text: "hi"}}},
	}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest() // reuse_or_generate, lookup misses
	prepared, err := p.Prepare(context.Background(), req, "run-1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if len(tr.genSources) != 1 {
		t.Fatalf("expected exactly 1 Generate call, got %d", len(tr.genSources))
	}
	sum := sha256.Sum256([]byte("asset-source"))
	if got := tr.genSources[0]; got == nil || got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("Generate must receive the materialized source with its sha256, got %+v", got)
	}
	if prepared.Transcript == nil || prepared.Transcript.Text != "generated" {
		t.Fatalf("expected generated transcript, got %+v", prepared.Transcript)
	}
}

// TestPrepare_ReuseRequiredButMissing fails closed: mode=reuse with no READY
// track must return ErrTranscriptUnavailable and never call Generate.
func TestPrepare_ReuseRequiredButMissing(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{} // no existing track
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Transcript.Mode = TranscriptModeReuse
	_, err := p.Prepare(context.Background(), req, "run-1")
	if !errors.Is(err, ErrTranscriptUnavailable) {
		t.Fatalf("expected ErrTranscriptUnavailable, got %v", err)
	}
	if len(tr.genSources) != 0 {
		t.Fatalf("mode=reuse must never generate, got %d Generate calls", len(tr.genSources))
	}
}

// TestPrepare_ModeGenerateSkipsLookup verifies mode=generate never performs
// the DB lookup and always generates.
func TestPrepare_ModeGenerateSkipsLookup(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing:   &TranscriptResult{AssetID: "asset-source", Language: "en", Text: "existing"},
		existingOK: true,
		genResult:  &TranscriptResult{AssetID: "asset-source", Language: "en", Text: "fresh"},
	}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Transcript.Mode = TranscriptModeGenerate
	prepared, err := p.Prepare(context.Background(), req, "run-1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if tr.lookupCalls != 0 {
		t.Fatalf("mode=generate must skip the DB lookup, got %d lookups", tr.lookupCalls)
	}
	if prepared.Transcript.Text != "fresh" {
		t.Fatalf("expected freshly generated transcript, got %q", prepared.Transcript.Text)
	}
}

// TestPrepare_ResolveFailureFailsClosed verifies a single phase failure in
// wave 1 aborts the whole preparation with a typed error.
func TestPrepare_ResolveFailureFailsClosed(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	resolver.err = errors.New("registry unavailable")
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{existing: &TranscriptResult{Text: "x"}, existingOK: true}
	p := newTestPreparer(resolver, mat, tr)

	_, err := p.Prepare(context.Background(), baseRenderRequest(), "run-1")
	if err == nil {
		t.Fatal("expected error when asset resolution fails")
	}
}

// TestPrepare_MaterializeFailureFailsClosed verifies a wave-2 materialization
// failure aborts preparation.
func TestPrepare_MaterializeFailureFailsClosed(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{err: errors.New("download failed")}
	tr := &fakeTranscriptResolver{existing: &TranscriptResult{Text: "x"}, existingOK: true}
	p := newTestPreparer(resolver, mat, tr)

	_, err := p.Prepare(context.Background(), baseRenderRequest(), "run-1")
	if err == nil {
		t.Fatal("expected error when source materialization fails")
	}
}

// TestPrepare_ContractResolved verifies the default contract resolution.
func TestPrepare_ContractResolved(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{"asset-source": {AssetID: "asset-source"}})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{existing: &TranscriptResult{Text: "x"}, existingOK: true}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Output.Width = 1080
	req.Output.Height = 1920
	req.Output.FPSNum = 60
	req.Output.FPSDen = 1

	prepared, err := p.Prepare(context.Background(), req, "run-1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	c := prepared.Contract
	if c == nil {
		t.Fatal("expected resolved contract")
	}
	if c.ContractID != OutputContractVeloxEditingClipV1 || c.Width != 1080 || c.Height != 1920 || c.FPSNum != 60 || c.FPSDen != 1 {
		t.Fatalf("unexpected contract: %+v", c)
	}
	if c.VideoCodec != "h264" || c.PixelFormat != "yuv420p" || c.AudioCodec != "aac" {
		t.Fatalf("unexpected codec settings: %+v", c)
	}
}

// TestPrepare_BackgroundAsset verifies background.mode=asset resolves and
// materializes the background concurrently and surfaces it in Prepared.
func TestPrepare_BackgroundAsset(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
		"asset-bg":     {AssetID: "asset-bg"},
	})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{existing: &TranscriptResult{Text: "x"}, existingOK: true}
	p := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Background = &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "asset-bg"}

	prepared, err := p.Prepare(context.Background(), req, "run-1")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if prepared.Background == nil || prepared.Background.AssetID != "asset-bg" {
		t.Fatalf("expected background materialized, got %+v", prepared.Background)
	}
	if prepared.Watermark != nil {
		t.Fatalf("expected no watermark (disabled), got %+v", prepared.Watermark)
	}
}
