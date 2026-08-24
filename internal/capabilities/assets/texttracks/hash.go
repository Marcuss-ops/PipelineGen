// Package texttracks — hash.go: hash helper used by
// ComputeSourceTextHash in policy.go.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func hashSHA256Hex(s string) string {
	sum := digest.SHA256Bytes([]byte(s))
	return sum
}
