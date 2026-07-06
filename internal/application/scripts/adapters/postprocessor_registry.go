// Package adapters — postprocessor_registry.go: split-topology landing page.
//
// Former monolithic file (699 LOC) decomposed July 2026 per AGENTS.md Pattern 5:
//
//   - postprocessor_composite.go — PostProcessor interface + PostProcessorRegistry struct +
//     all methods (Register, Run, ValidateRequested, mergePostProcessResult) +
//     ProcessorPolicy + defaultPolicyByName + DefaultPolicyFor
//   - postprocessor_voiceover.go — SceneVoiceover type
//   - postprocessor_image.go    — SceneImage type
//   - postprocessor_document.go — PipelineResult + PostProcessResult + ProcessInput + IsEmpty
//
// This file is intentionally empty — it serves as a landing page for the split topology.
package adapters
