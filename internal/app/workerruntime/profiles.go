// Package workerruntime — profiles.go (Creator Blocco 1.1, July 2026).
//
// WorkerProfile and WorkerProfileRegistry implement the profile-based
// capability gating required by the Creator role. A profile declares
// the maximum set of job types a worker is permitted to claim; env
// overrides ($VELOX_WORKER_CAPABILITIES) can narrow but never expand
// the profile's allowed set.
//
// Profiles registered by NewProfileRegistry:
//
//	creator — script.generate (minimal script-only runtime)
//	content — full content pipeline capabilities, backed by the complete
//	          composition root and live handler registry
//	renderer — overlay.prepare + overlay.render, GPU/FFmpeg required
//
// ResolveCapabilities is the canonical entry point when a profile
// is active. It replaces ParseAndValidateCaps for profile-gated
// workers and enforces the ceiling invariant.
package workerruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// WorkerProfile declares the ceiling of job types a worker is
// permitted to claim, plus a global concurrency cap. The env var
// $VELOX_WORKER_CAPABILITIES can further narrow (but never expand)
// the AllowedJobTypes slice.
type WorkerProfile struct {
	// Name is the profile identifier matched against
	// $VELOX_WORKER_PROFILE (e.g. "creator").
	Name string

	// AllowedJobTypes is the MAXIMUM set of job types this profile
	// permits. Env overrides must be a subset.
	AllowedJobTypes []string

	// MaxParallel is the global concurrency cap for this profile.
	// Per-job-type concurrency is governed by the job Registry,
	// not by this field.
	MaxParallel int

	// RequiresGPU/RequiresFFmpeg are startup capability requirements.
	RequiresGPU    bool
	RequiresFFmpeg bool
}

// WorkerProfileRegistry is a read-only lookup table of named
// profiles. Populated once via NewProfileRegistry and never
// mutated after construction.
type WorkerProfileRegistry struct {
	profiles map[string]WorkerProfile
}

// NewProfileRegistry returns a registry populated with the built-in
// profiles. Callers must treat the returned registry as immutable.
func NewProfileRegistry() *WorkerProfileRegistry {
	return &WorkerProfileRegistry{
		profiles: map[string]WorkerProfile{
			"creator": {
				Name: "creator",
				AllowedJobTypes: []string{
					job.TypeScriptGenerate,
					job.TypeVoiceoverGenerateItem,
					job.TypeImageGenerateGoogle,
					appjobs.TypeMediaStock,
				},
				MaxParallel: 1, // script generation is memory-heavy
			},
			"content": {
				Name: "content",
				AllowedJobTypes: []string{
					job.TypeScriptGenerate,
					job.TypeVoiceoverGenerate,
					job.TypeVoiceoverGenerateItem,
					appjobs.TypeMediaStock,
					appjobs.TypeYouTubeClipExtract,
					appjobs.TypeImageGenerateGoogle,
					job.TypeClipRender,
					// The video.create workflow parent.
					// Assembly child types are intentionally absent until
					// production handlers are actually wired; advertising
					// them here would make the content worker fail startup or
					// claim jobs it cannot execute.
					job.TypeVideoCreate,
				},
				MaxParallel: 2,
			},
			"renderer": {
				Name: "renderer",
				AllowedJobTypes: []string{
					"overlay.prepare",
					"overlay.render",
				},
				MaxParallel:    1,
				RequiresGPU:    true,
				RequiresFFmpeg: true,
			},
		},
	}
}

// Lookup returns a copy of the named profile, or an error if
// the name is not registered. The copy is safe to mutate.
func (r *WorkerProfileRegistry) Lookup(name string) (*WorkerProfile, error) {
	if r == nil {
		return nil, fmt.Errorf("profile registry is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("worker profile name is empty")
	}
	p, ok := r.profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown worker profile: %q (available: %s)", name, r.AvailableNames())
	}
	// Return a copy so callers can't mutate the registry's canonical copy.
	copied := p
	copied.AllowedJobTypes = append([]string{}, p.AllowedJobTypes...)
	return &copied, nil
}

// AvailableNames returns a sorted, comma-separated list of
// registered profile names for error messages.
func (r *WorkerProfileRegistry) AvailableNames() string {
	if r == nil || len(r.profiles) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(r.profiles))
	for n := range r.profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ResolveCapabilities computes the effective capability set for a
// profile-gated worker. The algorithm enforces three invariants:
//
//  1. Profile ceiling: env override types MUST be a subset of
//     profile.AllowedJobTypes. Expansion attempts fail closed.
//  2. Registration gate: every resolved type MUST exist in
//     registeredTypes (the handler Dispatcher's view).
//  3. Non-empty output: the resolved set must contain at least
//     one job type.
//
// When envOverride is empty, the profile's AllowedJobTypes are
// used directly (after dedup + sort + registration gate).
//
// Returns a sorted, deduplicated WorkerCapabilities on success,
// or a wrapped error that the caller logs and surfaces as a
// startup failure.
func ResolveCapabilities(profile *WorkerProfile, envOverride string, registeredTypes []string) (appjobs.WorkerCapabilities, error) {
	if profile == nil {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("worker profile is nil")
	}
	if len(profile.AllowedJobTypes) == 0 {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("profile %q has no allowed job types", profile.Name)
	}

	// Build the profile's allowed set for O(1) membership checks.
	profileSet := make(map[string]struct{}, len(profile.AllowedJobTypes))
	for _, t := range profile.AllowedJobTypes {
		profileSet[t] = struct{}{}
	}

	var requested []string
	var payloadMatch job.PayloadMatch

	if strings.TrimSpace(envOverride) == "" {
		// No env override — use the profile's full allowed set.
		requested = append([]string{}, profile.AllowedJobTypes...)
	} else {
		// Env override present — parse and validate against profile ceiling.
		var caps appjobs.WorkerCapabilities
		if err := json.Unmarshal([]byte(envOverride), &caps); err != nil {
			return appjobs.WorkerCapabilities{}, fmt.Errorf("malformed VELOX_WORKER_CAPABILITIES JSON: %w", err)
		}
		if len(caps.JobTypes) == 0 {
			return appjobs.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES has empty job_types array")
		}
		// A payload scope is orthogonal to the profile's job-type ceiling (it
		// narrows WITHIN an allowed type) but an unusable one fails closed here,
		// exactly as it does on the unprofiled path.
		if err := job.ValidatePayloadMatch(caps.PayloadMatch); err != nil {
			return appjobs.WorkerCapabilities{}, fmt.Errorf("VELOX_WORKER_CAPABILITIES payload_match: %w", err)
		}
		payloadMatch = caps.PayloadMatch

		for _, jt := range caps.JobTypes {
			jt = strings.TrimSpace(jt)
			if jt == "" {
				continue
			}
			if _, ok := profileSet[jt]; !ok {
				return appjobs.WorkerCapabilities{}, fmt.Errorf(
					"VELOX_WORKER_CAPABILITIES requests job type %q which is not allowed by profile %q (ceiling: %s)",
					jt, profile.Name, strings.Join(profile.AllowedJobTypes, ", "))
			}
			requested = append(requested, jt)
		}
	}

	// Gate: every requested type must be registered in the worker's Dispatcher.
	registered := make(map[string]struct{}, len(registeredTypes))
	for _, t := range registeredTypes {
		registered[t] = struct{}{}
	}

	seen := make(map[string]struct{})
	var validated []string
	for _, jt := range requested {
		if _, ok := seen[jt]; ok {
			continue
		}
		seen[jt] = struct{}{}
		if _, ok := registered[jt]; !ok {
			// Silently skip types that are allowed by the profile
			// but not registered in the worker dispatcher. This
			// enables opt-in gating: the profile declares the
			// ceiling, the dispatcher determines which types are
			// actually available. The worker starts with the
			// intersection.
			continue
		}
		validated = append(validated, jt)
	}

	if len(validated) == 0 {
		return appjobs.WorkerCapabilities{}, fmt.Errorf("profile %q resolved to empty capability set", profile.Name)
	}

	sort.Strings(validated)
	// Preserve hardware capability declarations from the profile. The old
	// implementation returned only JobTypes, silently dropping GPU/FFmpeg
	// facts before registration. The payload scope (when the env declared one)
	// travels with the job types so a profile-gated pool can still be a
	// phase-scoped pool.
	return appjobs.WorkerCapabilities{
		JobTypes:     validated,
		PayloadMatch: payloadMatch,
		GPU:          profile.RequiresGPU,
		FFmpeg:       profile.RequiresFFmpeg,
	}, nil
}
