package localization

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// fakeSourceResolver returns fixed source facts (or an error), recording the
// requested asset id for assertion.
type fakeSourceResolver struct {
	facts SourceFacts
	err   error
}

func (f fakeSourceResolver) ResolveSource(_ context.Context, assetID string) (SourceFacts, error) {
	if f.err != nil {
		return SourceFacts{}, f.err
	}
	return f.facts, nil
}

// validSourceFacts returns source facts matching validPlan()'s source
// reference (SHA256 = strings.Repeat("a", 64), asset id "source-asset-1").
func validSourceFacts() SourceFacts {
	return SourceFacts{
		AssetID:   "source-asset-1",
		LocalPath: "/media/source.mp4",
		SHA256:    strings.Repeat("a", 64),
		FrameRate: audio.IntegerFrameRate(30),
	}
}

// validCompilerConfig returns a canonical compiler config with a real 64-hex
// encoder policy hash.
func validCompilerConfig() CompilerConfig {
	return CompilerConfig{
		WorkDir:           "/tmp/renders",
		EncoderPolicyHash: strings.Repeat("b", 64),
	}
}

func newTestCompiler(t *testing.T, facts SourceFacts) *LocalizedClipCompiler {
	t.Helper()
	c, err := NewLocalizedClipCompiler(fakeSourceResolver{facts: facts}, validCompilerConfig())
	if err != nil {
		t.Fatalf("NewLocalizedClipCompiler: %v", err)
	}
	return c
}

// TestCompiler_ProducesSealedRenderPlan verifies the compiler maps a valid
// LocalizedClipPlan into a sealed, validated render.RenderPlan whose identity,
// timeline, manifest, and execution policy derive from the plan.
func TestCompiler_ProducesSealedRenderPlan(t *testing.T) {
	c := newTestCompiler(t, validSourceFacts())
	plan := validPlan()

	out, err := c.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.PlanSHA256 == "" || out.ManifestSHA256 == "" {
		t.Fatalf("plan must be sealed with hashes: %+v", out)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("compiled plan must validate: %v", err)
	}

	// Identity + output.
	if out.JobID != plan.JobID {
		t.Errorf("JobID: got %q, want %q", out.JobID, plan.JobID)
	}
	if out.OutputPath != "/tmp/renders/clip-1.es.mp4" {
		t.Errorf("OutputPath: got %q", out.OutputPath)
	}

	// Timeline: one segment covering the full plan duration.
	if len(out.Timeline.Segments) != 1 {
		t.Fatalf("timeline segments: got %d, want 1", len(out.Timeline.Segments))
	}
	seg := out.Timeline.Segments[0]
	if seg.DurationUS != plan.DurationMS*1000 {
		t.Errorf("timeline duration: got %d, want %d", seg.DurationUS, plan.DurationMS*1000)
	}
	if seg.Video.AssetID != plan.SourceAssetID {
		t.Errorf("timeline video asset: got %q, want %q", seg.Video.AssetID, plan.SourceAssetID)
	}

	// Manifest: the source asset, with a frame count covering the timeline.
	if len(out.Manifest) != 1 {
		t.Fatalf("manifest entries: got %d, want 1", len(out.Manifest))
	}
	entry := out.Manifest[0]
	if entry.AssetID != plan.SourceAssetID || entry.SHA256 != plan.SourceSHA256 {
		t.Errorf("manifest entry: got %+v, want asset %s / %s", entry, plan.SourceAssetID, plan.SourceSHA256)
	}

	// Execution policy: re-encode (subtitle burn), plan renderer + profile.
	policy := out.ExecutionPolicy
	if policy == nil {
		t.Fatal("execution policy must be set")
	}
	if policy.AllowStreamCopy {
		t.Error("AllowStreamCopy must be false for a localized (burned) render")
	}
	if policy.RendererVersion != plan.RendererVersion {
		t.Errorf("RendererVersion: got %q, want %q", policy.RendererVersion, plan.RendererVersion)
	}
	if policy.TargetProfileHash != canonicalSHA256(plan.OutputProfileHash) {
		t.Errorf("TargetProfileHash: got %q, want %q", policy.TargetProfileHash, canonicalSHA256(plan.OutputProfileHash))
	}

	// One video segment on the primary track, covering the full duration.
	if len(out.VideoTracks) != 1 || len(out.VideoTracks[0].Segments) != 1 {
		t.Fatalf("video tracks: got %+v, want one segment", out.VideoTracks)
	}
}

// TestCompiler_Deterministic verifies identical inputs always produce the
// identical sealed PlanSHA256.
func TestCompiler_Deterministic(t *testing.T) {
	c := newTestCompiler(t, validSourceFacts())
	plan := validPlan()

	first, err := c.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("first Compile: %v", err)
	}
	second, err := c.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("second Compile: %v", err)
	}
	if first.PlanSHA256 != second.PlanSHA256 {
		t.Fatalf("compile must be deterministic: %q vs %q", first.PlanSHA256, second.PlanSHA256)
	}
}

// TestCompiler_RejectsInvalidPlan verifies a plan that fails Validate never
// reaches render.Compile (fail-closed before any resolution).
func TestCompiler_RejectsInvalidPlan(t *testing.T) {
	c := newTestCompiler(t, validSourceFacts())
	plan := validPlan()
	plan.Fingerprint = "bogus"

	if _, err := c.Compile(context.Background(), plan); err == nil {
		t.Fatal("Compile must reject an invalid plan")
	}
}

// TestCompiler_RejectsSourceHashMismatch verifies a source whose bytes do not
// match the plan's SourceSHA256 is rejected (never rendered).
func TestCompiler_RejectsSourceHashMismatch(t *testing.T) {
	facts := validSourceFacts()
	facts.SHA256 = strings.Repeat("f", 64)
	c := newTestCompiler(t, facts)

	if _, err := c.Compile(context.Background(), validPlan()); err == nil {
		t.Fatal("Compile must reject a source sha256 mismatch")
	}
}

// TestCompiler_SourceResolverFailurePropagates verifies a resolver error is
// surfaced, not swallowed.
func TestCompiler_SourceResolverFailurePropagates(t *testing.T) {
	resolver := fakeSourceResolver{err: errors.New("drive unavailable")}
	c, err := NewLocalizedClipCompiler(resolver, validCompilerConfig())
	if err != nil {
		t.Fatalf("NewLocalizedClipCompiler: %v", err)
	}
	if _, err := c.Compile(context.Background(), validPlan()); err == nil {
		t.Fatal("Compile must propagate a source resolver error")
	}
}

// TestCompiler_NilResolverFailsConstruction verifies a compiler cannot be
// built without a source resolver.
func TestCompiler_NilResolverFailsConstruction(t *testing.T) {
	if _, err := NewLocalizedClipCompiler(nil, validCompilerConfig()); err == nil {
		t.Fatal("NewLocalizedClipCompiler must reject a nil resolver")
	}
}

// TestCompiler_CompilesNilReceiverFailsClosed verifies calling Compile on a
// nil compiler is a typed failure, not a panic.
func TestCompiler_CompilesNilReceiverFailsClosed(t *testing.T) {
	var c *LocalizedClipCompiler
	if _, err := c.Compile(context.Background(), validPlan()); err == nil {
		t.Fatal("Compile on a nil compiler must fail")
	}
}

// TestCompiler_InterfaceSatisfaction asserts the concrete compiler satisfies
// the Compiler interface at compile time.
func TestCompiler_InterfaceSatisfaction(t *testing.T) {
	var _ Compiler = (*LocalizedClipCompiler)(nil)
}
