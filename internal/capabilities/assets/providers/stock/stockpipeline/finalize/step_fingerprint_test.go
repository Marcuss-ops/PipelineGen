package assets

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

func TestStepInputFingerprintDeterministic(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1", ChunkDurationSec: 20, ClipDurationSec: 5, MaxConcurrentJobs: 3}
	input := &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		Clips:         []ClipSpec{{URL: "https://example.com/source.mp4", StartSec: 10, EndSec: 15}},
		ChunkDuration: 20,
		ClipDuration:  5,
		NoAudio:       true,
	}
	previous := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 10, EndSec: 15}}}

	first := stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, previous)
	second := stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, previous)
	if first == "" || first != second {
		t.Fatalf("fingerprint is not deterministic: first=%q second=%q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("fingerprint length = %d, want SHA-256 hex length 64", len(first))
	}
}

func TestStepInputFingerprintChangesForRelevantIdentity(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1", ChunkDurationSec: 20, ClipDurationSec: 5, MaxConcurrentJobs: 3}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}, ChunkDuration: 20, ClipDuration: 5}
	previous := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	base := stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, previous)

	cases := []struct {
		name   string
		mutate func(*OrchestratorConfig, *RunInput, *RunState)
	}{
		{"url", func(_ *OrchestratorConfig, in *RunInput, _ *RunState) {
			in.DirectURLs[0] = "https://example.com/other.mp4"
		}},
		{"timestamp", func(_ *OrchestratorConfig, _ *RunInput, state *RunState) { state.Plan[0].EndSec = 6 }},
		{"configuration", func(c *OrchestratorConfig, _ *RunInput, _ *RunState) { c.MaxConcurrentJobs = 4 }},
		{"policy", func(c *OrchestratorConfig, _ *RunInput, _ *RunState) { c.PolicyVersion = "policy-v2" }},
		{"duration", func(c *OrchestratorConfig, in *RunInput, _ *RunState) { c.ClipDurationSec = 6; in.ClipDuration = 6 }},
		{"previous-output", func(_ *OrchestratorConfig, _ *RunInput, state *RunState) {
			state.Plan[0].SourceID = "https://example.com/changed-output.mp4"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseCfg := cfg
			caseInput := *input
			caseInput.DirectURLs = append([]string(nil), input.DirectURLs...)
			caseState := *previous
			caseState.Plan = append([]ClipPlan(nil), previous.Plan...)
			tc.mutate(&caseCfg, &caseInput, &caseState)
			got := stepInputFingerprint("job-1", "stock.extract_clips", caseCfg, &caseInput, &caseState)
			if got == base {
				t.Fatalf("fingerprint did not change for %s", tc.name)
			}
		})
	}
}

func TestStepInputFingerprintUsesV2ForAnyRealInput(t *testing.T) {
	legacy := legacyStepInputFingerprint("job-1", "stock.plan")
	cases := []struct {
		name  string
		input RunInput
	}{
		{name: "url", input: RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}},
		{name: "policy", input: RunInput{PolicyVersion: "policy-v1"}},
		{name: "lease", input: RunInput{FinalizationLease: finalization.Lease{LeaseID: "lease-1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stepInputFingerprint("job-1", "stock.plan", OrchestratorConfig{}, &tc.input, nil)
			if got == legacy {
				t.Fatalf("real input %q reused legacy fingerprint", tc.name)
			}
		})
	}
}

func TestStepInputFingerprintLegacyHelperRemainsStable(t *testing.T) {
	if got, want := legacyStepInputFingerprint("job-1", "stock.plan"), "job-1|stock.plan"; got != want {
		t.Fatalf("legacy helper = %q, want %q", got, want)
	}
}

func TestRunFingerprintUsesCanonicalPayloadFields(t *testing.T) {
	baseConfig := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1", ChunkDurationSec: 20, ClipDurationSec: 5}
	baseInput := &RunInput{
		DirectURLs:    []string{"https://example.com/source.mp4"},
		SearchQueries: []string{"boxing highlights"},
		FolderID:      "folder-1",
		ChunkDuration: 20,
		ClipDuration:  5,
	}

	fingerprint := func(cfg OrchestratorConfig, input *RunInput) string {
		runner := &orchestratorRunner{
			orch: &Orchestrator{cfg: cfg},
			in:   input,
		}
		return runner.RunFingerprint()
	}

	base := fingerprint(baseConfig, baseInput)
	if len(base) != 64 {
		t.Fatalf("run fingerprint length = %d, want SHA-256 hex length 64", len(base))
	}
	if got := fingerprint(baseConfig, baseInput); got != base {
		t.Fatalf("run fingerprint is not deterministic: first=%q second=%q", base, got)
	}

	cases := []struct {
		name   string
		mutate func(*RunInput)
	}{
		{"drive-url", func(in *RunInput) { in.DriveURLs = []string{"https://drive.example/source.mp4"} }},
		{"explicit-clip", func(in *RunInput) {
			in.Clips = []ClipSpec{{URL: in.DirectURLs[0], StartSec: 3, EndSec: 9, Title: "round 1"}}
		}},
		{"metadata", func(in *RunInput) {
			in.Metadata = &ChunkMetadataInput{Title: "Fight night", Tags: []string{"boxing"}}
		}},
		{"bounded-duration", func(in *RunInput) { in.TargetTotalDurationSeconds = 120 }},
		{"render-option", func(in *RunInput) { in.NoAudio = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := *baseInput
			input.DirectURLs = append([]string(nil), baseInput.DirectURLs...)
			input.SearchQueries = append([]string(nil), baseInput.SearchQueries...)
			tc.mutate(&input)
			if got := fingerprint(baseConfig, &input); got == base {
				t.Fatalf("run fingerprint did not change for relevant payload field %s", tc.name)
			}
		})
	}
}

func TestCheckpointFingerprintPayloadCarriesCanonicalInputAndPreviousResultHash(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1", MaxConcurrentJobs: 3}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}, Clips: []ClipSpec{{URL: "https://example.com/source.mp4", StartSec: 0, EndSec: 5}}}
	previous := &RunState{Plan: []ClipPlan{{SourceID: "https://example.com/source.mp4", StartSec: 0, EndSec: 5}}}
	payload, err := buildCheckpointFingerprintPayload("job-1", "stock.extract_clips", cfg, input, previous)

	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if got, want := payload.SchemaVersion, checkpointFingerprintVersion; got != want {
		t.Fatalf("schema version = %q, want %q", got, want)
	}
	if got, want := payload.JobID, "job-1"; got != want {
		t.Fatalf("job ID = %q, want %q", got, want)
	}
	if got, want := payload.StepKey, "stock.extract_clips"; got != want {
		t.Fatalf("step key = %q, want %q", got, want)
	}
	if len(payload.CanonicalInput) == 0 || string(payload.CanonicalInput) == "null" {
		t.Fatal("canonical input must be present")
	}
	if got, want := payload.PreviousResultHash, deterministicPreviousOutputHash(previous); got != want {
		t.Fatalf("previous result hash = %q, want %q", got, want)
	}
	if len(payload.PreviousResultHash) != 64 {
		t.Fatalf("previous result hash length = %d, want 64", len(payload.PreviousResultHash))
	}
}

func TestCheckpointFingerprintAllIdentityFieldsAffectDigest(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1", MaxConcurrentJobs: 3}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	previous := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	basePayload, err := buildCheckpointFingerprintPayload("job-1", "stock.extract_clips", cfg, input, previous)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	digest := func(payload checkpointFingerprintPayload) string {
		raw, marshalErr := marshalCheckpointFingerprintPayload(payload)
		if marshalErr != nil {
			t.Fatalf("marshal payload: %v", marshalErr)
		}
		return sha256String(string(raw))
	}
	base := digest(basePayload)
	cases := []struct {
		name   string
		mutate func(*checkpointFingerprintPayload)
	}{
		{"schema", func(p *checkpointFingerprintPayload) { p.SchemaVersion = "stock-step-fingerprint-test" }},
		{"job", func(p *checkpointFingerprintPayload) { p.JobID = "job-2" }},
		{"step", func(p *checkpointFingerprintPayload) { p.StepKey = "stock.publish" }},
		{"policy", func(p *checkpointFingerprintPayload) { p.PolicyVersion = "policy-v2" }},
		{"canonical-input", func(p *checkpointFingerprintPayload) { p.CanonicalInput = []byte(`{"urls":{"direct":["changed"]}}`) }},
		{"previous-result-hash", func(p *checkpointFingerprintPayload) { p.PreviousResultHash = sha256String("changed-result") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := basePayload
			tc.mutate(&candidate)
			if got := digest(candidate); got == base {
				t.Fatalf("digest did not change when %s changed", tc.name)
			}
		})
	}
}

func TestCheckpointStoreIdempotencyUsesFingerprintVersioning(t *testing.T) {
	store := steps.NewInMemoryStore()
	ctx := context.Background()
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1"}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	firstState := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	secondState := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 6}}}
	first := steps.StepKey{JobID: "job-1", StepKey: "stock.extract_clips", InputFingerprint: stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, firstState)}
	requireNoError(t, store.MarkStarted(ctx, first))
	requireNoError(t, store.MarkCompleted(ctx, first, []byte(`{"checkpoint_version":1}`), nil))
	if err := store.MarkStarted(ctx, first); err != steps.ErrStepAlreadyCompleted {
		t.Fatalf("same fingerprint retry error = %v, want ErrStepAlreadyCompleted", err)
	}

	second := first
	second.InputFingerprint = stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, secondState)
	if second.InputFingerprint == first.InputFingerprint {
		t.Fatal("different previous result must produce a different v3 fingerprint")
	}
	requireNoError(t, store.MarkStarted(ctx, second))
	rows, err := store.ListByJob(ctx, first.JobID)
	requireNoError(t, err)
	if got, want := len(rows), 2; got != want {
		t.Fatalf("fingerprint-versioned rows = %d, want %d", got, want)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStepInputFingerprintChangesWhenPreviousOutputChanges(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1"}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	firstState := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	secondState := &RunState{
		Plan:      []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}},
		Published: []ChunkState{{Index: 0, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 123}},
	}

	first := stepInputFingerprint("job-1", "stock.compose_chunks", cfg, input, firstState)
	second := stepInputFingerprint("job-1", "stock.compose_chunks", cfg, input, secondState)
	if first == second {
		t.Fatalf("fingerprint did not change when previous step output changed")
	}
}

func TestStepInputFingerprintPreviousOutputHashIsStableForEquivalentState(t *testing.T) {
	cfg := OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1"}
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	first := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	second := &RunState{Plan: []ClipPlan{{SourceID: input.DirectURLs[0], StartSec: 0, EndSec: 5}}}
	if got, want := deterministicPreviousOutputHash(first), deterministicPreviousOutputHash(second); got != want {
		t.Fatalf("equivalent previous results have different hashes: %q vs %q", got, want)
	}
	if got, want := stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, first), stepInputFingerprint("job-1", "stock.extract_clips", cfg, input, second); got != want {
		t.Fatalf("equivalent previous results have different fingerprints: %q vs %q", got, want)
	}
}

func TestRunFingerprintCachesIdentityForRunnerLifetime(t *testing.T) {
	input := &RunInput{DirectURLs: []string{"https://example.com/source.mp4"}}
	runner := &orchestratorRunner{
		orch: &Orchestrator{cfg: OrchestratorConfig{JobId: "job-1", PolicyVersion: "policy-v1"}},
		in:   input,
	}

	first := runner.RunFingerprint()
	input.DirectURLs[0] = "https://example.com/changed.mp4"
	input.Clips = []ClipSpec{{URL: input.DirectURLs[0], StartSec: 1, EndSec: 4}}
	second := runner.RunFingerprint()
	if first != second {
		t.Fatalf("cached run fingerprint changed after input mutation: first=%q second=%q", first, second)
	}
}
