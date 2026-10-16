package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrLeaseConflict wraps queue report errors that returned 409: the job is no
// longer owned by the reporting worker (lease expired and requeued, completed,
// or claimed elsewhere). It is a permanent condition — no retry can recover a
// lease held by someone else.
var ErrLeaseConflict = errors.New("queue: lease conflict")

// ClaimedJob is the payload returned to a worker on a successful claim: the
// Job envelope plus the Lease the worker must hold (and renew) while
// rendering.
//
// It is an ALIAS of Job — there is ONE job declaration shared by the queue
// service, the server, the worker adapter and every producer, so the claim
// response can never drift from the submit/GET bodies. Lease is the only field
// a claim adds, and it is omitted everywhere else.
type ClaimedJob = Job

// ClaimFinalization atomically claims a parent for finalization.
func (c *Client) ClaimFinalization(ctx context.Context, parentID, workerID string) (*ClaimedJob, bool, error) {
	body, err := json.Marshal(map[string]string{"worker": workerID})
	if err != nil {
		return nil, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/"+url.PathEscape(parentID)+"/finalize/claim", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("queue finalize claim: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, false, err
	}
	return &job, true, nil
}

// Claim atomically claims the next pending job for workerID. It returns a nil
// job when the queue is empty.
func (c *Client) Claim(ctx context.Context, workerID string) (*ClaimedJob, error) {
	return c.claim(ctx, workerID, "", 0)
}

// ClaimWait long-polls the queue for up to maxWait, returning as soon as a
// job can be claimed. The wake-up signal is event-driven; the atomic claim
// itself is unchanged (SKIP LOCKED / memory store), so no job is ever handed
// to two workers by the wait path. It returns a nil job on timeout.
func (c *Client) ClaimWait(ctx context.Context, workerID string, maxWait time.Duration) (*ClaimedJob, error) {
	return c.claimWait(ctx, workerID, "", maxWait)
}

func (c *Client) claimWait(ctx context.Context, workerID, state string, maxWait time.Duration) (*ClaimedJob, error) {
	if maxWait <= 0 {
		maxWait = 25 * time.Second
	}
	body, err := json.Marshal(map[string]any{
		"worker":      workerID,
		"state":       state,
		"max_wait_ms": maxWait.Milliseconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("queue claim wait marshal: %w", err)
	}
	// The request outlives the long-poll window so the server can respond
	// after its own wait elapses instead of tripping the client timeout.
	reqCtx, cancel := context.WithTimeout(ctx, maxWait+10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/jobs/claim/wait", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("queue claim wait request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue claim wait do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue claim wait: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("queue claim wait decode: %w", err)
	}
	return &job, nil
}

func (c *Client) claim(ctx context.Context, workerID, state string, wait time.Duration) (*ClaimedJob, error) {
	waitMS := wait.Milliseconds()
	if waitMS < 0 {
		waitMS = 0
	}
	body, err := json.Marshal(struct {
		Worker string `json:"worker"`
		State  string `json:"state,omitempty"`
		WaitMS int64  `json:"wait_ms,omitempty"`
	}{Worker: workerID, State: state, WaitMS: waitMS})
	if err != nil {
		return nil, fmt.Errorf("queue claim marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("queue claim request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue claim do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // queue empty after optional wait
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue claim: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var job ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, fmt.Errorf("queue claim decode: %w", err)
	}
	return &job, nil
}

// Complete reports a successfully rendered job along with its artifact.
func (c *Client) Complete(ctx context.Context, id, workerID string, artifact Artifact) error {
	return c.report(ctx, id, workerID, "complete", artifact)
}

// Fail reports a job that could not be rendered.
func (c *Client) Fail(ctx context.Context, id, workerID, reason string) error {
	return c.report(ctx, id, workerID, "fail", map[string]string{"reason": reason})
}

// FailPermanently reports a non-retryable failure, bypassing max_attempts.
func (c *Client) FailPermanently(ctx context.Context, id, workerID, reason string) error {
	return c.report(ctx, id, workerID, "fail-permanent", map[string]string{"reason": reason})
}

// Rendered reports a job whose render completed and was durably stored, but
// whose external publication (Drive) failed. The job stays claimable for a
// publication-only retry.
func (c *Client) Rendered(ctx context.Context, id, workerID, reason string, artifact Artifact) error {
	return c.report(ctx, id, workerID, "rendered", map[string]any{"reason": reason, "artifact": artifact})
}

// Renew extends the lease on a running job owned by workerID. It fails if the
// job expired and was requeued to another worker.
func (c *Client) Renew(ctx context.Context, id, workerID string) error {
	return c.report(ctx, id, workerID, "renew", nil)
}

// Progress is the per-job render progress.
//
// On a worker report only FramesDone/TotalFrames are meaningful; LastFrameAt
// and Worker are filled in by the queue when it stores the snapshot and are
// exposed on GET /jobs/{id} so a stalled render is observable without asking
// the worker. It is the SINGLE progress type: the report payload and the
// persisted/read projection are the same declaration.
type Progress struct {
	FramesDone  int       `json:"frames_done"`
	TotalFrames int       `json:"frames_total,omitempty"`
	LastFrameAt time.Time `json:"last_frame_at,omitzero"`
	Worker      string    `json:"worker,omitempty"`
}

// ReportProgress records render progress for a running job owned by workerID.
// The queue stores it so GET /jobs/{id} exposes frames_done/last_frame_at
// without asking the worker; a report from a worker that no longer owns the
// lease is rejected (409), never applied.
func (c *Client) ReportProgress(ctx context.Context, id, workerID string, p Progress) error {
	return c.report(ctx, id, workerID, "progress", p)
}

// WorkerStatus represents the lifecycle state of a registered worker.
type WorkerStatus string

const (
	WorkerStatusUnknown  WorkerStatus = "unknown"
	WorkerStatusReady    WorkerStatus = "ready"
	WorkerStatusBusy     WorkerStatus = "busy"
	WorkerStatusDraining WorkerStatus = "draining"
	WorkerStatusOffline  WorkerStatus = "offline"
)

// AllWorkerStatuses returns the canonical worker-status vocabulary. It is the
// single list the `rendering_workers_status_check` SQL constraint must agree
// with (pinned by queue/internal/model/state_schema_test.go). The vocabulary
// has the same standing as the job-state and attempt-status sets: without the
// pin, adding a status here and forgetting the migration (or vice versa) only
// fails when a worker tries to register.
func AllWorkerStatuses() []WorkerStatus {
	return []WorkerStatus{
		WorkerStatusUnknown, WorkerStatusReady, WorkerStatusBusy,
		WorkerStatusDraining, WorkerStatusOffline,
	}
}

// WorkerLiveness is the DERIVED liveness of a registered worker, computed from
// how long ago it last heartbeated.
//
// It exists because WorkerStatus is NOT a liveness signal: it is the value the
// worker last REPORTED, and a worker that stops (SIGSTOP, a wedged GPU driver, a
// frozen VM) keeps its last reported status forever. Live observation
// (2026-09-17): the only RenderingGen worker had every one of its sixteen
// threads in state `T` — heartbeat frozen for hours, no job claimable — while
// GET /workers still answered `status: ready`, and GET /workers/health answered
// `ready: 1`. Anything planned from that number (a benchmark, an autoscaling
// decision, a certification preflight) was planned from a worker that could not
// do work.
//
// The classification is a pure function of the heartbeat age (see
// internal/model), so it cannot drift between the repositories, the service and
// the wire. Its bands are fractions of ONE configured number — the queue's
// `-worker-stale-after` (the shipped unit runs `3m` against the worker's 20s
// heartbeat): past a third it is degraded, past two thirds stale, past the
// window dead.
type WorkerLiveness string

const (
	// WorkerLivenessReady — heartbeating inside the first third of the
	// staleness window.
	WorkerLivenessReady WorkerLiveness = "ready"
	// WorkerLivenessDegraded — heartbeat aged past one third of the window but
	// still inside it, so its leases are intact; must not be counted on.
	WorkerLivenessDegraded WorkerLiveness = "degraded"
	// WorkerLivenessStale — aged past two thirds of the window, about to
	// leave it: treat as unreliable.
	WorkerLivenessStale WorkerLiveness = "stale"
	// WorkerLivenessDead — outside the staleness window, or it never
	// heartbeated after registering. This is the state that used to be
	// invisible on /workers.
	WorkerLivenessDead WorkerLiveness = "dead"
)

// AllWorkerLivenesses returns the canonical liveness vocabulary in
// most-alive-first order. It is a DERIVED vocabulary, not a stored column: no
// SQL constraint mirrors it (contrast AllWorkerStatuses), because liveness is a
// function of the heartbeat age, not a value anyone writes.
func AllWorkerLivenesses() []WorkerLiveness {
	return []WorkerLiveness{
		WorkerLivenessReady, WorkerLivenessDegraded,
		WorkerLivenessStale, WorkerLivenessDead,
	}
}

// Worker represents a worker registration payload / health status.
type Worker struct {
	ID                   string       `json:"id"`
	Hostname             string       `json:"hostname,omitempty"`
	Status               WorkerStatus `json:"status"`
	RenderingGenVersion  string       `json:"renderinggen_version,omitempty"`
	ChrononVersion       string       `json:"chronon_version,omitempty"`
	OverlaySchemaVersion int          `json:"overlay_schema_version,omitempty"`
	GPUBackend           string       `json:"gpu_backend,omitempty"`
	GPUDevice            string       `json:"gpu_device,omitempty"`
	GPUDriver            string       `json:"gpu_driver,omitempty"`
	StartedAt            time.Time    `json:"started_at,omitempty"`
	LastHeartbeatAt      time.Time    `json:"last_heartbeat_at,omitempty"`
	// Liveness is DERIVED from LastHeartbeatAt and is therefore filled by the
	// QUEUE, exactly like Progress.LastFrameAt: it is meaningful on the GET
	// /workers projection and is ignored on POST /workers/register (the worker
	// does not know, and must not claim, how alive it looks to the queue).
	Liveness WorkerLiveness `json:"liveness,omitempty"`
}

// WorkerHealth is the aggregate worker health.
//
// Ready/Busy count only workers whose DERIVED liveness is ready and whose
// reported status is ready/busy: a worker that has stopped heartbeating is not
// capacity, however recently it claimed to be ready. Degraded/Stale count the
// workers still inside the staleness window whose heartbeat has visibly aged;
// Offline counts the ones outside it (the state the old health snapshot called
// "offline" and the old /workers page called "ready"). A worker whose liveness
// is ready but whose reported status is draining/unknown is counted in Total
// only, as before.
type WorkerHealth struct {
	Ready    int `json:"ready"`
	Busy     int `json:"busy"`
	Degraded int `json:"degraded"`
	Stale    int `json:"stale"`
	Offline  int `json:"offline"`
	Total    int `json:"total"`
}

// RegisterWorker registers the worker with the central queue.
func (c *Client) RegisterWorker(ctx context.Context, w Worker) error {
	buf, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("queue register worker marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/workers/register", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("queue register worker request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue register worker do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue register worker: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// HeartbeatWorker sends a liveness heartbeat for workerID.
func (c *Client) HeartbeatWorker(ctx context.Context, workerID string) error {
	buf, err := json.Marshal(map[string]string{"worker": workerID})
	if err != nil {
		return fmt.Errorf("queue heartbeat marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/workers/heartbeat", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("queue heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue heartbeat do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue heartbeat: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// ListWorkers returns all currently registered workers.
func (c *Client) ListWorkers(ctx context.Context) ([]Worker, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/workers", nil)
	if err != nil {
		return nil, fmt.Errorf("queue list workers request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("queue list workers do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("queue list workers: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var workers []Worker
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return nil, fmt.Errorf("queue list workers decode: %w", err)
	}
	return workers, nil
}

// WorkerHealth queries the worker health stats.
func (c *Client) WorkerHealth(ctx context.Context) (WorkerHealth, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/workers/health", nil)
	if err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return WorkerHealth{}, fmt.Errorf("queue worker health: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var health WorkerHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return WorkerHealth{}, fmt.Errorf("queue worker health decode: %w", err)
	}
	return health, nil
}

// Retry resets a failed job back to pending state so it can be re-claimed.
func (c *Client) Retry(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/jobs/"+url.PathEscape(id)+"/retry", nil)
	if err != nil {
		return fmt.Errorf("queue retry request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue retry do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("queue retry: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// report POSTs a worker action (complete/fail/renew) to the job endpoint. The
// wire body carries the worker identity plus an optional data payload, matching
// the queue server's handlers.
func (c *Client) report(ctx context.Context, id, workerID, action string, payload any) error {
	body := map[string]any{"worker": workerID}
	if payload != nil {
		body["data"] = payload
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("queue %s marshal: %w", action, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/jobs/"+url.PathEscape(id)+"/"+action, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("queue %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("queue %s do: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("queue %s: HTTP %d: %s", action, resp.StatusCode, strings.TrimSpace(string(raw)))
		// A 409 on renew means the lease is definitively gone (expired and
		// requeued, completed, or owned by another worker). Callers use the
		// sentinel to abort immediately instead of retrying a lost lease.
		if resp.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w: %v", ErrLeaseConflict, err)
		}
		return err
	}
	return nil
}
