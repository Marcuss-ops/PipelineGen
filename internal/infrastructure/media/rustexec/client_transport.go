package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
