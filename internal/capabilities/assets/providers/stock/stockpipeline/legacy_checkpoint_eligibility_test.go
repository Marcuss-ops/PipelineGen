package stockpipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// The legacy-checkpoint compatibility decision is one long conjunction over
// config + input fields. It is decomposed into named dimensions so each one is
// reviewable and testable on its own; these tests mutate exactly ONE dimension
// at a time so a regression names the dimension it broke instead of only
// flipping a boolean.

func legacyBaseConfig() OrchestratorConfig { return OrchestratorConfig{} }

func legacyBaseInput() *RunInput { return &RunInput{} }

func TestLegacyEligibility_PolicySaltAbsent(t *testing.T) {
	require.True(t, legacyPolicySaltAbsent(legacyBaseConfig(), legacyBaseInput()))
	require.False(t, legacyPolicySaltAbsent(OrchestratorConfig{PolicyVersion: "policy-v1"}, legacyBaseInput()),
		"a config policy salt marks a modern job")
	require.False(t, legacyPolicySaltAbsent(legacyBaseConfig(), &RunInput{PolicyVersion: "run-v1"}),
		"a request policy salt marks a modern job")
}

func TestLegacyEligibility_LeaseAbsent(t *testing.T) {
	require.True(t, legacyLeaseAbsent(finalization.Lease{}))
	leases := map[string]finalization.Lease{
		"lease id":  {LeaseID: "lease-1"},
		"job id":    {JobID: "job-1"},
		"worker id": {WorkerID: "worker-1"},
		"attempt":   {Attempt: 1},
		"expiry":    {ExpiresAt: time.Now()},
	}
	for name, lease := range leases {
		require.Falsef(t, legacyLeaseAbsent(lease), "%s must not be legacy-shaped", name)
	}
}

func TestLegacyEligibility_ConfigDurationsAbsent(t *testing.T) {
	require.True(t, legacyConfigDurationsAbsent(legacyBaseConfig()))
	require.False(t, legacyConfigDurationsAbsent(OrchestratorConfig{ChunkDurationSec: 30}))
	require.False(t, legacyConfigDurationsAbsent(OrchestratorConfig{ClipDurationSec: 15}))
}

func TestLegacyEligibility_ExplicitTargetsAbsent(t *testing.T) {
	require.True(t, legacyExplicitTargetsAbsent(legacyBaseInput()))
	targets := map[string]*RunInput{
		"search query": {SearchQueries: []string{"mike tyson"}},
		"direct url":   {DirectURLs: []string{"https://example.com/v.mp4"}},
		"drive url":    {DriveURLs: []string{"https://drive.example.com/f"}},
		"clip spec":    {Clips: []ClipSpec{{URL: "https://example.com/v.mp4"}}},
	}
	for name, input := range targets {
		require.Falsef(t, legacyExplicitTargetsAbsent(input), "%s must not be legacy-shaped", name)
	}
}

func TestLegacyEligibility_BudgetOverridesAbsent(t *testing.T) {
	require.True(t, legacyBudgetOverridesAbsent(legacyBaseInput()))
	overrides := map[string]*RunInput{
		"total minutes":         {TotalMinutes: 10},
		"total duration":        {TargetTotalDurationSeconds: 600},
		"per-source duration":   {TargetDurationPerSourceSeconds: 60},
		"clips per source":      {ClipsPerSource: 3},
		"clip duration seconds": {ClipDurationSeconds: 20},
		"download mode":         {DownloadMode: "audio_video"},
		"max videos":            {MaxVideos: 5},
		"chunk duration":        {ChunkDuration: 30},
		"clip duration":         {ClipDuration: 15},
		"seconds per segment":   {SecondsPerSegment: 12},
	}
	for name, input := range overrides {
		require.Falsef(t, legacyBudgetOverridesAbsent(input), "%s must not be legacy-shaped", name)
	}
}

func TestLegacyEligibility_RenderOverridesAbsent(t *testing.T) {
	require.True(t, legacyRenderOverridesAbsent(legacyBaseInput()))
	overrides := map[string]*RunInput{
		"no audio":       {NoAudio: true},
		"no effects":     {NoEffects: true},
		"no transitions": {NoTransitions: true},
		"subfolder":      {Subfolder: "clips"},
		"folder name":    {FolderName: "clips"},
	}
	for name, input := range overrides {
		require.Falsef(t, legacyRenderOverridesAbsent(input), "%s must not be legacy-shaped", name)
	}
}

func TestLegacyEligibility_DestinationOverridesAbsent(t *testing.T) {
	require.True(t, legacyDestinationOverridesAbsent(legacyBaseInput()))
	overrides := map[string]*RunInput{
		"drive folder id":       {DriveFolderID: "drive-1"},
		"folder id":             {FolderID: "folder-1"},
		"drive folder resolved": {DriveFolderResolved: true},
		"operator metadata":     {Metadata: &ChunkMetadataInput{Title: "t"}},
		"persist":               {Persist: true},
	}
	for name, input := range overrides {
		require.Falsef(t, legacyDestinationOverridesAbsent(input), "%s must not be legacy-shaped", name)
	}
}

// TestLegacyCheckpointEligible_Composite pins the conjunction: a fully
// zero-value run is eligible (the v0 migration branch stays reachable), and
// every single modern dimension makes it ineligible.
func TestLegacyCheckpointEligible_Composite(t *testing.T) {
	require.True(t, legacyCheckpointEligible(legacyBaseConfig(), legacyBaseInput()),
		"a zero-value run remains resumable through the legacy branch")
	require.False(t, legacyCheckpointEligible(legacyBaseConfig(), nil),
		"a nil request can never be a legacy checkpoint")

	ineligible := map[string]struct {
		cfg   OrchestratorConfig
		input *RunInput
	}{
		"config policy salt":    {OrchestratorConfig{PolicyVersion: "p"}, legacyBaseInput()},
		"request policy salt":   {legacyBaseConfig(), &RunInput{PolicyVersion: "p"}},
		"orchestrator lease":    {OrchestratorConfig{Lease: finalization.Lease{LeaseID: "l"}}, legacyBaseInput()},
		"config chunk duration": {OrchestratorConfig{ChunkDurationSec: 30}, legacyBaseInput()},
		"config clip duration":  {OrchestratorConfig{ClipDurationSec: 15}, legacyBaseInput()},
		"explicit target":       {legacyBaseConfig(), &RunInput{DirectURLs: []string{"https://example.com/v.mp4"}}},
		"budget override":       {legacyBaseConfig(), &RunInput{MaxVideos: 5}},
		"render override":       {legacyBaseConfig(), &RunInput{NoEffects: true}},
		"folder override":       {legacyBaseConfig(), &RunInput{FolderName: "clips"}},
		"destination override":  {legacyBaseConfig(), &RunInput{FolderID: "folder-1"}},
		"operator metadata":     {legacyBaseConfig(), &RunInput{Metadata: &ChunkMetadataInput{Title: "t"}}},
		"persist flag":          {legacyBaseConfig(), &RunInput{Persist: true}},
		"finalization lease":    {legacyBaseConfig(), &RunInput{FinalizationLease: finalization.Lease{ExpiresAt: time.Now()}}},
	}
	for name, tc := range ineligible {
		require.Falsef(t, legacyCheckpointEligible(tc.cfg, tc.input), "%s must not be legacy-eligible", name)
	}
}
