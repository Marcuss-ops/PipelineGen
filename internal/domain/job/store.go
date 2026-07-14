// Package job — Store + JobBroker type aliases (Phase A.2).
//
// Production definitions of the persistence port (Store) and the
// composable broker port (JobBroker) live in internal/kernel/job/.
// This file re-exports them as type aliases for back-compat with
// 107 import sites in the codebase. The two interface types are the
// seam on which every concrete persistence implementation declares
// conformance (compile-time assertion in adapter packages).
//
// Rationale (preserved from the pre-Phase-A.2 source) lives in
// kernel/job/store.go::JobBroker — see ADR-0002 §D2
// (`architecture/decisions/0002-p2-p3-roadmap.md`) for the
// embedding-not-alias decision. A future PR-postgres that proposes
// collapsing JobBroker to `type JobBroker = Store` MUST first
// re-ratify §D2 — otherwise the rationale is silently lost.
package job

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2) ──────────

type (
	// Store is the canonical persistence contract for jobs
	// (see kernel/job.Store). All state-changing operations accept
	// the lease fencing tuple (workerID, leaseID, expectedRevision)
	// inline. Implementations MUST perform an optimistic-concurrency
	// check before mutating job state.
	Store = job.Store

	// JobBroker is the canonical port under which any persistence
	// implementation declares conformance
	// (`var _ job.JobBroker = (*Adapter)(nil)`).
	//
	// JobBroker embeds Store (Shape B per PR-B, Wave 22, June 2026).
	// Future broker-specific primitives (e.g. a cross-node reservation
	// API) extend JobBroker here without modifying the canonical Store
	// contract; adapters that cannot implement them stay out of the port
	// (per godlike/07 "no fake availability").
	JobBroker = job.JobBroker
)
