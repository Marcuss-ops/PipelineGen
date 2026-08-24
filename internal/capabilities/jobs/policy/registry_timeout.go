// Package jobs — registry_timeout.go (PR-SPLIT-JOBS-REGISTRY-DEFINITIONS, July 2026).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): this file is the
// canonical SOLE owner of the HC-1 typed timeout port surface:
//
//   - TimeoutMap     : the type-keyed snapshot of every registered
//     job-type timeout (returned by (*Registry).Compose()).
//   - TimeoutResolver: the typed port both worker.go and the
//     bulk_upload config-port consume.
//
// Lookup paths preserved: jobs.TimeoutMap, jobs.TimeoutResolver, and any
// `reg.Compose()[j.Type]` snapshot pattern resolve identically pre/post
// split (same package).
//
// 3-file split layout (per d44e0239 pkg/retry canonical pattern):
//
//	registry_definitions.go  (slim: package doc + canonical policy constants)
//	registry_timeout.go     (this file: HC-1 typed port surface)
//	registry_types.go       (NEW: RegistryEntry + JobPolicy + Type... const block)
package policy

import "time"

// HC-1 (June 2026) replaces the pre-HC-1 package-level
// `var jobTimeoutRegistry` global in worker.go with a type-keyed
// lookup rooted here on Registry.
//
//   - TimeoutMap        : a type-keyed snapshot of every registered
//                         job-type timeout (Compose()).
//   - TimeoutResolver   : the typed port both worker.go and the
//                         bulk_upload config-port consume.

// TimeoutMap is the type-keyed lookup of per-job-type execution
// timeouts. Returned by (*Registry).Compose() as a fresh snapshot
// so callers cannot mutate the underlying registry state.
//
// Usage: `reg.Compose()[j.Type]` returns the canonical timeout for
// job type j.Type, or zero if not registered (worker.go treats zero
// as the canonical 10-minute default).
type TimeoutMap map[string]time.Duration

// TimeoutResolver is the typed timeout lookup port consumed by HC-1
// worker.go (replaces the pre-HC-1 package-level global) and by the
// HC-1 bulk_upload config-port (see clips.ClipConfigPort.JobTimeout
// + internal/app/clips_adapters_cfg.go::clipsCfgAdapter).
//
// *Registry satisfies this interface directly (via JobTimeout). A
// narrow port interface lets future consumers (e.g. an admin-driven
// override layer) satisfy the contract without forcing them to also
// be a Registry.
type TimeoutResolver interface {
	JobTimeout(jobType string) time.Duration
}
