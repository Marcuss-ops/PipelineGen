// Package voiceoversync — moved to internal/application/assets/reconciliation/voiceover (PR 6, June 2026).
// This file provides backward-compatible type aliases.
package voiceoversync

import (
	recvo "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
)

// Service is the voiceover sync service, moved to reconciliation/voiceover.
// Deprecated: use reconciliation/voiceover.Service directly.
type Service = recvo.Service

// Summary aggregates sync results.
// Deprecated: use reconciliation/voiceover.Summary directly.
type Summary = recvo.Summary

// NewService builds a voiceover sync service.
// Deprecated: use reconciliation/voiceover.NewService directly.
var NewService = recvo.NewService
