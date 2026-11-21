// Package job — failure.go: the canonical failure taxonomy carrier.
//
// A handler failure is not just a string. Two facts have to survive the trip
// from the handler to the worker's finalize path:
//
//   - the CLASS (why it failed), because a deterministic contract failure and a
//     provider outage demand opposite retry decisions; and
//   - the STAGE + CODE, because an operator grouping failures by message text
//     is how a whole class of runs drowns in one indistinguishable label
//     (measured 2026-11-17..20: 135 failed job docs, 12+ distinct causes, all
//     reported by the same `durable run failed` prefix).
//
// This file owns the vocabulary only. It declares no cross-package type and no
// policy about specific providers: the classifier that maps stage/code/message
// onto these classes lives with the component that knows those strings.
//
// IMPORTANT: ClassifiedFailure deliberately does NOT implement
// `IsRetryable() bool`. The retry gate is retry.IsTransient, a typed probe; a
// classified failure must never become retryable by accident just because it
// carries a class.
package job

import (
	"errors"
	"fmt"
	"strings"
)

// FailureClass is the canonical reason a job failed, in the granularity an
// operator needs to decide "is this work worth retrying?".
type FailureClass string

const (
	// FailureDeterministic is a contract/certification failure: the same
	// immutable request produces the same failure again. Retrying it burns an
	// attempt and the whole downstream cost (LLM, TTS, render) for nothing.
	FailureDeterministic FailureClass = "deterministic_contract"
	// FailureConfiguration is a missing capability (no translation provider for
	// a language pair, no warmable model). Not retryable until the deployment
	// changes.
	FailureConfiguration FailureClass = "configuration"
	// FailureProvider is a remote provider/render failure that may succeed on a
	// later attempt.
	FailureProvider FailureClass = "provider"
	// FailureDeadline is a cancellation or deadline (including a parent
	// context that died), which may succeed when the contention is gone.
	FailureDeadline FailureClass = "deadline"
	// FailureInfrastructure is a transport/queue/stall condition.
	FailureInfrastructure FailureClass = "infrastructure"
	// FailureUnknown is "no classification matched". It is deliberately
	// retryable: an unknown failure must not be downgraded to permanent by this
	// taxonomy.
	FailureUnknown FailureClass = "unknown"
)

// Retryable reports whether a NEW attempt could plausibly succeed WITHOUT new
// inputs. Only deterministic contract failures and configuration gaps are
// excluded; everything else keeps the historical retry behaviour.
func (c FailureClass) Retryable() bool {
	switch c {
	case FailureDeterministic, FailureConfiguration:
		return false
	default:
		return true
	}
}

// ClassifiedFailure carries the class plus the diagnostic coordinates.
//
// Error() keeps the original message readable and appends the class in a
// machine-greppable form, so the persisted job error is both human and
// parseable without a second schema.
type ClassifiedFailure struct {
	Class   FailureClass
	Stage   string
	Code    string
	Message string
}

// Error implements error.
func (e *ClassifiedFailure) Error() string {
	if e == nil {
		return "job failed"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "job failed"
	}
	return fmt.Sprintf("%s (class=%s stage=%s code=%s)", message, e.Class, e.Stage, e.Code)
}

// Permanent reports whether this failure must not be retried.
func (e *ClassifiedFailure) Permanent() bool {
	return e != nil && !e.Class.Retryable()
}

// NewClassifiedFailure builds a classified failure, normalising empty strings
// to the same markers the run summary already uses.
func NewClassifiedFailure(class FailureClass, stage, code, message string) *ClassifiedFailure {
	if class == "" {
		class = FailureUnknown
	}
	if strings.TrimSpace(stage) == "" {
		stage = "UNKNOWN_STAGE"
	}
	if strings.TrimSpace(code) == "" {
		code = "NO_ERROR_CODE"
	}
	return &ClassifiedFailure{Class: class, Stage: stage, Code: code, Message: message}
}

// ClassifiedFailureFrom reports whether err (or anything it wraps) carries a
// classification.
func ClassifiedFailureFrom(err error) (*ClassifiedFailure, bool) {
	if err == nil {
		return nil, false
	}
	var classified *ClassifiedFailure
	if errors.As(err, &classified) {
		return classified, true
	}
	return nil, false
}
