package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const scriptPreparationVersion = "script-preparation-v1"

// ScriptPreparationPlanner creates the queue-aware preparation DAG for script
// generation. It only describes work; execution remains owned by the relevant
// existing pipeline components.
type ScriptPreparationPlanner struct {
	ProcessorVersion string
}

func NewScriptPreparationPlanner() *ScriptPreparationPlanner {
	return &ScriptPreparationPlanner{ProcessorVersion: scriptPreparationVersion}
}

func (p *ScriptPreparationPlanner) Plan(_ context.Context, j *job.Job) (PreparationPlan, error) {
	if j == nil {
		return PreparationPlan{}, fmt.Errorf("script preparation: job is nil")
	}
	if j.Type != job.TypeScriptGenerate && j.Type != job.TypeScriptGenerateItem {
		return PreparationPlan{}, fmt.Errorf("script preparation: unsupported job type %q", j.Type)
	}
	if p == nil {
		p = NewScriptPreparationPlanner()
	}
	payload := append([]byte(nil), j.Payload...)
	model := scriptPayloadModel(payload)
	phaseDefs := []struct {
		id       string
		kind     string
		depends  []string
		priority int
		cost     string
		resource string
	}{
		{"request.parse", "PREFLIGHT", nil, 100, "LOW", "CPU_LIGHT"},
		{"request.validate", "PREFLIGHT", []string{"request.parse"}, 100, "LOW", "CPU_LIGHT"},
		{"request.normalize", "PREFLIGHT", []string{"request.validate"}, 100, "LOW", "CPU_LIGHT"},
		{"source.resolve", "SOURCE", []string{"request.normalize"}, 95, "LOW", "NETWORK"},
		{"research.resolve", "RESEARCH", []string{"source.resolve"}, 80, "MEDIUM", "NETWORK"},
		{"narrative.plan", "LLM", []string{"source.resolve", "research.resolve"}, 75, "HIGH", "LLM"},
		{"script.generate", "LLM", []string{"narrative.plan"}, 70, "HIGH", "LLM"},
		{"scene.fanout", "SCENE_FANOUT", []string{"script.generate"}, 65, "LOW", "CPU_LIGHT"},
		{"scene.nlp", "NLP", []string{"scene.fanout"}, 60, "MEDIUM", "CPU_LIGHT"},
		{"scene.vidrush", "VIDRUSH", []string{"scene.nlp"}, 55, "MEDIUM", "NETWORK"},
		{"scene.tts", "TTS", []string{"scene.fanout"}, 55, "HIGH", "TTS"},
		{"scene.overlay", "OVERLAY", []string{"scene.nlp", "scene.tts"}, 45, "HIGH", "GPU_RENDER"},
		{"audio.prepare", "AUDIO", []string{"scene.tts"}, 40, "MEDIUM", "CPU_HEAVY"},
		{"documents.prepare", "DOCUMENTS", []string{"scene.fanout"}, 35, "LOW", "CPU_LIGHT"},
	}

	units := make([]PreparationUnit, 0, len(phaseDefs))
	for _, def := range phaseDefs {
		inputs := map[string]string{"phase": def.id, "processor_version": p.ProcessorVersion}
		if def.kind == "LLM" && model != "" {
			inputs["model"] = model
		}
		inputManifest := make(job.InputManifest, len(inputs))
		for key, value := range inputs {
			inputManifest[key] = value
		}
		fingerprint, err := PreparationUnitFingerprint(def.kind, j.Type, payload, inputs, def.depends, p.ProcessorVersion)
		if err != nil {
			return PreparationPlan{}, fmt.Errorf("script preparation unit %q: %w", def.id, err)
		}
		units = append(units, PreparationUnit{
			ID: def.id, Kind: def.kind, Fingerprint: fingerprint,
			DependsOn: append([]string(nil), def.depends...), Priority: def.priority,
			CostClass: def.cost, ResourceClass: def.resource, Reusable: true,
			Inputs:           inputManifest,
			ProcessorVersion: p.ProcessorVersion,
		})
	}
	return PreparationPlan{JobID: j.ID, Units: units}, nil
}

// scriptPayloadModel extracts the explicit model override without making the
// preparation planner depend on the script API DTO. Empty/invalid payloads
// intentionally fall back to the runtime's configured default model.
func scriptPayloadModel(payload []byte) string {
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	return envelope.Model
}

// MarshalJSON makes the planner version visible in diagnostics without
// changing the planner interface or embedding execution dependencies.
func (p ScriptPreparationPlanner) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ProcessorVersion string `json:"processor_version"`
	}{p.ProcessorVersion})
}

var _ PreparationPlanner = (*ScriptPreparationPlanner)(nil)
