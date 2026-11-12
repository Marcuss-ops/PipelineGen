// Package remotejob submits explicit final_job requests to the Master.
package remotejob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
	PollInterval   time.Duration
	PollTimeout    time.Duration
	// QueueTimeout bounds only the NOT-STARTED portion of a BOUNDED attach.
	// A job the Master has not started cannot finish, so waiting the whole
	// render budget for it changes nothing: r9 sat PENDING for 46 minutes and
	// was then cancelled. Zero disables the rule. An unbounded attach
	// (budget <= 0, the resume path) ignores it deliberately: by then the
	// render exists and abandoning it would only duplicate the wait.
	QueueTimeout time.Duration
}

type Result struct {
	JobID       string
	Status      string
	WorkerID    string
	ArtifactURL string
	SHA256      string
	// Timing decomposes the wait on the Master clock. Zero fields mean the
	// Master did not report that phase (or the job never reached it); they are
	// never inferred from local wall time, because the render outlives any one
	// attempt and a local clock cannot prove where the time went.
	Timing Timing
}

// WorkerTiming is the worker-side split of one remote render, relayed by the
// Master as `worker_timing` on GET /api/v1/jobs/{id}.
type WorkerTiming struct {
	LeasedAt   time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	Download   time.Duration
	Decode     time.Duration
	Compose    time.Duration
	Encode     time.Duration
	Mux        time.Duration
	Upload     time.Duration
	Frames     int64
	EncodeFPS  float64
}

// Timing is what a final-job wait is actually made of:
//
//	queue      created_at → started_at   (waiting for a worker/lease)
//	execution  started_at → completed_at (the remote render itself)
//	wait       the local attach wall    (polling, resumption, transport)
//	poll lag   terminal observation − completed_at (poll interval, not render)
//
// Queue+execution are the Master's numbers; wait+poll lag are what the pipeline
// adds on top. The worker-internal split (download/decode/compose/encode/mux/
// upload) is not exposed by GET /api/v1/jobs/{id} and therefore stays a
// documented gap rather than a guessed number.
type Timing struct {
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
	QueueWait   time.Duration
	Execution   time.Duration
	Wait        time.Duration
	PollLag     time.Duration
	Polls       int
	// Worker is the remote worker's own phase report, when the Master relays
	// it. It is the only way to see inside `execution_ms`: download, decode,
	// composition, encode, mux and upload all happen after the lease, so a
	// local clock cannot attribute them. Zero means "not reported".
	Worker WorkerTiming
}

type jobEnvelope struct {
	Job      json.RawMessage `json:"job"`
	ID       string          `json:"id"`
	JobID    string          `json:"job_id"`
	Status   string          `json:"status"`
	WorkerID string          `json:"worker_id"`
	Artifact json.RawMessage `json:"artifact"`
	Result   json.RawMessage `json:"result"`
	// The Master status API currently exposes these fields at the top level;
	// older deployments wrapped the same values in artifact/result.
	ArtifactURL    string `json:"artifact_url"`
	ArtifactSHA256 string `json:"sha256"`
	Error          string `json:"error"`
	// ErrorCode is the Master's STRUCTURED failure identity. The free-text
	// Error alone cannot distinguish a transient worker fault from a rejected
	// preparation, and guessing from prose is how a job that can never succeed
	// kept being polled. Absent on older Masters: classification then falls
	// back to the diagnostic token in Error.
	ErrorCode            string `json:"error_code"`
	DispatchStatus       string `json:"dispatch_status"`
	FinalizationStatus   string `json:"finalization_status"`
	RuntimeAssetsPending bool   `json:"runtime_assets_pending"`
	// Master-clock phase boundaries. GET /api/v1/jobs/{id} exposes created_at,
	// started_at and completed_at; a pre-only job keeps started_at null, which
	// is exactly why an unstarted phase must stay zero here.
	CreatedAt   string `json:"created_at"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	// WorkerTiming is relayed verbatim: the worker owns its phase names, and a
	// Master that has not implemented the report simply omits the key.
	WorkerTiming json.RawMessage `json:"worker_timing"`
}

// workerTimingWire is the on-the-wire worker report. Durations are integer
// milliseconds and times are RFC3339Nano, matching the Master contract.
type workerTimingWire struct {
	LeasedAt   string  `json:"leased_at"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	DownloadMS int64   `json:"download_ms"`
	DecodeMS   int64   `json:"decode_ms"`
	ComposeMS  int64   `json:"compose_ms"`
	EncodeMS   int64   `json:"encode_ms"`
	MuxMS      int64   `json:"mux_ms"`
	UploadMS   int64   `json:"upload_ms"`
	Frames     int64   `json:"frames"`
	EncodeFPS  float64 `json:"encode_fps"`
}

func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: 5 * time.Minute}, PollInterval: 3 * time.Second, PollTimeout: 2 * time.Hour}
}

func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read remote master credentials: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "VELOX_M2M_SECRET" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("remote master credentials file has no VELOX_M2M_SECRET")
}

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

func (c *Client) ready() error {
	if c == nil || c.HTTP == nil || c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("remote job client is not configured")
	}
	return nil
}

// PrepareJob performs the Master's PREPARE phase and returns the full result
// envelope. It refuses an identity the Master reports as already terminal: a
// canceled preparation must never take a FINALIZE, so the caller gets a typed
// StaleJobError and replaces that identity instead of leasing it again.
func (c *Client) PrepareJob(ctx context.Context, pre map[string]any) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	var prepared jobEnvelope
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/pre", pre, &prepared, http.StatusAccepted); err != nil {
		return Result{}, fmt.Errorf("remote PREPARE: %w", err)
	}
	jobID := strings.TrimSpace(prepared.JobID)
	if jobID == "" {
		jobID = strings.TrimSpace(prepared.ID)
	}
	if jobID == "" {
		return Result{}, fmt.Errorf("remote PREPARE returned no job_id")
	}
	state := unwrap(prepared)
	status := strings.ToUpper(strings.TrimSpace(state.Status))
	if terminalJobStatus(status) {
		return Result{JobID: jobID, Status: status}, &StaleJobError{JobID: jobID, Status: status}
	}
	return Result{JobID: jobID, Status: state.Status}, nil
}

// Prepare performs the Master's PREPARE phase and returns the job it created.
//
// It is split out of Submit so a caller can persist the job id BEFORE waiting
// on it: the id is the only handle to a render that outlives this process, and
// while it lived inside one blocking call a crash mid-render lost the address
// of a job the Master kept running. It is the id-only projection of PrepareJob.
func (c *Client) Prepare(ctx context.Context, pre map[string]any) (string, error) {
	result, err := c.PrepareJob(ctx, pre)
	return result.JobID, err
}

// Finalize performs the Master's FINALIZE phase for an already prepared job.
// Idempotent-by-job-id on the caller's side: the phase names the job, so a
// retried FINALIZE addresses the same Master job instead of creating one.
//
// A Master refusal that names a stale identity (a lease held elsewhere, or a
// job already canceled) is classified as StaleJobError: replaying FINALIZE on
// that id can never succeed, so the caller must replace it rather than retry.
func (c *Client) Finalize(ctx context.Context, jobID string, finalize map[string]any) error {
	if err := c.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("remote FINALIZE requires a job id")
	}
	if err := c.request(ctx, http.MethodPost, "/api/v1/jobs/"+url.PathEscape(jobID)+"/finalize", finalize, nil, http.StatusAccepted); err != nil {
		wrapped := fmt.Errorf("remote FINALIZE job %s: %w", jobID, err)
		if staleJobResponse(wrapped.Error()) {
			return &StaleJobError{JobID: jobID, Status: "LEASE_CONFLICT", Cause: wrapped}
		}
		return wrapped
	}
	return nil
}

// Poll reads the job's state ONCE, which is what makes the wait hand-off-able:
// one call is a decision point (done / failed / still running) instead of a
// loop that owns the caller's goroutine until the render finishes.
//
//   - terminal success -> (result, nil)
//   - terminal failure -> (result, error)
//   - still running    -> (result carrying the RAW status, ErrRemotePending)
func (c *Client) Poll(ctx context.Context, jobID string) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(jobID) == "" {
		return Result{}, fmt.Errorf("remote poll requires a job id")
	}
	var state jobEnvelope
	if err := c.request(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil, &state, http.StatusOK); err != nil {
		return Result{JobID: jobID}, fmt.Errorf("poll remote job %s: %w", jobID, err)
	}
	state = unwrap(state)
	// The Master can leave a preparation job marked RUNNING while a window
	// preparation executor has failed and runtime assets are still pending.
	// That state cannot reach rendering by polling: this identity needs to be
	// replaced so the producer can submit a fresh preparation. Keep the check
	// narrow to the explicit waiting-for-assets phase and a reported error; a
	// transient error on an otherwise progressing render remains observable as
	// RUNNING.
	if strings.EqualFold(strings.TrimSpace(state.Status), "RUNNING") &&
		strings.EqualFold(strings.TrimSpace(state.DispatchStatus), "waiting_runtime_assets") &&
		strings.EqualFold(strings.TrimSpace(state.FinalizationStatus), "WAITING_PREPARATION") &&
		state.RuntimeAssetsPending && strings.TrimSpace(state.Error) != "" {
		return resultFrom(jobID, state), &StaleJobError{
			JobID: jobID, Status: state.Status,
			Cause: fmt.Errorf("master preparation failed while runtime assets remain pending: %s", strings.TrimSpace(state.Error)),
		}
	} // A preparation that FAILED permanently must not be polled forever: the
	// Master reports RUNNING while the error names the definitive cause
	// (r-Kaufmann: WINDOW_EXTRADATA_IDENTITY_MISMATCH). Classifying it as a
	// stale identity ends the wait without a concurrent retry, and the
	// caller rotates the attempt instead of replaying a job that can only
	// reproduce the same rejection. A transient or unrecognised error stays
	// RUNNING, because the same job may still finish.
	if code, permanent := remotePermanentPreparationFailure(state); permanent {
		// A plan/prepare defect is terminal but NOT a stale identity: rotating
		// the attempt would resubmit the same plan and reproduce it exactly.
		return resultFrom(jobID, state), &InvalidPlanError{
			JobID: jobID, Status: state.Status, Code: code,
			Cause: fmt.Errorf("master preparation failed permanently (%s): %s", code, strings.TrimSpace(state.Error)),
		}
	}
	switch strings.ToUpper(strings.TrimSpace(state.Status)) {
	case "SUCCEEDED", "SUCCESS", "COMPLETED":
		state.Status = "SUCCEEDED"
		return resultFrom(jobID, state), nil
	case "FAILED", "CANCELED", "CANCELLED", "REJECTED":
		return resultFrom(jobID, state), fmt.Errorf("remote job %s ended %s: %s", jobID, state.Status, state.Error)
	}
	return resultFrom(jobID, state), fmt.Errorf("remote job %s is still %s: %w", jobID, state.Status, ErrRemotePending)
}

// Attach waits for a job that was ALREADY submitted: the poll phase of the
// split, usable with no PREPARE/FINALIZE and therefore resumable from a job id
// alone.
//
// budget bounds the wait. budget <= 0 waits up to PollTimeout, which is what
// Submit has always done; a positive budget is the caller saying "hold my
// worker for at most this long, then give me the handle back". Expiry is NOT a
// failure: it returns the last observed Result plus ErrRemotePending, so the
// caller decides whether to yield, defer, or poll again.
func (c *Client) Attach(ctx context.Context, jobID string, budget time.Duration) (Result, error) {
	if err := c.ready(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(jobID) == "" {
		return Result{}, fmt.Errorf("remote attach requires a job id")
	}
	wait := budget
	if wait <= 0 {
		wait = c.PollTimeout
	}
	interval := c.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	// The first observation is immediate; the gaps after it start short and back
	// off to the configured interval. A render that ends in seconds must not be
	// observed a whole interval later (that delay lands in poll_lag, not in the
	// render), and a 50 minute one must still not hammer the Master.
	next := interval / 8
	if next < 10*time.Millisecond {
		next = 10 * time.Millisecond
	}
	started := time.Now()
	polls := 0
	var last Result
	finish := func(result Result, err error) (Result, error) {
		result.Timing.Polls = polls
		result.Timing.Wait = nonNegative(time.Since(started))
		if !result.Timing.CompletedAt.IsZero() {
			// Poll lag is local observation minus the Master's own completion
			// time: it is the polling interval and transport, never render work.
			result.Timing.PollLag = nonNegative(time.Since(result.Timing.CompletedAt))
		}
		return result, err
	}
	for {
		result, err := c.Poll(ctx, jobID)
		polls++
		last = result
		if err == nil {
			return finish(result, nil)
		}
		if !errors.Is(err, ErrRemotePending) {
			// Terminal failure or a transport fault: the wait is over either way.
			return finish(result, err)
		}
		// A job that has not been STARTED cannot finish, so a bounded wait must
		// not spend the render budget on it. This is the r9 failure mode: the
		// Master kept the job PENDING for 46 minutes and then cancelled it, and
		// the local run held its worker for the whole time. The handle is still
		// returned, so the next attempt resumes the SAME Master job.
		if budget > 0 && c.QueueTimeout > 0 && result.Timing.StartedAt.IsZero() && time.Since(started) > c.QueueTimeout {
			return finish(last, fmt.Errorf("remote job %s still %s after %s in queue (queue budget): %w",
				jobID, last.Status, c.QueueTimeout, ErrRemotePending))
		}
		// A QUEUED job cannot complete, so a long gap there costs nothing; a
		// STARTED one can finish at any moment, and every second after the start
		// is a second the observation trails the artifact. r75 measured 1,93 s of
		// poll_lag over 11 polls at a 3 s ceiling: bound the gap to one second
		// for the rest of the wait, which is the window where it is measurable.
		next = boundPollGap(next, result.Timing.StartedAt)
		select {
		case <-ctx.Done():
			return finish(last, ctx.Err())
		case <-deadline.C:
			if budget <= 0 {
				return finish(last, fmt.Errorf("remote job %s exceeded poll timeout: %w", jobID, ErrRemotePending))
			}
			return finish(last, fmt.Errorf("remote job %s still %s after %s: %w", jobID, last.Status, wait, ErrRemotePending))
		case <-time.After(next):
		}
		if next *= 2; next > interval {
			next = interval
		}
	}
}

// startedPollInterval bounds the poll gap once the Master reports a STARTED
// job. It is the whole of the observable poll_lag, so it is set from the
// measured cost of trailing a 13 s render by half an interval (r75: 1,93 s),
// not from a request-rate budget: a queued wait keeps the configured ceiling.
const startedPollInterval = time.Second

// boundPollGap applies that ceiling: a job the Master has not started yet keeps
// whatever gap the backoff has reached, and a started one is observed at least
// once per startedPollInterval. A configured interval below the ceiling is left
// alone, so a caller that asked for faster polls still gets them.
func boundPollGap(next time.Duration, startedAt time.Time) time.Duration {
	if startedAt.IsZero() || next <= startedPollInterval {
		return next
	}
	return startedPollInterval
}

// nonNegative clamps a local delta so a Master clock ahead of this host cannot
// produce a negative phase duration.
func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// WaitTerminal waits for a job with the client's full poll timeout.
func (c *Client) WaitTerminal(ctx context.Context, jobID string) (Result, error) {
	return c.Attach(ctx, jobID, 0)
}

// Submit performs the Master's PREPARE → FINALIZE handoff and polls the job.
// It is the composition of Prepare, Finalize and WaitTerminal — the split is
// additive: every existing caller keeps the blocking contract it was written
// against, while a caller that needs to yield uses the phases directly.
func (c *Client) Submit(ctx context.Context, pre, finalize map[string]any) (Result, error) {
	jobID, err := c.Prepare(ctx, pre)
	if err != nil {
		return Result{}, err
	}
	if err := c.Finalize(ctx, jobID, finalize); err != nil {
		return Result{JobID: jobID}, err
	}
	return c.WaitTerminal(ctx, jobID)
}

func (c *Client) request(ctx context.Context, method, path string, body any, out any, wantStatus int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		body := strings.TrimSpace(string(data))
		if planErr := parsePlanValidationError(resp.StatusCode, body); planErr != nil {
			return planErr
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
