package jobs

import (
	"context"
	"encoding/json"
	"testing"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type correlationRunRepo struct {
	run             *scriptgen.GenerationRun
	jobLookupCalls  int
	keyLookupCalls  int
	setJobIDCalls   int
	stageUpdateCall int
}

func (r *correlationRunRepo) Create(context.Context, *scriptgen.GenerationRun) error { return nil }
func (r *correlationRunRepo) Get(context.Context, string) (*scriptgen.GenerationRun, error) {
	return r.run, nil
}
func (r *correlationRunRepo) GetByJobID(context.Context, string) (*scriptgen.GenerationRun, error) {
	r.jobLookupCalls++
	return nil, nil
}
func (r *correlationRunRepo) GetByIdempotencyKey(context.Context, string) (*scriptgen.GenerationRun, error) {
	r.keyLookupCalls++
	return r.run, nil
}
func (r *correlationRunRepo) SetJobID(_ context.Context, _, jobID string) error {
	r.setJobIDCalls++
	r.run.JobID = jobID
	return nil
}
func (r *correlationRunRepo) UpdateStage(context.Context, string, scriptgen.RunStatus, scriptgen.Stage) error {
	r.stageUpdateCall++
	return nil
}
func (r *correlationRunRepo) FailRun(context.Context, scriptgen.FailRunInput) error { return nil }
func (r *correlationRunRepo) SavePartialResult(context.Context, string, *scriptgen.GenerateResult) error {
	return nil
}

func TestGenerateJobHandlerCorrelatesRunWhenJobIDBindingRacesWorker(t *testing.T) {
	// A run created by the submission service is persisted as PENDING; the
	// repository never returns a zero-value status, and the handler's stage
	// transition fires only for PENDING runs.
	runRepo := &correlationRunRepo{run: &scriptgen.GenerationRun{ID: "run-1", Status: scriptgen.RunStatusPending}}
	handler := NewGenerateJobHandler(nil, nil, zap.NewNop())
	handler.SetRunRepository(runRepo)

	result, err := handler.Handle(context.Background(), &job.Job{
		ID:            "job-1",
		CorrelationID: "idem-1",
		Payload:       json.RawMessage(`{}`),
	}, nil)
	if err == nil || result != nil {
		t.Fatalf("invalid payload should fail after correlation lookup: result=%v err=%v", result, err)
	}
	if runRepo.jobLookupCalls != 1 || runRepo.keyLookupCalls != 1 || runRepo.setJobIDCalls != 1 || runRepo.stageUpdateCall != 1 {
		t.Fatalf("race fallback did not self-heal run correlation: %#v", runRepo)
	}
	if runRepo.run.JobID != "job-1" {
		t.Fatalf("self-healed job ID = %q, want job-1", runRepo.run.JobID)
	}
}

func TestBuildDurableRunRequestUsesPersistedDocsIntent(t *testing.T) {
	tests := []struct {
		name           string
		durableEnabled bool
		envelope       string
		wantEnabled    bool
	}{
		{
			name:           "durable opt-out overrides job envelope opt-in",
			durableEnabled: false,
			envelope:       `{"version":2,"preset":"custom","items":[{"id":"item-1","docs":{"enabled":true,"languages":["it"],"folder_id":"job-folder"},"source":{"type":"text","topic":"topic"}}]}`,
			wantEnabled:    false,
		},
		{
			name:           "durable opt-in overrides job envelope opt-out",
			durableEnabled: true,
			envelope:       `{"version":2,"preset":"custom","items":[{"id":"item-1","docs":{"enabled":false},"source":{"type":"text","topic":"topic"}}]}`,
			wantEnabled:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env domainScript.GenerationEnvelopeV2
			if err := json.Unmarshal([]byte(tt.envelope), &env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			run := &scriptgen.GenerationRun{Request: scriptgen.GenerateRequest{
				IdempotencyKey: "idem-1",
				Docs:           scriptgen.DocumentsConfig{Enabled: tt.durableEnabled, Languages: []scriptgen.Language{"fr"}, FolderID: "durable-folder"},
				DocsEnabled:    !tt.durableEnabled, // deliberately contradictory legacy bit
			}}
			got, err := buildDurableRunRequest(&env, run)
			if err != nil {
				t.Fatalf("build durable request: %v", err)
			}
			enabled, languages, folderID := got.ResolveDocsConfig()
			if enabled != tt.wantEnabled || got.Docs.Enabled != tt.wantEnabled || got.DocsEnabled != tt.wantEnabled {
				t.Fatalf("Docs intent canonical=%t legacy=%t resolved=%t, want %t", got.Docs.Enabled, got.DocsEnabled, enabled, tt.wantEnabled)
			}
			if len(languages) != 1 || languages[0] != "fr" || folderID != "durable-folder" {
				t.Fatalf("persisted Docs routing not retained: languages=%v folder=%q", languages, folderID)
			}
		})
	}
}

var _ scriptgen.RunRepository = (*correlationRunRepo)(nil)
