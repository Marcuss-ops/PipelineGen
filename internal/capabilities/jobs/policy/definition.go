// Package stockbuild — definition.go (P0-2 stock-pipeline refactor, July 2026).
//
// Godlike/06 SSOT: this file is the SOLE canonical owner of the
// youtube.stock.build.v1 JobDefinition literal + the matching
// PayloadCodec / ResultCodec descriptor markers. Every registration
// site MUST bind via the constructors in this file; ad-hoc
// registration literals are forbidden (drift here orphans
// dispatcher vs. handler registration).
package policy

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ─── Canonical Job Definition ────────────────────────────────────────────────

// Definition returns the canonical JobDefinition for youtube.stock.build.v1.
// The literal is the SINGLE source of truth referenced by registration.go
// (the handler-binding code path) and validation_test.go
// (the registry-composition test surface).
//
// Parameters per kernel/job.JobDefinition contract:
//   - Type:           "youtube.stock.build.v1" — referenced by
//     wsbroker.FindByType(jobType) and the worker
//     runtime's handler-claim lookup.
//   - ExecutionClass: ExecutionCreatorAllowed — both the central
//     Sender and remote Workers can claim the job.
//     Stock builds carry heavy media IO; routing
//     to a Worker when one is available keeps the
//     Sender free for lighter orchestration.
//   - Queue:          "stock_orchestrator" — a separate queue from
//     "default" so stock builds don't block
//     lighter producer-side jobs.
//   - Timeout:        2h — covers the worst-case 8-phase
//     (SEARCH→SELECT→DOWNLOAD→EXTRACT→UPLOAD→PERSIST→
//     INDEX→VERIFY) run with a full 300-clip batch
//   - network-bound provider searches. A
//     pathological run that genuinely exceeds 2h
//     would re-enter via the broker's next retry.
//   - RetryPolicyKey: "stock_build_orchestrator" — decided by the
//     JobPolicy table (looked up at runtime by
//     jobkernel/kernelscheduler).
//   - ConcurrencyKey: "" — defaults to "single_global"; stock
//     builds are durable idempotent runs and the
//     reservation per (subject_id, run_id) is
//     enough.
//   - RequiredCapabilities: ["stock.build.youtube"] — the worker
//     supervisor advertises this capability set
//     for any node that has the yt-dlp pipeline
//     installed; the gate is checked at claim time.
//   - PayloadCodec/ResultCodec: marker with schemaVersion=v1.
//     Bodylessness today (godlike/06 back-compat:
//     the json.RawMessage round-trip happens at
//     the kernel/handler boundary); a future v2
//     schema would replace these markers in
//     lockstep with the legacy handlers' migration.
func Definition() job.JobDefinition {
	return job.JobDefinition{
		Type:                 JobType,
		Description:          "youtube stock pipeline orchestrator (subject-led canonical run; resume-capable; one-job-per-(subject,target))",
		ExecutionClass:       job.ExecutionCreatorAllowed,
		Queue:                "stock_orchestrator",
		Timeout:              2 * time.Hour,
		RetryPolicyKey:       "stock_build_orchestrator",
		ConcurrencyKey:       "", // single_global default
		RequiredCapabilities: []job.Capability{"stock.build.youtube"},
		PayloadCodec:         NewPayloadCodecMarker(),
		ResultCodec:          NewResultCodecMarker(),
		ArtifactPolicy: job.ArtifactPolicy{
			ProducesArtifacts: false, // purely orchestration; media_assets writes are post-commit outbox events
			RequireManifest:   false,
			MaxArtifacts:      0,
			MaxTotalBytes:     0,
		},
	}
}

// NewPayloadCodecMarker returns the canonical PayloadCodec marker for
// youtube.stock.build.v1. SchemaVersion() returns "v1" so a future
// v2 codec is identifiable and rejected at drain time by
// CompositionRoot codec-acceptance gates. godlike/06 SSOT: the
// marker is the SINGLE owner of the schema-version fact.
func NewPayloadCodecMarker() job.CodecDescriptorMarker {
	return job.NewCodecDescriptorMarker("pipelinegen.payload."+JobType+".v1", JobType)
}

// NewResultCodecMarker returns the canonical ResultCodec marker for
// youtube.stock.build.v1. Mirror of NewPayloadCodecMarker.
func NewResultCodecMarker() job.CodecDescriptorMarker {
	return job.NewCodecDescriptorMarker("pipelinegen.result."+JobType+".v1", JobType)
}
