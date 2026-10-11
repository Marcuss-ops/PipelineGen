package renderinggen

// localized_job_id_contract_test.go pins the CROSS-REPO id contract between
// the localization producer and the RenderingGen queue.
//
// The localized render revision (localization's LocalizedClipPlan → sealed
// render.RenderPlan.Revision) IS the RenderingGen queue job id: the executor
// submits plan.RunID and the worker uses it as ONE workspace filename
// component. RenderingGen tightened ValidateJobID on 2026-10-09 to reject path
// separators; the producer kept emitting `<clip>/<lang>/overlay-v3/<fp>`, so
// EVERY localized clip render failed at workspace materialization — after the
// plan had been compiled and the job accepted:
//
//	workspace: invalid job id "…" contains unsupported character '/'
//
// The fix is in localization (see localizedRevision); this test lives HERE, in
// the package that owns the RenderingGen boundary, because it must validate
// against the FOREIGN validator rather than a local re-spelling of its rule.
// A local copy of the rule would have stayed green through the whole outage:
// the rule that broke was RenderingGen's, and only the real implementation can
// fail this test when the two repositories drift again.
//
// The test drives the PRODUCTION path (NewLocalizedClipCompiler → Compile),
// not a string literal, so it fails if any part of the producer reintroduces a
// separator the queue rejects.

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// localizedSourceResolver is the minimal SourceResolver: it returns facts for
// the one source the test plan references, with the digest the plan pins.
type localizedSourceResolver struct {
	facts localization.SourceFacts
}

func (r localizedSourceResolver) ResolveSource(context.Context, string) (localization.SourceFacts, error) {
	return r.facts, nil
}

// localizedPlan builds a fully-resolved plan through the same public contract
// the fan-out uses, so the revision under test is the one production derives.
func localizedPlan(t *testing.T) localization.LocalizedClipPlan {
	t.Helper()
	sourceSHA := strings.Repeat("a", 64)
	plan := localization.LocalizedClipPlan{
		Version:           localization.LocalizedClipPlanVersion,
		JobID:             "job-1",
		SceneID:           "scene-1",
		ClipID:            "clip-1",
		SourceAssetID:     "source-asset-1",
		SourceSHA256:      sourceSHA,
		SourceLanguage:    "en",
		TargetLanguage:    "es",
		TranscriptTrackID: 101,
		TranscriptSHA256:  "transcript-sha",
		SubtitleTrackID:   202,
		SubtitleSHA256:    "subtitle-sha",
		SubtitleStyleHash: "style-sha",
		DurationMS:        8432,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		Priority:          1,
	}
	plan.Fingerprint = localization.Fingerprint(plan)
	if err := plan.Validate(); err != nil {
		t.Fatalf("test plan must validate: %v", err)
	}
	return plan
}

// TestLocalizedRenderRevisionIsAValidRenderingGenJobID is the regression pin
// for the 2026-10-10 localized-render outage: the revision the producer
// derives must satisfy RenderingGen's own job-id validator.
func TestLocalizedRenderRevisionIsAValidRenderingGenJobID(t *testing.T) {
	plan := localizedPlan(t)
	compiler, err := localization.NewLocalizedClipCompiler(
		localizedSourceResolver{facts: localization.SourceFacts{
			AssetID:   plan.SourceAssetID,
			LocalPath: "/media/source.mp4",
			SHA256:    plan.SourceSHA256,
			FrameRate: audio.IntegerFrameRate(30),
		}},
		localization.CompilerConfig{WorkDir: "/tmp/renders", EncoderPolicyHash: strings.Repeat("b", 64)},
	)
	if err != nil {
		t.Fatalf("NewLocalizedClipCompiler: %v", err)
	}
	renderPlan, err := compiler.Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	revision := renderPlan.Revision

	// The foreign validator is the authority: the queue will use this string
	// as one filename component in the worker workspace.
	if err := queueclient.ValidateJobID(revision); err != nil {
		t.Fatalf("localized render revision %q is not a valid RenderingGen job id: %v\n"+
			"the queue rejects it at workspace materialization, after the plan is compiled and the job accepted", revision, err)
	}

	// Pin the exact address so a future edit cannot silently re-address every
	// already-rendered artifact (a different id is a different remote job).
	fingerprint := plan.Fingerprint
	if len(fingerprint) > 16 {
		fingerprint = fingerprint[:16]
	}
	want := plan.ClipID + ":" + plan.TargetLanguage + ":overlay-v3:" + fingerprint
	if revision != want {
		t.Fatalf("revision = %q, want %q", revision, want)
	}

	// Negative control: the pre-fix spelling MUST fail the foreign validator.
	// Without this the test could pass against a validator that accepts
	// everything, i.e. it could not detect the regression it exists to catch.
	preFix := plan.ClipID + "/" + plan.TargetLanguage + "/overlay-v3/" + fingerprint
	if err := queueclient.ValidateJobID(preFix); err == nil {
		t.Fatalf("RenderingGen accepts the pre-fix '/'-separated id %q; this contract test can no longer detect the outage it pins", preFix)
	}
}
