package remotejob

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrRemotePending is the sentinel for a Master job that EXISTS and is still
// running. It is a sentinel rather than a message so a caller can hand the wait
// to another owner (a later attempt, another worker, a deferral) without
// matching on status strings, and it always travels with the Result that
// carries the job id — the handle a caller must keep in order to come back.
var ErrRemotePending = errors.New("remote job is not terminal yet")

// ErrRemoteStaleJob reports a Master job identity that must never take another
// FINALIZE: its preparation was canceled, it already ended terminal, or its
// lease is held elsewhere (`job_already_leased`). The identity lives on the
// Master, so clearing a local receipt does not make it reusable — the caller
// must archive the id it names (see StaleJobError) and prepare a fresh one.
var ErrRemoteStaleJob = errors.New("remote job identity is stale and cannot be finalized")

// StaleJobError is ErrRemoteStaleJob carrying the exact job identity that must
// never be finalized again plus the transport cause, so the caller can archive
// the handle and still see why the Master refused it.
type StaleJobError struct {
	JobID  string
	Status string
	Cause  error
}

func (e *StaleJobError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("remote job %s is stale (status=%s): %v: %v", e.JobID, e.Status, ErrRemoteStaleJob, e.Cause)
	}
	return fmt.Sprintf("remote job %s is stale (status=%s): %v", e.JobID, e.Status, ErrRemoteStaleJob)
}

// Unwrap exposes both the sentinel and the transport cause.
func (e *StaleJobError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrRemoteStaleJob}
	}
	return []error{ErrRemoteStaleJob, e.Cause}
}

// ErrRemoteInvalidPlan reports a Master rejection of the PLAN the producer
// submitted. It is deliberately NOT ErrRemoteStaleJob: rotating the remote
// identity cannot fix a deterministic defect in the plan, so the caller must
// fail the run and regenerate the plan instead of presenting the failure as a
// stale receipt (which would rotate the attempt and resubmit the same plan).
var ErrRemoteInvalidPlan = errors.New("remote job rejected the submitted plan")

// InvalidPlanError carries the Master code that proved the rejection.
type InvalidPlanError struct {
	JobID  string
	Status string
	Code   string
	Cause  error
}

func (e *InvalidPlanError) Error() string {
	return fmt.Sprintf("remote job %s plan rejected (%s): %v: %v", e.JobID, e.Code, ErrRemoteInvalidPlan, e.Cause)
}

// Unwrap exposes the sentinel and the transport cause, never the stale sentinel.
func (e *InvalidPlanError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrRemoteInvalidPlan}
	}
	return []error{ErrRemoteInvalidPlan, e.Cause}
}

// terminalJobStatus reports a Master status that can no longer be finalized.
func terminalJobStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FAILED", "CANCELED", "CANCELLED", "REJECTED":
		return true
	default:
		return false
	}
}

// ErrInvalidCompiledRenderPlan is the producer-owned classification for a
// Master/prepare rejection that is NOT a quota problem: the compiled plan asked
// for more source than the asset certifies. The Master historically returned
// `m2m_quota_failure` for it; the Producer must still name the real cause so an
// operator sees a plan bug, not a billing/quota incident.
var ErrInvalidCompiledRenderPlan = errors.New("invalid compiled render plan")

// PlanValidationError carries the Master's segment coordinates. It is terminal:
// retrying the same plan can only reproduce the same rejection, so it must not
// be wrapped as transient infrastructure noise.
type PlanValidationError struct {
	StatusCode int
	Code       string
	Message    string
	Segments   []string
}

func (e *PlanValidationError) Error() string {
	if e == nil {
		return ErrInvalidCompiledRenderPlan.Error()
	}
	detail := strings.TrimSpace(e.Message)
	if len(e.Segments) > 0 {
		detail += " (segments: " + strings.Join(e.Segments, ", ") + ")"
	}
	if detail == "" {
		detail = "no detail"
	}
	return fmt.Sprintf("invalid compiled render plan (HTTP %d, %s): %s", e.StatusCode, e.Code, detail)
}

func (e *PlanValidationError) Unwrap() error { return ErrInvalidCompiledRenderPlan }

// IsRetryable makes the canonical typed retry classifier return false. A plan
// defect is deterministic; only a changed plan (or a new attempt key) may help.
func (e *PlanValidationError) IsRetryable() bool { return false }

var (
	planSegmentIndexPattern = regexp.MustCompile(`segments\[(\d+)\]`)
	planSegmentNamePattern  = regexp.MustCompile(`segment\s+"([^"]+)"`)
)

// parsePlanValidationError turns the Master's misleading quota envelope into
// the real, typed cause. It deliberately requires BOTH the quota/invalid-plan
// code and a compiled-plan/source-range token: a genuine quota failure must
// keep its own identity and must never be re-labelled as a plan bug.
func parsePlanValidationError(status int, body string) *PlanValidationError {
	var envelope struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(body), &envelope)
	code := strings.ToLower(strings.TrimSpace(envelope.Error))
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = strings.TrimSpace(body)
	}
	if code != "m2m_quota_failure" && code != "invalid_compiled_render_plan" {
		return nil
	}
	if !strings.Contains(message, "CompiledRenderPlan") && !strings.Contains(message, "source_range_out_of_bounds") {
		return nil
	}
	seen := make(map[string]struct{})
	segments := make([]string, 0)
	for _, match := range planSegmentIndexPattern.FindAllStringSubmatch(message, -1) {
		key := "segments[" + match[1] + "]"
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		segments = append(segments, key)
	}
	for _, match := range planSegmentNamePattern.FindAllStringSubmatch(message, -1) {
		key := `segment "` + match[1] + `"`
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		segments = append(segments, key)
	}
	sort.Strings(segments)
	return &PlanValidationError{StatusCode: status, Code: code, Message: message, Segments: segments}
}

// remoteTransientErrorHints mark a failure the Master may still recover from:
// polling must keep observing it as RUNNING instead of rotating the identity.
var remoteTransientErrorHints = []string{
	"timeout", "timed out", "deadline", "rate_limit", "rate limit", "throttl",
	"temporar", "unavailable", "connection reset", "connection refused",
	"econnreset", "econnrefused", "try again", "retry", "busy", "429", "502", "503", "504",
}

// remotePermanentPreparationErrorCodes is the EXPLICIT allowlist of structured
// codes that name a deterministic preparation/plan defect. Only these end a
// wait. The list is an allowlist and not a denylist on purpose: a code this
// build has never seen is UNKNOWN, and an unknown failure may be recoverable,
// so classifying it as permanent would abandon a render the Master can still
// finish.
//
// Every entry names a defect in the submitted plan or in its preparation, not
// an infrastructure fault: replaying the same plan can only reproduce it.
var remotePermanentPreparationErrorCodes = map[string]struct{}{
	"window_extradata_identity_mismatch": {},
	"extradata_identity_mismatch":        {},
	"plan_validation_failed":             {},
	"invalid_compiled_plan":              {},
	"invalid_overlay_plan":               {},
	"unsupported_media_contract":         {},
	"media_contract_unsupported":         {},
	"asset_content_mismatch":             {},
	"schema_unsupported":                 {},
}

// remoteDiagnosticTokenPattern matches the Master's structured error tokens
// embedded in prose (`WINDOW_EXTRADATA_IDENTITY_MISMATCH`). Only a token of this
// shape can classify a failure; ordinary words cannot.
var remoteDiagnosticTokenPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+){2,}\b`)

// remotePreparationPosture reports a job that has NOT reached rendering, i.e.
// one whose failure is a preparation defect rather than a render fault. A job
// the Master already STARTED is never classified from this path: its error may
// be a recoverable worker fault, and killing the wait would strand a render that
// is still producing frames.
func remotePreparationPosture(state jobEnvelope) bool {
	if strings.TrimSpace(state.StartedAt) == "" {
		return true
	}
	if state.RuntimeAssetsPending {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(state.DispatchStatus), "waiting_runtime_assets") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(state.FinalizationStatus), "WAITING_PREPARATION")
}

// remotePermanentPreparationFailure classifies a reported error on a job that
// cannot progress. It is deliberately strict in three ways:
//
//  1. no error text  -> not a failure at all (a healthy RUNNING job);
//  2. no structured code and no diagnostic token -> unknown, therefore
//     TRANSIENT: an unrecognised error must stay observable, never terminal;
//  3. the job must be in a preparation posture -> a started render is never
//     rotated by this rule.
//
// The returned token is what the operator sees in the classified cause.
func remotePermanentPreparationFailure(state jobEnvelope) (string, bool) {
	message := strings.TrimSpace(state.Error)
	if message == "" {
		return "", false
	}
	if !remotePreparationPosture(state) {
		return "", false
	}
	code := strings.TrimSpace(state.ErrorCode)
	if code != "" {
		if _, permanent := remotePermanentPreparationErrorCodes[strings.ToLower(code)]; permanent {
			return code, true
		}
		// Allowlist, not denylist: an unrecognised structured code stays
		// observable instead of ending the wait on a recoverable failure.
		return "", false
	}
	lower := strings.ToLower(message)
	for _, hint := range remoteTransientErrorHints {
		if strings.Contains(lower, hint) {
			return "", false
		}
	}
	token := remoteDiagnosticTokenPattern.FindString(message)
	if token == "" {
		return "", false
	}
	if _, permanent := remotePermanentPreparationErrorCodes[strings.ToLower(token)]; !permanent {
		return "", false
	}
	return token, true
}

// staleJobResponse reports whether a Master error names a stale job identity:
// a lease held elsewhere or a job already canceled. The tokens are matched as
// whole phrases so a message about a released resource never classifies as a
// lease conflict.
func staleJobResponse(message string) bool {
	msg := strings.ToLower(message)
	for _, token := range []string{
		"job_already_leased", "job_already_canceled", "job_already_cancelled",
		"already leased", "already canceled", "already cancelled",
		"lease conflict", "lease_conflict",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}
