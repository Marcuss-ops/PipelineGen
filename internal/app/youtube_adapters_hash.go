// Package app — sourcing hash adapter
// split from youtube_metadata_adapter.go (PR-GODOBJ-Azione-4, July 2026).
//
// 1 adapter: sourcingHashAdapter.
package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// ── sourcingHashAdapter ───────────────────────────────────────────────

type sourcingHashAdapter struct{}

func (a *sourcingHashAdapter) MD5File(path string) (string, error) {
	return hashutil.MD5File(path)
}

var _ sourcing.HashPort = (*sourcingHashAdapter)(nil)
