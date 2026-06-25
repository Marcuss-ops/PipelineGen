package generation

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// HandlerFuncGenerator wraps an existing appjobs.HandlerFunc as a
// domaingeneration.Generator. This is the migration bridge: existing
// worker handlers can be registered as Generators without rewriting
// the execution logic.
type HandlerFuncGenerator struct {
	jobType string
	fn      appjobs.HandlerFunc
}

// NewHandlerFuncGenerator wraps a HandlerFunc as a Generator.
func NewHandlerFuncGenerator(jobType string, fn appjobs.HandlerFunc) *HandlerFuncGenerator {
	return &HandlerFuncGenerator{jobType: jobType, fn: fn}
}

func (g *HandlerFuncGenerator) Type() string { return g.jobType }

func (g *HandlerFuncGenerator) Execute(ctx context.Context, j jobdomain.Job, progress domaingeneration.ProgressReporter) (domaingeneration.Result, error) {
	tools := &appjobs.JobTools{
		Progress:    progress.Progress,
		Event:       progress.Event,
		IsCancelled: progress.IsCancelled,
	}
	raw, err := g.fn(ctx, &j, tools)
	if err != nil {
		return domaingeneration.Result{}, err
	}
	return handlerResultToGenerationResult(raw), nil
}

func handlerResultToGenerationResult(raw map[string]any) domaingeneration.Result {
	var result domaingeneration.Result
	if raw == nil {
		return result
	}
	// Preserve the raw map as Data for consumers that need the full output.
	data, _ := json.Marshal(raw)
	result.Data = data
	// Extract structured artifacts if present.
	if v, ok := raw["primary_artifact"]; ok {
		if s, ok := v.(string); ok {
			result.PrimaryArtifact = &domaingeneration.Artifact{
				Type: "auto",
				URI:  s,
			}
		}
	}
	if v, ok := raw["artifacts"]; ok {
		if arr, ok := v.([]any); ok {
			for _, a := range arr {
				if m, ok := a.(map[string]any); ok {
					art := domaingeneration.Artifact{}
					if t, ok := m["type"].(string); ok {
						art.Type = t
					}
					if uri, ok := m["uri"].(string); ok {
						art.URI = uri
					}
					result.Artifacts = append(result.Artifacts, art)
				}
			}
		}
	}
	if len(result.Artifacts) == 0 && result.PrimaryArtifact == nil {
		// If no structured artifacts, expose a single data artifact.
		result.PrimaryArtifact = &domaingeneration.Artifact{
			Type: "result",
			URI:  "",
		}
	}
	return result
}

// NewGenerationDispatcher returns an appjobs.HandlerFunc that resolves
// the job type from a generation registry and delegates execution to the
// matching Generator. This lets the worker dispatch generation jobs
// through the canonical Generator contract.
func NewGenerationDispatcher(genRegistry *Registry, executorRegistry map[string]domaingeneration.Generator) appjobs.HandlerFunc {
	return func(ctx context.Context, j *jobdomain.Job, tools *appjobs.JobTools) (map[string]any, error) {
		// First, try the executor registry (specific Generators).
		gen, ok := executorRegistry[j.Type]
		if !ok {
			// Fall back to the generation registry's Definitions to
			// reconstruct the public type and see if this is a known job.
			return nil, fmt.Errorf("no generator registered for job type: %s", j.Type)
		}
		pr := &jobToolsReporter{tools: tools}
		result, err := gen.Execute(ctx, *j, pr)
		if err != nil {
			return nil, err
		}
		// Convert Result back to map[string]any for backward compat with
		// the worker's result persistence (which stores map[string]any as JSON).
		return resultToMap(result), nil
	}
}

type jobToolsReporter struct {
	tools *appjobs.JobTools
}

func (r *jobToolsReporter) Progress(percent int, message string) {
	if r.tools != nil && r.tools.Progress != nil {
		r.tools.Progress(percent, message)
	}
}

func (r *jobToolsReporter) Event(eventType, message string, data map[string]any) {
	if r.tools != nil && r.tools.Event != nil {
		r.tools.Event(eventType, message, data)
	}
}

func (r *jobToolsReporter) IsCancelled() bool {
	if r.tools != nil && r.tools.IsCancelled != nil {
		return r.tools.IsCancelled()
	}
	return false
}

func resultToMap(result domaingeneration.Result) map[string]any {
	m := make(map[string]any)
	if result.PrimaryArtifact != nil {
		m["primary_artifact"] = map[string]any{
			"type": result.PrimaryArtifact.Type,
			"uri":  result.PrimaryArtifact.URI,
		}
	}
	if len(result.Artifacts) > 0 {
		arts := make([]map[string]any, 0, len(result.Artifacts))
		for _, a := range result.Artifacts {
			arts = append(arts, map[string]any{"type": a.Type, "uri": a.URI})
		}
		m["artifacts"] = arts
	}
	if len(result.Data) > 0 {
		var data any
		if err := json.Unmarshal(result.Data, &data); err == nil {
			m["data"] = data
		}
	}
	if len(result.Metadata) > 0 {
		m["metadata"] = result.Metadata
	}
	if len(m) == 0 {
		m["ok"] = true
	}
	return m
}
