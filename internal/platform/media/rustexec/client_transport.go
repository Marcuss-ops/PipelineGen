package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

func (c *Client) call(ctx context.Context, req request) (response, error) {
	if req.Version == "" {
		req.Version = ProtocolVersion
	}
	if err := req.Validate(); err != nil {
		cleanupPartFilesForRequest(req)
		return response{}, err
	}
	if c.executor != nil {
		req.FFmpegPath = c.executor.FFmpegPath()
	}
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
	var result response
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		cleanupPartFiles(payload)
		return response{}, fmt.Errorf("decode rust media response: %w", err)
	}
	if !result.OK {
		cleanupPartFiles(payload)
		return result, fmt.Errorf("rust media %s: %s", req.Operation, result.Error)
	}
	return result, nil
}
