// Package jobs — fake_broker_test.go (PR-jobs-retry-contract test support).
//
// Provides a non-nil job.JobBroker stub for internal tests that need to
// construct a *Service but do not exercise any broker method. The stub
// satisfies the interface structurally by embedding job.JobBroker; any
// actual method dispatch panics, which is acceptable because the tests
// using it only call Service methods that do not touch the broker.
package queue

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// nakedJobBroker is a non-nil job.JobBroker stub. It is intentionally
// minimal: tests that need method dispatch must provide their own
// implementation (e.g. correlationTimeoutBroker, enqueue_service_test.go).
type nakedJobBroker struct{ job.JobBroker }

// Compile-time pin: nakedJobBroker satisfies job.JobBroker.
var _ job.JobBroker = nakedJobBroker{}
