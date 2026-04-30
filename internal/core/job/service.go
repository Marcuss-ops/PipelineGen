package job

// File refactored into modules:
// - job_core.go: Service struct, queue operations, job CRUD
// - job_lifecycle.go: Status updates, assignment, deletion
// - job_scheduling.go: Scheduling, zombie cleanup, auto-cleanup
// - job_helpers.go: Capability checks, status transitions, event logging

// All modules are ≤300 lines. No dead code found in original file.
