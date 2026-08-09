// Package rustexec is the single Go adapter for the Rust media execution
// plane. Application code must depend on its existing ports, not this package.
package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

type Client struct {
	executor *Executor
	log      *zap.Logger
	// runner is retained as a narrow test seam for protocol tests. Production
	// calls always use the shared Executor below it.
	runner commandRunner
}

type commandRunner interface {
	Run(context.Context, string, []byte) ([]byte, []byte, error)
}

func NewClient(binaryPath, ffmpegPath string, log *zap.Logger) *Client {
	return NewClientWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), log)
}

// NewClientWithExecutor binds a protocol client to a shared composition-root
// Executor. All adapters created from this client then share its limiter and
// process lifecycle policy.
func NewClientWithExecutor(executor *Executor, log *zap.Logger) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	return &Client{executor: executor, log: log}
}

func (c *Client) call(ctx context.Context, req request) (response, error) {
	req.Version = "mediaexec.v1"
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
		return response{}, fmt.Errorf("rust media executor: %w: %s", err, stderr)
	}
	var result response
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		return response{}, fmt.Errorf("decode rust media response: %w", err)
	}
	if !result.OK {
		return result, fmt.Errorf("rust media %s: %s", req.Operation, result.Error)
	}
	return result, nil
}

// VideoProcessor implements the infrastructure media processor port through
// the Rust executor. It contains no business policy or persistence logic.
