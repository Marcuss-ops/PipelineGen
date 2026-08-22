// Package hashutil contains the canonical infrastructure-layer adapter
// for the YouTube application layer's HashPort.
//
// COMPOSITION-ROOT USE ONLY — application code depends on ports.HashPort
// (declared in internal/application/youtube/ports/hash.go) and never on
// this concrete adapter. This keeps the application→infra seam narrow
// (Pattern 0: port abstraction layer, AGENTS.md June 2026).
//
// The adapter delegates to internal/infrastructure/files.MD5File /
// MD5String, which is the canonical Go stdlib MD5 implementation.
package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// HashAdapter is the concrete HashPort backed by the files package.
type HashAdapter struct{}

// NewHashAdapter returns a fresh HashAdapter.
func NewHashAdapter() *HashAdapter { return &HashAdapter{} }

// Compile-time assertion: HashAdapter MUST satisfy ports.HashPort.
// Drift in either interface or concrete signature is caught at compile.
var _ ports.HashPort = (*HashAdapter)(nil)

// MD5String returns the MD5 hex digest of s.
func (a *HashAdapter) MD5String(s string) string {
	return files.MD5String(s)
}

// MD5File returns the MD5 hex digest of the file at path.
func (a *HashAdapter) MD5File(path string) (string, error) {
	return files.MD5File(path)
}

func (a *HashAdapter) SHA256String(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (a *HashAdapter) SHA256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
