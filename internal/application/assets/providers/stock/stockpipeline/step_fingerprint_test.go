package stockpipeline

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
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
