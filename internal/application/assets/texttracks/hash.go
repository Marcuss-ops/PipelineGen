// Package texttracks — hash.go: hash helper used by
// ComputeSourceTextHash in policy.go.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
package texttracks

import (
	"crypto/sha256"
	"encoding/hex"
)

func hashSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
