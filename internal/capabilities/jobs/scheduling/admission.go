// Package scheduling — admission.go: the pure admission policy that decides
// how many due scheduled jobs may be promoted to QUEUED on one tick.
//
// Two independent ceilings bound promotion:
//
//   - MaxConcurrent: how many jobs may be in flight (LEASED + RUNNING +
//     FINALIZING) at once. This is the "one behind the other" lane cap.
//   - DailyQuota: how many jobs may be promoted per UTC day. This is the
//     backpressure ceiling — once the day's quota is spent the remaining
//     due jobs stay SCHEDULED and roll into the next day.
//
// A non-positive value means "unbounded" for that dimension, so the zero
// AdmissionPolicy is the historical unlimited behaviour (every due job is
// promoted immediately) and existing deployments are unaffected.
//
// The policy is a pure function of (active, promotedToday): no clock, no
// store, no logging. That is what makes the 5000/day arithmetic testable
// without a database.
package scheduling

import "fmt"

// AdmissionPolicy bounds scheduled-job promotion per tick.
type AdmissionPolicy struct {
	// MaxConcurrent caps in-flight jobs (LEASED/RUNNING/FINALIZING).
	// <= 0 means unbounded.
	MaxConcurrent int
	// DailyQuota caps promotions per UTC day. <= 0 means unbounded.
	DailyQuota int
}

// Validate rejects negative ceilings, which would be configuration bugs
// rather than runtime conditions.
func (p AdmissionPolicy) Validate() error {
	if p.MaxConcurrent < 0 {
		return fmt.Errorf("admission policy: MaxConcurrent=%d must be >= 0", p.MaxConcurrent)
	}
	if p.DailyQuota < 0 {
		return fmt.Errorf("admission policy: DailyQuota=%d must be >= 0", p.DailyQuota)
	}
	return nil
}

// Slots returns how many due jobs may be promoted given the number of
// currently active jobs and how many were already promoted today.
//
// The result is the tightest of the three constraints (concurrency
// headroom, remaining daily quota, and "there is nothing to promote" = 0
// when a ceiling is already saturated).
func (p AdmissionPolicy) Slots(active, promotedToday int) int {
	slots := -1 // -1 means "no ceiling encountered yet"

	if p.MaxConcurrent > 0 {
		remaining := p.MaxConcurrent - active
		if remaining < 0 {
			remaining = 0
		}
		slots = remaining
	}
	if p.DailyQuota > 0 {
		remaining := p.DailyQuota - promotedToday
		if remaining < 0 {
			remaining = 0
		}
		if slots < 0 || remaining < slots {
			slots = remaining
		}
	}
	if slots < 0 {
		// Both dimensions unbounded: the caller's batch limit is the only
		// bound, so report "unbounded" as a large number.
		return int(^uint(0) >> 1)
	}
	return slots
}

// Unbounded reports whether neither ceiling is configured.
func (p AdmissionPolicy) Unbounded() bool {
	return p.MaxConcurrent <= 0 && p.DailyQuota <= 0
}
