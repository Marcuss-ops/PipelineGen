package certification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(value bool) *bool { return &value }

func testPlan(items ...PlanItem) *Plan {
	return &Plan{
		SchemaVersion: "overlay-plan.v1",
		PlanID:        "plan-test",
		Fingerprint:   strings.Repeat("a", 64),
		Items:         items,
	}
}

func testItem(id, motion string, startMS, endMS int64) PlanItem {
	return PlanItem{
		ID:       id,
		SceneID:  "scene-1",
		Kind:     "text_phrase",
		MotionID: motion,
		StartMs:  startMS,
		EndMs:    endMS,
		Params:   map[string]any{"motion_origin": "auto"},
	}
}

func testExecution(item PlanItem, motion string, hash string) OverlayExecution {
	start, end, _, _ := windowOf(item, 24, 1)
	return OverlayExecution{
		OverlayID:           item.ID,
		Rendered:            boolPtr(true),
		ArtifactSHA256:      hash,
		ExecutedMotion:      motion,
		FallbackUsed:        boolPtr(false),
		PixelVerified:       boolPtr(true),
		VisibleIntervals:    []FrameInterval{{StartFrame: start, EndFrame: end}},
		CompositionVerified: boolPtr(true),
	}
}

func TestBuildFastModeSkipsUnmeasuredPixelProof(t *testing.T) {
	plan := testPlan(
		testItem("overlay-1", "apple_scale_settle", 0, 1000),
		testItem("overlay-2", "apple_fade_settle", 1000, 2000),
	)
	report, err := Build(Input{Plan: plan, Mode: ModeFast})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if report.Verdict != VerdictPass {
		t.Fatalf("verdict = %s, want PASS (fast mode does not promise pixel proof)", report.Verdict)
	}
	if report.Counts.Planned != 2 || report.Counts.Verified != 0 {
		t.Fatalf("counts = %+v, want planned=2 verified=0", report.Counts)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.visibility"); got != VerdictSkipped {
		t.Fatalf("visibility = %s, want SKIPPED", got)
	}
	if !containsLimitation(report.Limitations, "mode fast") {
		t.Fatalf("missing fast-mode limitation: %v", report.Limitations)
	}
}

func TestBuildCertifyWithoutEvidenceIsUnverifiable(t *testing.T) {
	plan := testPlan(testItem("overlay-1", "apple_scale_settle", 0, 1000))
	report, err := Build(Input{Plan: plan, Mode: ModeCertify})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if report.Verdict != VerdictUnverifiable {
		t.Fatalf("verdict = %s, want UNVERIFIABLE", report.Verdict)
	}
	if got := checkVerdict(report.Checks, "execution.coverage"); got != VerdictUnverifiable {
		t.Fatalf("execution coverage = %s, want UNVERIFIABLE", got)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.rendered"); got != VerdictUnverifiable {
		t.Fatalf("rendered = %s, want UNVERIFIABLE", got)
	}
}

func TestMotionLineageDistinguishesSilentSubstitutionFromDeclaredFallback(t *testing.T) {
	item := testItem("overlay-1", "apple_scale_settle", 0, 2000)
	plan := testPlan(item)
	hash := strings.Repeat("b", 64)
	artifact := &ArtifactEvidence{Kind: "overlay_artifact", SHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1}
	exec := testExecution(item, "apple_fade_settle", hash)

	report, err := Build(Input{
		Plan:      plan,
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, RunID: "run-1", ArtifactSHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1, Overlays: []OverlayExecution{exec}},
		Artifact:  artifact,
		Mode:      ModeCertify,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if report.Verdict != VerdictFail {
		t.Fatalf("verdict = %s, want FAIL for a silent substitution", report.Verdict)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.motion_lineage"); got != VerdictFail {
		t.Fatalf("motion lineage = %s, want FAIL", got)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.fallback"); got != VerdictFail {
		t.Fatalf("fallback = %s, want FAIL", got)
	}

	item.Params["motion_origin"] = "fallback"
	exec.FallbackUsed = boolPtr(true)
	report, err = Build(Input{
		Plan:      testPlan(item),
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, RunID: "run-1", ArtifactSHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1, Overlays: []OverlayExecution{exec}},
		Artifact:  artifact,
		Mode:      ModeCertify,
	})
	if err != nil {
		t.Fatalf("Build declared fallback: %v", err)
	}
	if report.Verdict != VerdictPass {
		t.Fatalf("verdict = %s, want PASS for a declared fallback: %+v", report.Verdict, report.Checks)
	}
	if !report.Overlays[0].FallbackUsed {
		t.Fatal("receipt did not record the declared fallback")
	}
}

func TestRequestedMotionMismatchIsSilentUnlessDeclared(t *testing.T) {
	item := testItem("overlay-1", "apple_fade_settle", 0, 1000)
	item.Params["requested_motion"] = "apple_scale_settle"

	report, err := Build(Input{Plan: testPlan(item), Mode: ModeFast})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.motion_request"); got != VerdictFail {
		t.Fatalf("motion request = %s, want FAIL", got)
	}
	if report.Overlays[0].RequestedMotion != "apple_scale_settle" || report.Overlays[0].CompiledMotion != "apple_fade_settle" {
		t.Fatalf("receipt lineage = %+v", report.Overlays[0])
	}

	item.Params["motion_origin"] = "fallback"
	report, err = Build(Input{Plan: testPlan(item), Mode: ModeFast})
	if err != nil {
		t.Fatalf("Build declared compile fallback: %v", err)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.motion_request"); got != VerdictPass {
		t.Fatalf("motion request = %s, want PASS for a declared compile fallback", got)
	}
}

func TestVisibilityEntryExitCoverage(t *testing.T) {
	item := testItem("overlay-1", "apple_scale_settle", 0, 2000)
	hash := strings.Repeat("c", 64)
	artifact := &ArtifactEvidence{SHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1}
	baseExec := testExecution(item, "apple_scale_settle", hash)

	cases := []struct {
		name      string
		mode      Mode
		intervals []FrameInterval
		want      Verdict
	}{
		{name: "full coverage", mode: ModeCertify, intervals: []FrameInterval{{0, 47}}, want: VerdictPass},
		{name: "late entry and early exit", mode: ModeCertify, intervals: []FrameInterval{{10, 30}}, want: VerdictFail},
		{name: "leak outside window", mode: ModeCertify, intervals: []FrameInterval{{-5, 50}}, want: VerdictFail},
		{name: "sampled edges are unverifiable not failed", mode: ModeNormal, intervals: []FrameInterval{{10, 30}}, want: VerdictUnverifiable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := baseExec
			exec.VisibleIntervals = tc.intervals
			report, err := Build(Input{
				Plan:      testPlan(item),
				Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, ArtifactSHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1, Overlays: []OverlayExecution{exec}},
				Artifact:  artifact,
				Mode:      tc.mode,
			})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if report.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s (checks %+v)", report.Verdict, tc.want, report.Checks)
			}
		})
	}
}

func TestVarietyGateOnlyInCertify(t *testing.T) {
	items := make([]PlanItem, 0, 8)
	for i := 0; i < 8; i++ {
		items = append(items, testItem("overlay-"+string(rune('a'+i)), "same_motion", int64(i)*1000, int64(i)*1000+900))
	}
	plan := testPlan(items...)

	fast, err := Build(Input{Plan: plan, Mode: ModeFast})
	if err != nil {
		t.Fatalf("Build fast: %v", err)
	}
	if fast.Verdict != VerdictPass || fast.Variety.Verdict != VerdictSkipped {
		t.Fatalf("fast verdict=%s variety=%s, want PASS/SKIPPED", fast.Verdict, fast.Variety.Verdict)
	}

	hash := strings.Repeat("d", 64)
	execs := make([]OverlayExecution, 0, len(items))
	for _, item := range items {
		execs = append(execs, testExecution(item, "same_motion", hash))
	}
	certify, err := Build(Input{
		Plan:      plan,
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, ArtifactSHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1, Overlays: execs},
		Artifact:  &ArtifactEvidence{SHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1},
		Mode:      ModeCertify,
	})
	if err != nil {
		t.Fatalf("Build certify: %v", err)
	}
	if certify.Variety.Verdict != VerdictFail || certify.Verdict != VerdictFail {
		t.Fatalf("certify verdict=%s variety=%s, want FAIL/FAIL", certify.Verdict, certify.Variety.Verdict)
	}
	if certify.Variety.DistinctMotions != 1 {
		t.Fatalf("distinct motions = %d, want 1", certify.Variety.DistinctMotions)
	}

	for i := range items {
		items[i].MotionID = "motion_" + string(rune('a'+i%4))
	}
	execs = execs[:0]
	for _, item := range items {
		execs = append(execs, testExecution(item, item.MotionID, hash))
	}
	mixed, err := Build(Input{
		Plan:      plan,
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, ArtifactSHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1, Overlays: execs},
		Artifact:  &ArtifactEvidence{SHA256: hash, FrameCount: 48, FPSNum: 24, FPSDen: 1},
		Mode:      ModeCertify,
	})
	if err != nil {
		t.Fatalf("Build mixed: %v", err)
	}
	if mixed.Variety.Verdict != VerdictPass || mixed.Verdict != VerdictPass {
		t.Fatalf("mixed verdict=%s variety=%s, want PASS/PASS", mixed.Verdict, mixed.Variety.Verdict)
	}
}

func TestArtifactHashBinding(t *testing.T) {
	item := testItem("overlay-1", "apple_scale_settle", 0, 1000)
	exec := testExecution(item, "apple_scale_settle", strings.Repeat("d", 64))

	report, err := Build(Input{
		Plan:      testPlan(item),
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, ArtifactSHA256: strings.Repeat("f", 64), Overlays: []OverlayExecution{exec}},
		Artifact:  &ArtifactEvidence{SHA256: strings.Repeat("e", 64)},
		Mode:      ModeFast,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := checkVerdict(report.Checks, "artifact.binding"); got != VerdictFail {
		t.Fatalf("artifact binding = %s, want FAIL", got)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.artifact"); got != VerdictFail {
		t.Fatalf("overlay artifact = %s, want FAIL", got)
	}
}

func TestFinalVideoBindingIsRunLevel(t *testing.T) {
	item := testItem("overlay-1", "apple_scale_settle", 0, 2000)
	hash := strings.Repeat("1", 64)
	exec := testExecution(item, "apple_scale_settle", strings.Repeat("2", 64))
	report, err := Build(Input{
		Plan:      testPlan(item),
		Execution: &ExecutionEvidence{SchemaVersion: ExecutionSchemaVersion, ArtifactSHA256: hash, Overlays: []OverlayExecution{exec}},
		Artifact:  &ArtifactEvidence{Kind: "final_video", SHA256: hash},
		Mode:      ModeFast,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := checkVerdict(report.Checks, "artifact.binding"); got != VerdictPass {
		t.Fatalf("run-level binding = %s, want PASS", got)
	}
	if got := checkVerdict(report.Overlays[0].Checks, "overlay.artifact"); got != VerdictPass {
		t.Fatalf("per-overlay artifact = %s, want PASS because the master cannot share the render hash", got)
	}
}

func TestDecodePlanAcceptsJobEnvelope(t *testing.T) {
	job := map[string]any{
		"result": map[string]any{
			"result": map[string]any{
				"overlay_plan": map[string]any{
					"schema_version": "overlay-plan.v1",
					"plan_id":        "nested",
					"items":          []any{map[string]any{"id": "one", "start_ms": 0, "end_ms": 500}},
				},
			},
		},
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodePlan(encoded)
	if err != nil {
		t.Fatalf("DecodePlan: %v", err)
	}
	if plan.PlanID != "nested" || len(plan.Items) != 1 {
		t.Fatalf("decoded plan = %+v", plan)
	}
}

func TestMarshalIsDeterministic(t *testing.T) {
	plan := testPlan(testItem("overlay-1", "apple_scale_settle", 0, 1000), testItem("overlay-2", "apple_fade_settle", 1000, 2000))
	report, err := Build(Input{Plan: plan, Mode: ModeFast})
	if err != nil {
		t.Fatal(err)
	}
	first, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("Marshal is not deterministic")
	}
}

func TestCorpusFixturesCertifyDeterministically(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	corpus := filepath.Join(root, "internal", "capabilities", "scripts", "testdata", "overlay_contract_corpus")
	manifestBytes, err := os.ReadFile(filepath.Join(corpus, "manifest.json"))
	if err != nil {
		t.Fatalf("read corpus manifest: %v", err)
	}
	var manifest struct {
		Fixtures []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Kind   string `json:"kind"`
			Expect string `json:"expect"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode corpus manifest: %v", err)
	}
	certified := 0
	for _, fixture := range manifest.Fixtures {
		if fixture.Kind != "pipelinegen_plan" || fixture.Expect != "compiled" {
			continue
		}
		source := strings.TrimPrefix(fixture.Source, "refactored/")
		planBytes, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			t.Fatalf("fixture %s: %v", fixture.ID, err)
		}
		plan, err := DecodePlan(planBytes)
		if err != nil {
			t.Fatalf("fixture %s decode: %v", fixture.ID, err)
		}
		report, err := Build(Input{Plan: plan, Mode: ModeFast})
		if err != nil {
			t.Fatalf("fixture %s build: %v", fixture.ID, err)
		}
		if report.SchemaVersion != SchemaVersion || len(report.Overlays) != len(plan.Items) {
			t.Fatalf("fixture %s report shape = %+v", fixture.ID, report)
		}
		first, err := Marshal(report)
		if err != nil {
			t.Fatalf("fixture %s marshal: %v", fixture.ID, err)
		}
		second, err := Marshal(report)
		if err != nil {
			t.Fatalf("fixture %s marshal twice: %v", fixture.ID, err)
		}
		if string(first) != string(second) {
			t.Fatalf("fixture %s is not deterministic", fixture.ID)
		}
		certified++
	}
	if certified == 0 {
		t.Fatal("no compiled pipelinegen fixture was certified")
	}
}

func containsLimitation(limitations []string, fragment string) bool {
	for _, limitation := range limitations {
		if strings.Contains(limitation, fragment) {
			return true
		}
	}
	return false
}
