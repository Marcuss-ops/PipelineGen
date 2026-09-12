package cliprender

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// sealedPlanFixture builds a valid sealed ClipRenderPlanV1 for the
// continuation contract tests.
func sealedPlanFixture(t *testing.T, runID string) ClipRenderPlanV1 {
	t.Helper()
	sourcePath := t.TempDir() + "/source.mp4"
	sourceBytes := []byte("continuation source")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := ClipRenderPlanV1{
		Version:    PlanVersion,
		RunID:      runID,
		DurationMS: 1000,
		Source:     PlanSource{AssetID: "source-asset-001", Path: sourcePath, SHA256: fmt.Sprintf("%x", sha256.Sum256(sourceBytes))},
		Background: &PlanBackground{Mode: BackgroundModeNone},
		Output:     PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      PlanAudio{Mode: AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: t.TempDir() + "/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal plan fixture: %v", err)
	}
	return plan
}

// TestParseRenderPhase pins the phase contract: an absent key is the submit
// phase (every existing caller keeps working), and an unknown value is a typed
// error rather than a silent fallback — a typo must never silently re-run
// submission (which would re-prepare and re-upload the clip's assets).
func TestParseRenderPhase(t *testing.T) {
	t.Run("absent key is submit", func(t *testing.T) {
		phase, err := ParseRenderPhase(map[string]any{"other": 1})
		if err != nil || phase != RenderPhaseSubmit {
			t.Fatalf("phase=%q err=%v, want submit/nil", phase, err)
		}
	})
	t.Run("nil payload is submit", func(t *testing.T) {
		phase, err := ParseRenderPhase(nil)
		if err != nil || phase != RenderPhaseSubmit {
			t.Fatalf("phase=%q err=%v, want submit/nil", phase, err)
		}
	})
	t.Run("empty value is submit", func(t *testing.T) {
		phase, err := ParseRenderPhase(map[string]any{payloadKeyRenderPhase: "  "})
		if err != nil || phase != RenderPhaseSubmit {
			t.Fatalf("phase=%q err=%v, want submit/nil", phase, err)
		}
	})
	t.Run("settle parses", func(t *testing.T) {
		phase, err := ParseRenderPhase(map[string]any{payloadKeyRenderPhase: "settle"})
		if err != nil || phase != RenderPhaseSettle {
			t.Fatalf("phase=%q err=%v, want settle/nil", phase, err)
		}
	})
	t.Run("unknown value fails closed", func(t *testing.T) {
		if _, err := ParseRenderPhase(map[string]any{payloadKeyRenderPhase: "settle-now"}); err == nil {
			t.Fatal("an unknown phase must be rejected, not defaulted to submit")
		}
	})
	t.Run("case is significant", func(t *testing.T) {
		if _, err := ParseRenderPhase(map[string]any{payloadKeyRenderPhase: "Settle"}); err == nil {
			t.Fatal("the phase vocabulary is lowercase; uppercase must not be accepted silently")
		}
	})
}

// TestSubmissionValidate pins the fail-closed rules on the durable submission
// record: an incomplete record can never start a continuation.
func TestSubmissionValidate(t *testing.T) {
	valid := Submission{RenderJobID: "clip-1", PlanSHA256: strings.Repeat("a", 64), State: RemoteRenderSubmitted, Attempt: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid submission rejected: %v", err)
	}
	cases := map[string]Submission{
		"missing render job id": {PlanSHA256: valid.PlanSHA256, State: RemoteRenderSubmitted, Attempt: 1},
		"bad plan digest":       {RenderJobID: "clip-1", PlanSHA256: "not-hex", State: RemoteRenderSubmitted, Attempt: 1},
		"unknown state":         {RenderJobID: "clip-1", PlanSHA256: valid.PlanSHA256, State: "WEIRD", Attempt: 1},
		"zero attempt":          {RenderJobID: "clip-1", PlanSHA256: valid.PlanSHA256, State: RemoteRenderSubmitted},
	}
	for name, sub := range cases {
		if err := sub.Validate(); err == nil {
			t.Errorf("%s: expected a typed rejection", name)
		}
	}
}

// TestContinuationRefValidate pins the address rules: a continuation whose
// blob address is malformed can never be resumed.
func TestContinuationRefValidate(t *testing.T) {
	if err := (ContinuationRef{SHA256: strings.Repeat("b", 64), SizeBytes: 12}).Validate(); err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	for name, ref := range map[string]ContinuationRef{
		"empty digest":  {SizeBytes: 12},
		"short digest":  {SHA256: "abc", SizeBytes: 12},
		"zero size":     {SHA256: strings.Repeat("b", 64)},
		"negative size": {SHA256: strings.Repeat("b", 64), SizeBytes: -1},
	} {
		if err := ref.Validate(); err == nil {
			t.Errorf("%s: expected a typed rejection", name)
		}
	}
}

// TestRemoteRenderStateTerminal pins the state vocabulary and its terminal set.
func TestRemoteRenderStateTerminal(t *testing.T) {
	terminal := []RemoteRenderState{RemoteRenderCompleted, RemoteRenderFailed}
	nonTerminal := []RemoteRenderState{RemoteRenderPreparing, RemoteRenderSubmitted, RemoteRenderRendering, RemoteRenderArtifactReady, RemoteRenderPublishing}
	for _, s := range append(append([]RemoteRenderState{}, terminal...), nonTerminal...) {
		if !s.IsValid() {
			t.Errorf("%q must be a canonical state", s)
		}
	}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q must be terminal", s)
		}
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%q must NOT be terminal", s)
		}
	}
	if RemoteRenderState("NOPE").IsValid() {
		t.Error("unknown state must not validate")
	}
}

// TestContinuationRoundTripsAndFailsClosed certifies the handoff record: the
// small payload round-trips through JSON, and it refuses to resume when its
// address is malformed.
func TestContinuationRoundTripsAndFailsClosed(t *testing.T) {
	c := Continuation{
		Submission: Submission{
			RenderJobID:   "clip-continuation-1",
			PlanSHA256:    strings.Repeat("c", 64),
			CorrelationID: "corr-1",
			State:         RemoteRenderSubmitted,
			Attempt:       1,
		},
		Resume: ContinuationRef{SHA256: strings.Repeat("d", 64), SizeBytes: 2048},
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeContinuation(raw)
	if err != nil {
		t.Fatalf("DecodeContinuation: %v", err)
	}
	if got.Submission.RenderJobID != "clip-continuation-1" || got.Resume.SHA256 != strings.Repeat("d", 64) {
		t.Fatalf("continuation did not round-trip: %+v", got)
	}
	// The payload stays small: it carries an address, never the document.
	if strings.Contains(string(raw), "\"plan\"") {
		t.Fatalf("the continuation payload must not inline the sealed plan: %s", string(raw))
	}

	t.Run("missing payload fails closed", func(t *testing.T) {
		if _, err := DecodeContinuation(nil); err == nil {
			t.Fatal("the settle phase must require a continuation payload")
		}
	})
	t.Run("corrupt payload fails closed", func(t *testing.T) {
		if _, err := DecodeContinuation(json.RawMessage(`{"submission":`)); err == nil {
			t.Fatal("a corrupt continuation payload must be rejected")
		}
	})
	t.Run("malformed address fails closed", func(t *testing.T) {
		bad := c
		bad.Resume = ContinuationRef{SHA256: "nope", SizeBytes: 1}
		if err := bad.Validate(); err == nil {
			t.Fatal("a continuation with a malformed address must be rejected")
		}
	})
}

// TestResumeDocumentAttributes pins the digest binding between the resume
// document and the submission it resumes: a drifted document must not be
// acted on, because it would publish an artifact nobody can attribute.
func TestResumeDocumentAttributes(t *testing.T) {
	plan := sealedPlanFixture(t, "clip-resume-1")
	doc := ResumeDocument{
		Plan:            plan,
		Request:         RenderRequest{SourceAssetID: "source-asset-001"},
		PublishFolderID: "folder-1",
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("valid resume document rejected: %v", err)
	}

	t.Run("run id drift fails closed", func(t *testing.T) {
		if err := doc.Attributes("some-other-clip", plan.PlanSHA256); err == nil {
			t.Fatal("a document for a different remote render must be rejected")
		}
	})
	t.Run("plan digest drift fails closed", func(t *testing.T) {
		if err := doc.Attributes(plan.RunID, strings.Repeat("e", 64)); err == nil {
			t.Fatal("a document whose plan digest drifted must be rejected")
		}
	})
	t.Run("missing source asset fails closed", func(t *testing.T) {
		empty := doc
		empty.Request.SourceAssetID = "  "
		if err := empty.Validate(); err == nil {
			t.Fatal("a resume document without a source asset must be rejected")
		}
	})
	t.Run("subtitles without a path fail closed", func(t *testing.T) {
		broken := doc
		broken.Subtitles = &SubtitleArtifact{SHA256: strings.Repeat("f", 64)}
		if err := broken.Validate(); err == nil {
			t.Fatal("a resume document carrying an unpathable subtitle artifact must be rejected")
		}
	})
}

// TestActiveKeyForIsStable pins the enqueue idempotency key: a retried submit
// must address the SAME settle job, and a new attempt must get a new one.
func TestActiveKeyForIsStable(t *testing.T) {
	a := ActiveKeyFor("clip-1", 1)
	if a != ActiveKeyFor("clip-1", 1) {
		t.Fatal("the active key must be deterministic for the same (render, attempt)")
	}
	if a == ActiveKeyFor("clip-1", 2) {
		t.Fatal("a new attempt must not collapse onto the previous settle job")
	}
	if a == ActiveKeyFor("clip-2", 1) {
		t.Fatal("different renders must not share a settle job")
	}
	if a != "clip.render.settle:clip-1:1" {
		t.Fatalf("active key shape changed: %q", a)
	}
}
