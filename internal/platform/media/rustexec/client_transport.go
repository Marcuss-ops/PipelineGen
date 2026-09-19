package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// call is the single Go→Rust execution seam. Every operation (probe,
// normalize, cut, render, mux, ...) flows through here; when an
// ObservedExecutor is attached, the operation is measured exactly once at
// this boundary.
func (c *Client) call(ctx context.Context, req request) (response, error) {
	if req.Version == "" {
		req.Version = ProtocolVersion
	}
	if err := req.Validate(); err != nil {
		cleanupPartFilesRequest(&req)
		return response{}, err
	}
	if c.executor != nil {
		req.FFmpegPath = c.executor.FFmpegPath()
	}
	var result response
	var err error
	if c.observed != nil {
		result, err = c.observed.Execute(ctx, req)
	} else {
		result, err = c.execute(ctx, req)
	}
	if err != nil {
		return response{}, err
	}
	// The request reached the Rust media plane. Account for the subprocess the
	// operation spawns: probe is a single ffprobe invocation, health spawns no
	// media subprocess, and every other operation is an ffmpeg invocation. The
	// copy-only mux (mux_audio_copy) is one ffmpeg with -c:v copy -c:a copy, so
	// frames_decoded/frames_encoded stay 0 on that path.
	switch req.Operation {
	case OperationProbe:
		observability.FFprobeExecCount.Inc()
	case OperationHealth:
		// no media subprocess spawned
	default:
		observability.FFmpegExecCount.Inc()
	}
	if !result.OK {
		cleanupPartFilesRequest(&req)
		return result, fmt.Errorf("rust media %s: %s%s", req.Operation, result.Error, formatItemErrors(result.Items))
	}
	return result, nil
}

// maxReportedItemErrors bounds how many per-item failure reasons are folded
// into the returned error. A large batch must not flood the log; the count of
// suppressed items is reported instead so nothing is silently hidden.
const maxReportedItemErrors = 3

// formatItemErrors renders the per-item failure reasons carried by a Rust
// response, so the caller receives a CAUSE and not just a verdict.
//
// The Rust media executor reports one `items[]` entry per job, each with the
// real ffmpeg/ffprobe stderr in `error`. The top-level `error` is only ever a
// generic summary — for cut_batch it is the literal "all cut jobs failed".
// Dropping the per-item reasons here made that sentence the ONLY
// operator-visible diagnostic for a failed stock cut: the log said the cut
// failed without saying why, and the only way to learn the cause was to
// reproduce the invocation by hand (which then succeeded, because a manual run
// does not share the service's environment).
//
// Returns "" when no item carries a reason, so a response that legitimately
// fails without per-item detail keeps its original message byte-for-byte.
func formatItemErrors(items []cutItem) string {
	if len(items) == 0 {
		return ""
	}
	reasons := make([]string, 0, maxReportedItemErrors)
	failed := 0
	for _, item := range items {
		if item.Status != "failed" && item.Error == "" {
			continue
		}
		failed++
		if len(reasons) >= maxReportedItemErrors {
			continue
		}
		reason := item.Error
		if reason == "" {
			reason = "no reason reported"
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s", item.JobID, reason))
	}
	if failed == 0 {
		return ""
	}
	out := " (failed " + fmt.Sprintf("%d/%d", failed, len(items)) + ": " + strings.Join(reasons, "; ")
	if failed > len(reasons) {
		out += fmt.Sprintf("; +%d more", failed-len(reasons))
	}
	return out + ")"
}

// execute runs one marshaled request through the Rust process runner and
// decodes the response. It is the raw execution step the ObservedExecutor
// decorates.
func (c *Client) execute(ctx context.Context, req request) (response, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return response{}, fmt.Errorf("marshal rust media request: %w", err)
	}
	var stdout, stderr []byte
	if c.runner != nil {
		stdout, stderr, err = c.runner.Run(ctx, "", append(payload, '\n'))
	} else if c.executor != nil {
		reqPayload := append(payload, '\n')
		stdout, stderr, err = c.executor.Run(ctx, reqPayload)
	} else {
		return response{}, fmt.Errorf("rust media executor is not configured")
	}
	if err != nil {
		cleanupPartFiles(payload)
		return response{}, fmt.Errorf("rust media executor: %w: %s", err, stderr)
	}
	var result response
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		cleanupPartFiles(payload)
		return response{}, fmt.Errorf("decode rust media response: %w", err)
	}
	return result, nil
}
