// Package youtube — concrete infrastructure adapters for YouTube capabilities.
//
// HashAdapter satisfies the YouTube capabilities' HashServicePort.
// COMPOSITION-ROOT USE ONLY — application code depends on
// ports.HashServicePort (declared in internal/capabilities/youtube/ports/ports.go)
// and never on this concrete adapter.
//
// godlike/06 SSOT (August 2026): SHA-256 delegates to
// internal/kernel/digest. MD5 delegates to internal/platform/checksum.
package youtube

import (
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
)

// HashAdapter is the concrete HashServicePort backed by the checksum SSOT.
type HashAdapter struct{}

// NewHashAdapter returns a fresh HashAdapter.
func NewHashAdapter() *HashAdapter { return &HashAdapter{} }

// Compile-time assertion: HashAdapter MUST satisfy the single YouTube hash
// port. Drift in either interface or concrete signature is caught at compile.
var _ ports.HashServicePort = (*HashAdapter)(nil)

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
	return digest.SHA256String(s)
}

func (a *HashAdapter) SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digest.SHA256Reader(f)
}
