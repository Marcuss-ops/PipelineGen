// Package hashutil contains the canonical infrastructure-layer adapter
// for the YouTube application layer's HashPort.
//
// COMPOSITION-ROOT USE ONLY — application code depends on ports.HashPort
// (declared in internal/application/youtube/ports/hash.go) and never on
// this concrete adapter. This keeps the application→infra seam narrow
// (Pattern 0: port abstraction layer, AGENTS.md June 2026).
//
// The adapter delegates directly to internal/platform/checksum, the
// canonical MD5 SSOT (compat-only — never identity/dedup).
package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
)

// HashAdapter is the concrete HashPort backed by the checksum SSOT.
type HashAdapter struct{}

// NewHashAdapter returns a fresh HashAdapter.
func NewHashAdapter() *HashAdapter { return &HashAdapter{} }

// Compile-time assertion: HashAdapter MUST satisfy ports.HashPort.
// Drift in either interface or concrete signature is caught at compile.
var _ ports.HashPort = (*HashAdapter)(nil)

// MD5String returns the MD5 hex digest of s, delegated to the canonical
// checksum SSOT (compat-only — never identity/dedup).
func (a *HashAdapter) MD5String(s string) string {
	return checksum.LegacyMD5String(s)
}

// MD5File returns the MD5 hex digest of the file at path, delegated to
// the canonical checksum SSOT (streaming, never buffers the whole file).
func (a *HashAdapter) MD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
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
