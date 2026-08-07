// Package main — poll.go
//
// Job lifecycle helpers shared by both worker modes:
//   - submitAndPoll: submit the job + poll until a terminal state
//   - printDryRun:   operator-facing payload preview (-dry-run)
//   - renderResult:  final-output printer (stdout on success, stderr on
//     failure) — the "output verification" step of the worker flow
//   - buildStableReqID: deterministic 32-hex reqID from input parts
//
// The submit+poll loop is identical across script and voiceover modes
// (the only mode-specific inputs are the endpoint + payload), so it
// lives here once instead of being duplicated per runner (the original
// single-file version carried two byte-identical copies of this loop).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	veloxclient "github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// submitAndPoll dispatches the job via SubmitAsync and polls
// GetJobStatus until a terminal state (or ctx cancellation / maxWait).
// Tolerates transient ErrServer (network hiccups mid-poll) by logging +
// looping; only a StatusFailed result is fatal.
//
// On terminal failure the process exits 2 (after renderResult writes the
// failure JSON to stderr) so operator scripts can grep for it.
func submitAndPoll(ctx context.Context, cli *veloxclient.Client, endpoint string, payload map[string]any, reqID string, pollEvery, maxWait time.Duration, verbose bool) {
	// Default 30s submit/poll timeout is fine — the endpoints typically
	// return within a few hundred ms even on the idempotency-hit branch
	// (existing job rows read fast).
	log.Printf("submitting job (reqID=%s)", reqID)
	resp, err := cli.SubmitAsync(ctx, endpoint, payload, reqID)
	if err != nil {
		// SubmitAsync already wraps ErrUnauthorized/ErrBadRequest/ErrServer
		// with credential-redacted bodies — surface directly to operator.
		log.Fatalf("submit failed: %v", err)
	}
	log.Printf("job accepted: job_id=%s status=%s", resp.JobID, resp.Status)

	deadline := time.Now().Add(maxWait)
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			log.Fatalf("interrupted before terminal state (job %s still running on server): %v", resp.JobID, ctx.Err())
		case <-time.After(pollEvery):
		}
		if time.Now().After(deadline) {
			log.Fatalf("timed out after %s waiting for job %s", maxWait, resp.JobID)
		}

		st, err := cli.GetJobStatus(ctx, resp.JobID)
		if err != nil {
			if errors.Is(err, veloxclient.ErrNotFound) {
				// Job created seconds ago and already 404 — would be a bug.
				log.Fatalf("job %s vanished (404): %v", resp.JobID, err)
			}
			log.Printf("poll transient error (will retry): %v", err)
			continue
		}
		if verbose || st.Status != lastStatus {
			log.Printf("poll: id=%s status=%s progress=%d%%", st.ID, st.Status, st.Progress)
			lastStatus = st.Status
		}
		if veloxclient.IsTerminal(st.Status) {
			renderResult(resp.JobID, st)
			if st.Status == veloxclient.StatusFailed {
				os.Exit(2)
			}
			return
		}
	}
}

// printDryRun prints the endpoint + deterministic reqID + payload as
// pretty JSON to stdout (operator inspection) without contacting the
// server. Shared by both mode runners for the -dry-run flag.
func printDryRun(endpoint, baseURL, reqID string, payload map[string]any) {
	out := map[string]any{
		"url":          strings.TrimRight(baseURL, "/") + endpoint,
		"x_request_id": reqID,
		"payload":      payload,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	log.Printf("(dry-run: nothing was submitted; rerun without -dry-run to dispatch)")
}

// renderResult pretty-prints the final job status. Success → stdout, fail
// → stderr + non-zero exit. The Result field is the pipelinegen-defined
// payload map (script, scenes, voiceover paths, etc.).
func renderResult(jobID string, st *veloxclient.JobStatusResponse) {
	log.Printf("job %s reached terminal status=%s progress=%d%%",
		jobID, st.Status, st.Progress)
	if st.Status == veloxclient.StatusFailed {
		// Failure — likely has an `error_message` / `last_error` key in
		// Result. Surface on stderr so scripts can grep.
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"job_id": jobID,
			"failed": true,
			"detail": st.Result,
		})
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(st)
}

// buildStableReqID returns a 32-char hex reqID derived from the inputs.
// The caller MUST keep inputs stable across retries; the server enforces
// (type, correlation_id) UNIQUE and returns the existing job on collision
// — the network only sees a single job_id no matter how many times the
// worker process is restarted with the same arguments.
func buildStableReqID(parts ...string) string {
	canonical := strings.Join(parts, "|")
	return hashutil.MD5String(canonical) // 32 hex chars; server accepts up to 64
}
