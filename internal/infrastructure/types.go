// Package veloxclient type definitions.
//
// Deprecated: use pkg/veloxclient instead. This file provides type aliases
// so existing importers (including client_test.go) continue to compile.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// AsyncResponse is the enqueue response from async endpoints.
type AsyncResponse = veloxclient.AsyncResponse

// JobStatusResponse mirrors GET /api/jobs/{ID}/full.
type JobStatusResponse = veloxclient.JobStatusResponse

const (
	StatusQueued    = veloxclient.StatusQueued
	StatusRunning   = veloxclient.StatusRunning
	StatusCompleted = veloxclient.StatusCompleted
	StatusFailed    = veloxclient.StatusFailed
	StatusCancelled = veloxclient.StatusCancelled
)

const DefaultMaxAttempts = veloxclient.DefaultMaxAttempts
const DefaultRetryBase = veloxclient.DefaultRetryBase

// Deprecated: use pkg/veloxclient.IsTerminal.
func IsTerminal(status string) bool { return veloxclient.IsTerminal(status) }

var (
	ErrUnauthorized = veloxclient.ErrUnauthorized
	ErrBadRequest   = veloxclient.ErrBadRequest
	ErrServer       = veloxclient.ErrServer
	ErrNotFound     = veloxclient.ErrNotFound
)
