// Package veloxclient provides an HTTP client for PipelineGen.
//
// Deprecated: use pkg/veloxclient instead. This file delegates to the
// canonical implementation so existing importers continue to compile.
package platform

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// Client is an HTTP client for the PipelineGen server.
// Deprecated: use pkg/veloxclient.Client instead.
type Client = veloxclient.Client

// Option configures the Client at construction time.
// Deprecated: use pkg/veloxclient.Option instead.
type Option = veloxclient.Option

// Deprecated: use pkg/veloxclient.WithInsecureTLS.
func WithInsecureTLS() Option { return veloxclient.WithInsecureTLS() }

// Deprecated: use pkg/veloxclient.WithTimeout.
func WithTimeout(d time.Duration) Option { return veloxclient.WithTimeout(d) }

// WithRetryOptions overrides the retry policy.
// Deprecated: use pkg/veloxclient.WithRetryOptions.
func WithRetryOptions(opts retry.Options) Option { return veloxclient.WithRetryOptions(opts) }

// Deprecated: use pkg/veloxclient.New.
func New(baseURL, token string, opts ...Option) *Client {
	return veloxclient.New(baseURL, token, opts...)
}
