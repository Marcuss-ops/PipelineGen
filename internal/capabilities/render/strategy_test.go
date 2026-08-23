package render

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func validExecutionPolicy() *RenderExecutionPolicy {
	return &RenderExecutionPolicy{
		AllowStreamCopy:   true,
		TargetProfileHash: hash64('1'),
		RendererVersion:   "rust-render/v3",
		EncoderPolicyHash: hash64('2'),
	}
}

func compilePlanWithPolicy(t *testing.T, policy *RenderExecutionPolicy) RenderPlan {
	t.Helper()
	plan, err := Compile(CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
		ExecutionPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func compatibleFacts() AssetFacts {
	return AssetFacts{VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, FPSNum: 30, FPSDen: 1, KeyframeInterval: 48}
}

func testTargetProfile() TargetProfile {
	return TargetProfile{Codec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, KeyframeInterval: 48}
}

// fakeProber returns per-path facts; missing paths probe as zero facts.
type fakeProber struct {
	facts map[string]AssetFacts
	errs  map[string]error
}

func (f *fakeProber) Probe(_ context.Context, path string) (AssetFacts, error) {
	if f.errs != nil {
		if err, ok := f.errs[path]; ok {
			return AssetFacts{}, err
		}
	}
	return f.facts[path], nil
}

// fakeCache implements the artifact-cache contract with a digest hit set.
type fakeCache struct {
	hits map[string]bool
	err  error
}

func (f *fakeCache) Lookup(_ context.Context, key capcache.Key, _ int64) (*capcache.Entry, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	digest, err := key.Digest()
	if err != nil {
		return nil, false, err
	}
	if f.hits[digest] {
		return &capcache.Entry{CacheKey: digest, ArtifactSHA256: hash64('a'), Status: "READY"}, true, nil
	}
	return nil, false, nil
}

func (f *fakeCache) Store(context.Context, capcache.Key, io.Reader, string, int64) (*capcache.Entry, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCache) Open(context.Context, *capcache.Entry) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeCache) Invalidate(context.Context, capcache.Key) error {
	return errors.New("not implemented")
}

func (f *fakeCache) Metrics(context.Context, string) (capcache.Metrics, error) {
	return capcache.Metrics{}, errors.New("not implemented")
}

// ── ExecutionPolicy / PlanSHA256 ───────────────────────────────────────

func TestExecutionPolicyContributesToPlanSHA256(t *testing.T) {
	withPolicy := compilePlanWithPolicy(t, validExecutionPolicy())
	withOtherEncoder := compilePlanWithPolicy(t, &RenderExecutionPolicy{
		AllowStreamCopy: true, TargetProfileHash: hash64('1'), RendererVersion: "rust-render/v3", EncoderPolicyHash: hash64('9'),
	})
	withoutPolicy := compilePlanWithPolicy(t, nil)
	if withPolicy.PlanSHA256 == withoutPolicy.PlanSHA256 {
		t.Fatal("setting an execution policy must change PlanSHA256")
	}
	if withPolicy.PlanSHA256 == withOtherEncoder.PlanSHA256 {
		t.Fatal("changing the encoder policy hash must change PlanSHA256")
	}
	if withPolicy.ExecutionPolicy == nil {
		t.Fatal("plan must retain the compiled policy")
	}
	if withoutPolicy.ExecutionPolicy != nil {
		t.Fatal("plan without a policy must stay nil")
	}
}

func TestExecutionPolicyWireFormatKeepsLegacyPlansStable(t *testing.T) {
	withPolicy, err := json.Marshal(compilePlanWithPolicy(t, validExecutionPolicy()))
	if err != nil {
		t.Fatal(err)
	}
	withoutPolicy, err := json.Marshal(compilePlanWithPolicy(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !jsonHasKey(withPolicy, "execution_policy") {
		t.Fatal("sealed policy must appear on the wire")
	}
	if jsonHasKey(withoutPolicy, "execution_policy") {
		t.Fatal("nil policy must be omitted so legacy plans keep their exact PlanSHA256")
	}
}

func TestExecutionPolicyTamperingIsRejected(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	plan.ExecutionPolicy.EncoderPolicyHash = hash64('9')
	if err := plan.Validate(); err == nil {
		t.Fatal("tampered execution policy must be rejected by plan validation")
	}
}

func TestCompileRejectsIncompleteExecutionPolicy(t *testing.T) {
	cases := map[string]*RenderExecutionPolicy{
		"missing target profile hash": {AllowStreamCopy: true, RendererVersion: "rust-render/v3", EncoderPolicyHash: hash64('2')},
		"bad target profile hash":     {AllowStreamCopy: true, TargetProfileHash: "not-a-hash", RendererVersion: "rust-render/v3", EncoderPolicyHash: hash64('2')},
		"missing encoder policy hash": {AllowStreamCopy: true, TargetProfileHash: hash64('1'), RendererVersion: "rust-render/v3"},
		"missing renderer version":    {AllowStreamCopy: true, TargetProfileHash: hash64('1'), EncoderPolicyHash: hash64('2')},
	}
	for name, policy := range cases {
		if _, err := Compile(CompileInput{
			JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
			Timeline: testRenderTimeline(), ExecutionPolicy: policy,
			Manifest: []AssetManifestEntry{{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000}, {AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000}},
		}); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

// ── FinalRenderCacheKey ────────────────────────────────────────────────

func TestFinalRenderCacheKeyIsStableAndPolicyBound(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	key, err := FinalRenderCacheKey(plan)
	if err != nil {
		t.Fatal(err)
	}
	if key.SourceSHA256 != plan.PlanSHA256 || key.Operation != CacheOperationRenderFinal || key.ProcessorVersion != "rust-render/v3" {
		t.Fatalf("unexpected cache key: %+v", key)
	}
	first, err := key.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := FinalRenderCacheKey(plan)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first != secondDigest {
		t.Fatal("cache key digest must be deterministic")
	}
	other := compilePlanWithPolicy(t, &RenderExecutionPolicy{AllowStreamCopy: true, TargetProfileHash: hash64('1'), RendererVersion: "rust-render/v3", EncoderPolicyHash: hash64('9')})
	otherKey, err := FinalRenderCacheKey(other)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := otherKey.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == otherDigest {
		t.Fatal("changing the encoder policy must change the cache key")
	}
	if _, err := FinalRenderCacheKey(compilePlanWithPolicy(t, nil)); err == nil {
		t.Fatal("a plan without a policy must have no cache key")
	}
}

// ── SmartResolver decisions ────────────────────────────────────────────

func TestResolveFullRenderWithoutPolicy(t *testing.T) {
	resolver := NewSmartResolver(&fakeProber{facts: map[string]AssetFacts{}}, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), compilePlanWithPolicy(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionRender || report.VideoCompatible {
		t.Fatalf("plan without policy must render, got %+v", report)
	}
	if len(report.Reasons) == 0 {
		t.Fatal("decision must carry reasons")
	}
}

func TestResolveFullRenderWhenStreamCopyDisabled(t *testing.T) {
	policy := validExecutionPolicy()
	policy.AllowStreamCopy = false
	plan := compilePlanWithPolicy(t, policy)
	resolver := NewSmartResolver(&fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": compatibleFacts(), "/tmp/b.mp4": compatibleFacts()}}, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionRender {
		t.Fatalf("copy disabled by policy must render, got %+v", report)
	}
}

func TestResolveCacheHit(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	key, err := FinalRenderCacheKey(plan)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := key.Digest()
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeCache{hits: map[string]bool{digest: true}}
	resolver := NewSmartResolver(nil, cache, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionCache || !report.VideoCompatible || !report.AudioCompatible {
		t.Fatalf("expected CACHE_HIT, got %+v", report)
	}
}

func TestResolveCacheProbeFailureDegradesToRender(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	resolver := NewSmartResolver(&fakeProber{facts: map[string]AssetFacts{}}, &fakeCache{err: errors.New("cache down")}, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionRender {
		t.Fatalf("cache outage must degrade to render, got %+v", report)
	}
}

func TestResolveStreamCopyWhenAllSourcesCompatible(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	prober := &fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": compatibleFacts(), "/tmp/b.mp4": compatibleFacts()}}
	resolver := NewSmartResolver(prober, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionCopy || !report.VideoCompatible || !report.AudioCompatible {
		t.Fatalf("expected STREAM_COPY, got %+v", report)
	}
	if report.RequiresScale || report.RequiresFPSChange || report.RequiresPixelChange {
		t.Fatalf("compatible sources must not require changes: %+v", report)
	}
}

func TestResolveStreamCopyAcceptsRationalFPS(t *testing.T) {
	plan, err := Compile(CompileInput{
		JobID: "job-rational", Revision: "rev-1", OutputPath: "final.mp4",
		FrameRate: audio.FrameRate{Numerator: 30000, Denominator: 1001}, Timeline: testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
		ExecutionPolicy: validExecutionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	facts := compatibleFacts()
	facts.FPS, facts.FPSNum, facts.FPSDen = 29.97, 30000, 1001
	prober := &fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": facts, "/tmp/b.mp4": facts}}
	resolver := NewSmartResolver(prober, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionCopy {
		t.Fatalf("rational fps match must allow copy, got %+v", report)
	}
}

func TestResolveFullRenderOnEveryIncompatibility(t *testing.T) {
	cases := map[string]struct {
		facts     AssetFacts
		flagName  string
		flagValue bool
	}{
		"codec mismatch":        {facts: withFacts(func(f *AssetFacts) { f.VideoCodec = "h265" })},
		"geometry mismatch":     {facts: withFacts(func(f *AssetFacts) { f.Width, f.Height = 1280, 720 }), flagName: "RequiresScale", flagValue: true},
		"pixel format mismatch": {facts: withFacts(func(f *AssetFacts) { f.PixelFormat = "yuv444p" }), flagName: "RequiresPixelChange", flagValue: true},
		"fps mismatch":          {facts: withFacts(func(f *AssetFacts) { f.FPS, f.FPSNum, f.FPSDen = 25, 25, 1 }), flagName: "RequiresFPSChange", flagValue: true},
		"gop unverified":        {facts: withFacts(func(f *AssetFacts) { f.KeyframeInterval = 0 })},
		"gop mismatch":          {facts: withFacts(func(f *AssetFacts) { f.KeyframeInterval = 96 })},
	}
	for name, tc := range cases {
		plan := compilePlanWithPolicy(t, validExecutionPolicy())
		prober := &fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": tc.facts, "/tmp/b.mp4": compatibleFacts()}}
		resolver := NewSmartResolver(prober, nil, testTargetProfile())
		report, err := resolver.Resolve(context.Background(), plan)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if report.Mode != ExecutionRender || report.VideoCompatible {
			t.Fatalf("%s: expected FULL_RENDER, got %+v", name, report)
		}
		if tc.flagName != "" {
			switch tc.flagName {
			case "RequiresScale":
				if report.RequiresScale != tc.flagValue {
					t.Errorf("%s: RequiresScale=%v", name, report.RequiresScale)
				}
			case "RequiresPixelChange":
				if report.RequiresPixelChange != tc.flagValue {
					t.Errorf("%s: RequiresPixelChange=%v", name, report.RequiresPixelChange)
				}
			case "RequiresFPSChange":
				if report.RequiresFPSChange != tc.flagValue {
					t.Errorf("%s: RequiresFPSChange=%v", name, report.RequiresFPSChange)
				}
			}
		}
	}
}

func TestResolveDegradesToRenderWhenProbeFails(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	prober := &fakeProber{
		facts: map[string]AssetFacts{"/tmp/b.mp4": compatibleFacts()},
		errs:  map[string]error{"/tmp/a.mp4": errors.New("unreadable")},
	}
	resolver := NewSmartResolver(prober, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionRender {
		t.Fatalf("probe failure must degrade to render, got %+v", report)
	}
}

func TestResolveRequiresAudioMixWhenFinalAudioNotCopyEligible(t *testing.T) {
	plan, err := Compile(CompileInput{
		JobID: "job-1", Revision: "rev-1", OutputPath: "final.mp4", FrameRate: audio.IntegerFrameRate(30),
		Timeline: testRenderTimeline(),
		Manifest: []AssetManifestEntry{
			{AssetID: "clip-a", Path: "/tmp/a.mp4", SHA256: hash64('a'), FrameCount: 2000},
			{AssetID: "clip-b", Path: "/tmp/b.mp4", SHA256: hash64('b'), FrameCount: 1000},
		},
		FinalAudio:      &FinalAudioAsset{AssetID: "final", Path: "/tmp/final.m4a", SHA256: hash64('f'), CopyEligible: false},
		ExecutionPolicy: validExecutionPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prober := &fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": compatibleFacts(), "/tmp/b.mp4": compatibleFacts()}}
	resolver := NewSmartResolver(prober, nil, testTargetProfile())
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.AudioCompatible || !report.RequiresAudioMix {
		t.Fatalf("non-copy-eligible final audio must require an audio mix: %+v", report)
	}
	// The video decision is orthogonal to the audio path: video stream copy
	// stays eligible; the executor mixes/re-encodes audio at mux time.
	if report.Mode != ExecutionCopy || !report.VideoCompatible {
		t.Fatalf("video stream copy must stay eligible with an audio mix pending: %+v", report)
	}
}

func TestResolveRejectsDriftedPlan(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	plan.ManifestSHA256 = hash64('f')
	resolver := NewSmartResolver(nil, nil, testTargetProfile())
	if _, err := resolver.Resolve(context.Background(), plan); err == nil {
		t.Fatal("a drifted plan must be a hard error")
	}
}

func TestResolveIncompleteTargetProfileBlocksCopy(t *testing.T) {
	plan := compilePlanWithPolicy(t, validExecutionPolicy())
	prober := &fakeProber{facts: map[string]AssetFacts{"/tmp/a.mp4": compatibleFacts(), "/tmp/b.mp4": compatibleFacts()}}
	resolver := NewSmartResolver(prober, nil, TargetProfile{Codec: "h264"})
	report, err := resolver.Resolve(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != ExecutionRender {
		t.Fatalf("incomplete target profile must block copy, got %+v", report)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func jsonHasKey(raw []byte, key string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, ok := object[key]
	return ok
}

func withFacts(mutate func(*AssetFacts)) AssetFacts {
	facts := compatibleFacts()
	mutate(&facts)
	return facts
}
