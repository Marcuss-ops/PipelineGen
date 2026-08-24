// Package jobbrokerclient provides an HTTP client implementation of the
// appjobs.Broker interface for remote workers.
package jobbrokerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/platform/remote/shared"
)

// Client is an HTTP implementation of appjobs.Broker for remote workers.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a new broker client.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ----- Path constants (single source of truth in shared package) -----------
//
// All paths derive from `remoteshared.InternalPathPrefix` so the
// client and the server's `/internal/v1` router group cannot drift.
// Updating the prefix in one place but not the other surfaces as 404s
// with no breadcrumb — keep them synchronized.

const (
	pathRegisterWorker           = remoteshared.InternalPathPrefix + "/workers/register"
	pathHeartbeat                = remoteshared.InternalPathPrefix + "/workers/heartbeat"
	pathClaim                    = remoteshared.InternalPathPrefix + "/jobs/claim"
	pathRenewFmt                 = remoteshared.InternalPathPrefix + "/jobs/%s/renew"
	pathProgressFmt              = remoteshared.InternalPathPrefix + "/jobs/%s/progress"
	pathCompleteFmt              = remoteshared.InternalPathPrefix + "/jobs/%s/complete"
	pathCompleteWithArtifactsFmt = remoteshared.InternalPathPrefix + "/jobs/%s/complete-with-artifacts"
	pathFailFmt                  = remoteshared.InternalPathPrefix + "/jobs/%s/fail"
	pathIsCancelledFmt           = remoteshared.InternalPathPrefix + "/jobs/%s/cancelled"

	// ── P0 Commit 6 (July 2026): artifact-upload protocol commands ─────
	// The 3-command handshake (prepare → file content → finalize) is the
	// Creator-side surface for the canonical ArtifactUploader port
	// (domain/remote.ArtifactUploader). Path format mirrors the
	// existing job-broker pattern (jobs/<jobID>/...) with the upload
	// sub-resource embedded under /uploads/<sessionID>/.
	pathUploadPrepareFmt  = remoteshared.InternalPathPrefix + "/jobs/%s/uploads/prepare"
	pathUploadFileFmt     = remoteshared.InternalPathPrefix + "/jobs/%s/uploads/%s/file"
	pathUploadFinalizeFmt = remoteshared.InternalPathPrefix + "/jobs/%s/uploads/%s/finalize"
)

// ── P0 Commit 6: typed request/response DTOs for upload-protocol commands ─

// UploadPrepareRequest is the typed body for the prepare command.
// Mirrors the UploadSession envelope that the remote-side returns
// in the response, but the request shape carries only what the
// Creator-side knows at prepare-time (no leaseId, no state).
type UploadPrepareRequest struct {
	ArtifactID     string `json:"artifact_id"`
	ArtifactKind   string `json:"artifact_kind"`
	Filename       string `json:"filename"`
	MIMEType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	IdempotencyKey string `json:"idempotency_key"`
}

// UploadFinalizeRequest is the typed body for the finalize command.
// Carries the SHA256 the remote-side verifies + the IdempotencyKey
// for retry-collapse.
type UploadFinalizeRequest struct {
	SessionID      string `json:"session_id"`
	SHA256         string `json:"sha256"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (c *Client) RegisterWorker(ctx context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	var session appjobs.WorkerSession
	if err := c.post(ctx, pathRegisterWorker, cmd, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *Client) Heartbeat(ctx context.Context, cmd appjobs.HeartbeatCommand) error {
	return c.post(ctx, pathHeartbeat, cmd, nil)
}

func (c *Client) Claim(ctx context.Context, cmd appjobs.ClaimCommand) (*appjobs.Lease, error) {
	var lease appjobs.Lease
	if err := c.post(ctx, pathClaim, cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Renew(ctx context.Context, cmd appjobs.RenewCommand) (*appjobs.Lease, error) {
	var lease appjobs.Lease
	if err := c.post(ctx, fmt.Sprintf(pathRenewFmt, cmd.JobID), cmd, &lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

func (c *Client) Progress(ctx context.Context, cmd appjobs.ProgressCommand) error {
	return c.post(ctx, fmt.Sprintf(pathProgressFmt, cmd.JobID), cmd, nil)
}

func (c *Client) Complete(ctx context.Context, cmd appjobs.CompleteCommand) error {
	return c.post(ctx, fmt.Sprintf(pathCompleteFmt, cmd.JobID), cmd, nil)
}

// CompleteWithArtifacts POSTs the typed CompleteWithArtifactsCommand
// to /internal/v1/jobs/:id/complete-with-artifacts. Mirrors the
// canonical Complete implementation (json.Marshal cmd → POST body →
// 200 OK → success; 400+ → typed-error decode).
//
// CRITICAL CONTRACT (godlike/07 typed-error contract): when the
// server-side handler returns the typed-error envelope
//
//	{"kind":"lease_lost","error":"..."}
//
// this method wraps the canonical sentinel via fmt.Errorf(..., %w)
// so upstream callers can use errors.Is(err, appjobs.ErrLeaseLost)
// symmetrically across in-process (*local.Broker) and remote
// (this Client) worker executions.
//
// Forward-pointer: the server-side handler at
// internal/api/jobs/handler_workers.go::CompleteWithArtifacts emits
// a generic 500 today; the typed-error envelope emission lands in
// a follow-up PR (godlike/06 SSOT discipline: one owner per fact).
// This client-side decode is already forward-compatible — once
// the server emits the envelope, existing calls produce typed
// errors without further migration.
//
// Wire shape: serialises cmd (json.RawMessage bodies carried
// byte-stable through the wire) into the POST body and decodes
// the 200 response into the typed CWA response envelope (forward-
// declared fields; expected response shape mirrors
// internal/api/jobs.CompleteArtifactsResponse).
func (c *Client) CompleteWithArtifacts(ctx context.Context, cmd appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	bodyBytes, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("complete-with-artifacts: marshal: %w", err)
	}
	url := c.baseURL + fmt.Sprintf(pathCompleteWithArtifactsFmt, cmd.JobID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("complete-with-artifacts: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("complete-with-artifacts: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		rawBody, _ := io.ReadAll(resp.Body)
		// Typed-error envelope decode (P1 #15, July 2026).
		// Builds a *remote.RemoteCompletionError from the canonical
		// wire envelope so callers can probe either via errors.As
		// (structured Kind-by-Kind) or errors.Is against the
		// canonical closed-set sentinel (canonical sentinel-of-
		// sentinels in domain/remote/cerrors.go). The wire envelope
		// is emitted by server-side MapErrorToHTTP on the
		// /complete-with-artifacts path; this decode is the canonical
		// client-side reconstruction per godlike/06 SSOT.
		if remErr, ok := decodeCompletionErrorEnvelope(rawBody); ok {
			return nil, remErr
		}
		return nil, fmt.Errorf("complete-with-artifacts: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
	}

	// AZIONE 5 (July 2026): decode the CompleteArtifactsResponse from
	// the 200 OK body and extract canonical AssetIDs so the caller
	// can populate the HTTP-out DTO wire field.
	var cwaResp struct {
		JobID    string   `json:"job_id"`
		Status   string   `json:"status"`
		AssetIDs []string `json:"asset_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cwaResp); err != nil {
		return nil, fmt.Errorf("complete-with-artifacts: decode response: %w", err)
	}
	return cwaResp.AssetIDs, nil
}

func (c *Client) Fail(ctx context.Context, cmd appjobs.FailCommand) error {
	return c.post(ctx, fmt.Sprintf(pathFailFmt, cmd.JobID), cmd, nil)
}

func (c *Client) IsCancelled(ctx context.Context, jobID, leaseID string) (bool, error) {
	url := fmt.Sprintf("%s%s?lease_id=%s", c.baseURL, fmt.Sprintf(pathIsCancelledFmt, jobID), leaseID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("is-cancelled: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, err
	}
	return body.Cancelled, nil
}

func (c *Client) post(ctx context.Context, path string, reqBody any, respBody any) error {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// ── P0 Commit 6 (July 2026): artifact-upload protocol commands ───────
//
// The 3-method handshake (PrepareArtifactUpload / UploadArtifactFile /
// FinalizeArtifactUpload) is the Creator-side HTTP transport surface
// for the canonical ArtifactUploader port (internal/domain/remote/
// artifact_uploader.go). Command shape mirrors the existing claim /
// complete / fail pattern on *Client:
//
//   PrepareArtifactUpload(ctx, prepareCtx) -> POST /jobs/<jobID>/uploads/prepare
//                                              body: UploadPrepareRequest
//                                              resp: *remote.UploadSession
//   UploadArtifactFile(ctx, sessionID, localPath, idemKey)
//                                          -> POST /jobs/<jobID>/uploads/<sid>/file?filename=<url-escaped>
//                                              body: raw file bytes
//                                              headers: X-Filename + X-Idempotency-Key
//                                              resp: *remote.UploadSession
//   FinalizeArtifactUpload(ctx, sessionID, sha256Hex, idemKey)
//                                          -> POST /jobs/<jobID>/uploads/<sid>/finalize
//                                              body: UploadFinalizeRequest
//                                              resp: *remote.UploadSession
//
// The Creator-side adapter (internal/infrastructure/remote/creator/adapter.go)
// composes these 3 commands with state-machine transition gates +
// idempotency-key derivation. The compile-time assertion pinning
// *Client to the creator/jobBrokerClient interface lives in the
// Adapter, NOT here — fail-closed at the Adapter composition site.

// PrepareArtifactUpload — POST the prepare command. Sends the typed
// UploadPrepareRequest body and decodes the response into a typed
// UploadSession envelope. The server-side is expected to return
// StateUploadPreparing (the Creator adapter's transition gate
// enforces this client-side).
//
// P1 #10 hardening (formerly C6 NIT-1): the HTTP request is bound
// to prepareCtx.Ctx (the ambient job-ctx), NOT context.Background().
// When the worker is cancelled, the lease is lost, or shutdown is
// in progress, prepareCtx.Ctx is already cancelled and net/http
// aborts the in-flight request. Silent-degrade to a non-cancellable
// background context would let the upload continue running after
// the worker has decided to stop (godlike/07 no-fake-availability).
//
// godlike/07 fail-closed: when prepareCtx.Ctx is nil, reject before
// building the HTTP request — a nil Ctx would silently behave like
// context.Background() and reintroduce the exact bug P1 #10 fixed.
func (c *Client) PrepareArtifactUpload(prepareCtx remote.PrepareContext) (*remote.UploadSession, error) {
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("prepare-artifact-upload: %w", remote.ErrArtifactCtxRequired)
	}
	if prepareCtx.JobID == "" {
		return nil, fmt.Errorf("prepare-artifact-upload: prepareCtx.JobID required (godlike/07 no-fake-availability)")
	}
	reqBody := UploadPrepareRequest{
		ArtifactID:     prepareCtx.ArtifactID,
		ArtifactKind:   prepareCtx.ArtifactKind,
		Filename:       prepareCtx.Filename,
		MIMEType:       prepareCtx.MIMEType,
		SizeBytes:      prepareCtx.SizeBytes,
		SHA256:         prepareCtx.SHA256,
		IdempotencyKey: prepareCtx.IdempotencyKey,
	}
	url := c.baseURL + fmt.Sprintf(pathUploadPrepareFmt, prepareCtx.JobID)
	var session remote.UploadSession
	if err := c.postJSON(prepareCtx.Ctx, url, reqBody, &session); err != nil {
		return nil, fmt.Errorf("prepare-artifact-upload: %w", err)
	}
	return &session, nil
}

// UploadArtifactFile — POST raw file bytes to the session URL.
//
// Mirrors assettransferclient.Client.UploadFile streaming pattern: opens
// the local file via os.Open, posts the io.Reader as the request body
// (no full-file materialisation), threads X-Filename + X-Idempotency-
// Key headers. Percent-escapes the filename for URL-safety so names
// with spaces / ampersands / hash fragments / non-ASCII survive the
// ?filename= query parameter without 400-ing the request.
//
// P1 #10 hardening (formerly C6 NIT-1): the HTTP request is bound
// to prepareCtx.Ctx (the ambient job-ctx) via
// http.NewRequestWithContext, NOT context.Background(). For an
// upload-file command this is critical because the file stream
// can be many MB; without the binding, a worker shutdown drain or
// lease-loss would let the upload continue to completion silently
// even after the worker has decided to stop.
//
// godlike/07 fail-closed: prepareCtx.Ctx nil → reject.
func (c *Client) UploadArtifactFile(prepareCtx remote.PrepareContext, sessionID, localPath, idempotencyKey string) (*remote.UploadSession, error) {
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("upload-artifact-file: %w", remote.ErrArtifactCtxRequired)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("upload-artifact-file: sessionID required (godlike/07 no-fake-availability)")
	}
	if localPath == "" {
		return nil, fmt.Errorf("upload-artifact-file: localPath required (godlike/07 no-fake-availability)")
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("upload-artifact-file: open %s: %w", localPath, err)
	}
	defer f.Close()

	filename := path.Base(localPath)
	// SECURITY: filename is interpolated into the URL query string.
	// Without url.QueryEscape, filenames like "café voiceover.mp3"
	// produce a malformed URL (raw UTF-8 bytes); names like
	// "a&b.mp3" introduce a fake second query parameter; names like
	// "a#b.mp3" are truncated to "a" by fragment parsing. All three
	// silently break the upload pipeline. percent-escape canonical
	// via the stdlib url.QueryEscape (matches assettransferclient pattern).
	safeFilename := url.QueryEscape(filename)
	contentURL := c.baseURL + fmt.Sprintf(pathUploadFileFmt, prepareCtx.JobID, sessionID) + "?filename=" + safeFilename

	// P1 #10: bind the HTTP request to prepareCtx.Ctx (was
	// context.Background before this commit; see the type-level
	// godlike/07 contract). Worker cancellation / lease-loss /
	// shutdown drain surfaces as a transport error here, NOT a
	// silent success.
	req, err := http.NewRequestWithContext(prepareCtx.Ctx, "POST", contentURL, f)
	if err != nil {
		return nil, fmt.Errorf("upload-artifact-file: create request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("X-Filename", filename)
	if idempotencyKey != "" {
		req.Header.Set("X-Idempotency-Key", idempotencyKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload-artifact-file: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload-artifact-file: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var session remote.UploadSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("upload-artifact-file: decode response: %w", err)
	}
	return &session, nil
}

// FinalizeArtifactUpload — POST the finalize command. Sends the typed
// UploadFinalizeRequest body (carrying SHA256 + IdempotencyKey) and
// decodes the response into a typed UploadSession envelope. The
// server-side atomically verifies + transitions to VERIFIED + FINALIZED
// in one call; the Creator adapter's transition gate handles the
// intermediate VERIFIED hop.
//
// P1 #10 hardening (formerly C6 NIT-1): the HTTP request is bound
// to prepareCtx.Ctx (the ambient job-ctx), NOT context.Background().
// godlike/07 fail-closed: prepareCtx.Ctx nil → reject.
func (c *Client) FinalizeArtifactUpload(prepareCtx remote.PrepareContext, sessionID, sha256Hex, idempotencyKey string) (*remote.UploadSession, error) {
	if prepareCtx.Ctx == nil {
		return nil, fmt.Errorf("finalize-artifact-upload: %w", remote.ErrArtifactCtxRequired)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("finalize-artifact-upload: sessionID required (godlike/07 no-fake-availability)")
	}
	reqBody := UploadFinalizeRequest{
		SessionID:      sessionID,
		SHA256:         sha256Hex,
		IdempotencyKey: idempotencyKey,
	}
	url := c.baseURL + fmt.Sprintf(pathUploadFinalizeFmt, prepareCtx.JobID, sessionID)
	var session remote.UploadSession
	if err := c.postJSON(prepareCtx.Ctx, url, reqBody, &session); err != nil {
		return nil, fmt.Errorf("finalize-artifact-upload: %w", err)
	}
	return &session, nil
}

// postJSON is a helper that marshals a JSON body, POSTs it, and
// decodes the response. Kept separate from post() so the upload-file
// command (which streams raw bytes via os.Open) can use a custom
// request body without the JSON marshaling layer.
//
// Returns the path-prefixed error so callers can wrap with fmt.Errorf
// and preserve the underlying transport error via %w.
// Compile-time pin (godlike/06 SSOT discipline): *Client MUST
// satisfy the narrow typed CompletionPort for the artifact-
// completion wire surface. Drift in either the 1-method port
// signature (in appjobs.CompletionPort) or the Client method
// signature (this file) is a build failure rather than a
// runtime panic.
var _ appjobs.CompletionPort = (*Client)(nil)

func (c *Client) postJSON(ctx context.Context, url string, reqBody any, respBody any) error {
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
