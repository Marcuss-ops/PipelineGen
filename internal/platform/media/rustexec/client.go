// Package rustexec is the single Go adapter for the Rust media execution
// plane. Application code must depend on its existing ports, not this package.
package rustexec

import (
	"context"

	"go.uber.org/zap"
)

type Client struct {
	executor *Executor
	log      *zap.Logger
	// runner is retained as a narrow test seam for protocol tests. Production
	// calls always use the shared Executor below it.
	runner commandRunner
	// observed is the optional single measurement point decorator. When set,
	// every operation flowing through call() is measured exactly once.
	observed *ObservedExecutor
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

// SetObservedExecutor attaches the single measurement point decorator. The
// decorator wraps THIS client (it records every operation flowing through
// call). Nil-safe; nil disables per-operation measurement.
func (c *Client) SetObservedExecutor(observed *ObservedExecutor) {
	if c != nil {
		c.observed = observed
	}
}
