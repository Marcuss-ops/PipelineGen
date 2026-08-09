// Package rustexec is the single Go adapter for the Rust media execution
// plane. Application code must depend on its existing ports, not this package.
package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"os/exec"
)

type Client struct {
	binaryPath string
	ffmpegPath string
	log        *zap.Logger
	runner     commandRunner
}

type commandRunner interface {
	Run(context.Context, string, []byte) ([]byte, []byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, binary string, input []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func NewClient(binaryPath, ffmpegPath string, log *zap.Logger) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Client{binaryPath: binaryPath, ffmpegPath: ffmpegPath, log: log, runner: execCommandRunner{}}
}

func (c *Client) call(ctx context.Context, req request) (response, error) {
	req.Version = "mediaexec.v1"
	req.FFmpegPath = c.ffmpegPath
	payload, err := json.Marshal(req)
	if err != nil {
		return response{}, fmt.Errorf("marshal rust media request: %w", err)
	}
	stdout, stderr, err := c.runner.Run(ctx, c.binaryPath, append(payload, '\n'))
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
