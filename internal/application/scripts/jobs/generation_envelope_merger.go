// Package jobs — generation_envelope_merger.go is the canonical
// owner of the C10 dual-shape (Data + Artifacts) broker-side
// envelope assembly (godlike/06 SSOT: one owner per fact — the
// typed envelope marshal-unmarshal cycle that lets the typed
// ExecutionResult[domainScript.GenerationResult]{Data,Artifacts}
// shape round-trip into the broker map[string]any contract).
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//
//	(1) Type envelope assembly is PURE: no I/O, no log writes, no
//	    DB calls, no filesystem ops. The marshal/unmarshal cycle
//	    is the LEGAL boundary between the typed domain shape and
//	    the job-system map contract.
//	(2) The handler does NOT inline the marshal/unmarshal cycle
//	    directly. It calls MergeTypedExecutionEnvelope, which
//	    keeps the dual-shape discipline in one place (so a future
//	    schema version bump on the typed envelope modifies one
//	    function, not every handler site).
//
// godlike/07 typed-error contract: marshal/unmarshal errors are
// returned via the typed Output[T] envelope (Godlike/07 fail-closed:
// the handler treats an envelope-merge failure as a manifest-not-
// injected condition — the typed single/multi envelope still
// propagates via the broker wire, but the dual shape is absent).
//
// godlike/07 honest-limitation: this file targets ~70 LoC. The
// marshal/unmarshal cycle + the typed envelope construction + the
// ManifestKey sidecar write are inherent to the C10 dual-shape
// discipline. Forward-pointer for SLIM (none planned — the file
// is already under any reasonable cap).
package jobs

import (
	"encoding/json"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MergeTypedExecutionEnvelope takes the canonical §8.4 result +
// manifest and merges them into the broker handlerResult map under:
//   - handlerResult["data"] = marshal of *GenerationResult (the
//     typed Data half of ExecutionResult[GenerationResult])
//   - handlerResult["artifacts"] = marshal of *ArtifactManifest
//     (the typed Artifacts half)
//   - handlerResult[job.ManifestKey] = manifest (the runner
//     lookup-key sidecar that job.Decode reads)
//
// The merge preserves any pre-existing handlerResult keys (the
// caller may have populated the typed single/multi envelope already;
// this function ADDS the dual-shape on top, not overwrites).
//
// On marshal/unmarshal failure, MergeTypedExecutionEnvelope returns
// a typed error so the caller (handler) can decide between
// propagated-error vs manifest-not-injected fallback per its
// logging contract.
func MergeTypedExecutionEnvelope(
	handlerResult map[string]any,
	result *domainScript.GenerationResult,
	manifest *job.ArtifactManifest,
) error {
	if handlerResult == nil || result == nil || manifest == nil {
		return fmt.Errorf("envelope_merger: nil handlerResult/result/manifest is invalid")
	}
	envelope := job.ExecutionResult[domainScript.GenerationResult]{
		Data:      *result,
		Artifacts: manifest,
	}
	envBytes, mErr := json.Marshal(envelope)
	if mErr != nil {
		return fmt.Errorf("envelope_merger: marshal typed envelope: %w", mErr)
	}
	var envMap map[string]any
	if uErr := json.Unmarshal(envBytes, &envMap); uErr != nil {
		return fmt.Errorf("envelope_merger: unmarshal typed envelope: %w", uErr)
	}
	for k, v := range envMap {
		handlerResult[k] = v
	}
	handlerResult[job.ManifestKey] = manifest
	return nil
}
