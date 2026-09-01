// cache_prewarm.go — hot-video metadata cache prewarm + port-backed MD5
// hash wrappers + typed-nil port guard helper.
//
// Split out of orchestrator.go in Step 4 so each usecase/ file owns exactly
// one responsibility. The MD5 wrappers live here because the cache-prewarm
// path is the canonical call-site for the hash port (search capability
// caches MD5-keyed entries for hit-tracking); keeping them together makes
// the relationship between cache state and hash choice explicit.
//
// Step 4 also dropped the legacy private→exported forwarders
// (md5File→MD5File, md5String→MD5String) and the package-level fallback
// helpers (legacy fallback helpers) per the user-spec
// "niente alias, niente wrapper". Hash errors now surface as empty-string
// returns with a debug log, which matches the ExtractionCallbacks shape.
package usecase

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/portutil"
)

// PrewarmHotVideoMetadataCache forwards to the search capability service.
// Callers (currently internal/app/lifecycle.go) call
// ytSvc.PrewarmHotVideoMetadataCache(ctx) expecting this method on the
// orchestrator. The actual implementation lives on *Service (PR5 Phase 2
// capability extraction, set inside NewService when deps.SearchRunner is
// wired); this facade restores the dependency surface from the
// app-layer composition root's perspective.
//
// SearchRunner + Log on ServiceDeps are required; both checked in
// NewService before wiring s.search.
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.search == nil {
		return fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.search.PrewarmHotVideoMetadataCache(ctx)
}

// MD5File returns the MD5 hex digest of the file at path via the
// HashPort.
//
// Deprecated: use SHA256File for content identity. MD5File remains only
// for Drive upload receipt compatibility.
//
// PR5 Phase 3 (exported-for-ExtractionCallbacks): the ExtractionCallbacks
// interface declares MD5File(path string) string, so the port's
// (string, error) signature must be normalized to string. Errors are
// logged at debug level and the digest degrades gracefully to "".
//
// Step 4 removed the package-level fallback forwarder; the
// composition root must wire HashSvc (a *HashAdapter or equivalent) so
// the port is non-nil in production. If HashSvc is missing the helper
// surfaces a WARN-level log so operators see the missing dep at first
// observation rather than at runtime crash.
func (s *Service) MD5File(path string) string {
	if isUnavailablePort(s.hashSvc) {
		s.log.Warn("hashSvc not wired; MD5 digest unavailable", zap.String("path", path))
		return ""
	}
	h, err := s.hashSvc.MD5File(path)
	if err != nil {
		s.log.Debug("hashSvc.MD5File errored; returning empty digest",
			zap.String("path", path),
			zap.Error(err))
		return ""
	}
	return h
}

// SHA256File returns the canonical SHA-256 content identity (64 hex chars)
// of the file at path via the HashPort. This is the canonical byte identity
// — two files with identical bytes yield the same digest. Errors are logged
// and return an empty string (graceful degradation).
func (s *Service) SHA256File(path string) string {
	if isUnavailablePort(s.hashSvc) {
		s.log.Warn("hashSvc not wired; SHA-256 digest unavailable", zap.String("path", path))
		return ""
	}
	h, err := s.hashSvc.SHA256File(path)
	if err != nil {
		s.log.Debug("hashSvc.SHA256File errored; returning empty digest",
			zap.String("path", path),
			zap.Error(err))
		return ""
	}
	return h
}

// SHA256String returns the canonical SHA-256 hex digest of s via the HashPort.
func (s *Service) SHA256String(data string) string {
	if isUnavailablePort(s.hashSvc) {
		s.log.Warn("hashSvc not wired; SHA-256 digest unavailable", zap.String("data_len", fmt.Sprintf("%d", len(data))))
		return ""
	}
	return s.hashSvc.SHA256String(data)
}

// MD5String returns the MD5 hex digest of s via the HashPort.
//
// As with MD5File: no fallback chain. Empty string on missing port
// (WARN-level log) preserves the ExtractionCallbacks contract.
func (s *Service) MD5String(data string) string {
	if isUnavailablePort(s.hashSvc) {
		s.log.Warn("hashSvc not wired; MD5 digest unavailable", zap.String("data_len", fmt.Sprintf("%d", len(data))))
		return ""
	}
	return s.hashSvc.MD5String(data)
}

// isUnavailablePort returns true when port is nil (either bare nil or a
// typed-nil interface holding a nil concrete pointer). Use this for
// required ports that MUST be wired at composition time.
//
// Defined here (cache_prewarm.go) because the MD5 wrappers above are the
// heaviest consumers of the guard; same-package sharing makes it
// accessible from any other file in usecase/.
func isUnavailablePort(port any) bool {
	return port == nil || portutil.IsNilPort(port)
}
