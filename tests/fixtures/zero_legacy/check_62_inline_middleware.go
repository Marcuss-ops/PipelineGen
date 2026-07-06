//go:build ignore

// Package zero_legacy — Check 62 self-check fixture
// (architecture/current.yaml#AUDIT-RESIDUE-2026-07-04.linked_issues[PR-CHECK-58-INLINE-MIDDLEWARE]).
//
// This fixture INTENTIONALLY contains all 4 inline-middleware signatures
// (RequireAdminToken | extractHeaderToken | EnableAuth | AdminTokenProvider)
// so the `bash scripts/ci-architectural-checks.sh --self-check` mode runs
// each regex pattern against this fixture and verifies the regex catches
// the forbidden pattern. The file is intentionally SMALL (well below the
// 300-LoC threshold) so the size condition does NOT fire — the fixture's
// job is to validate the FOUR regex anchors, not the LoC check.
//
// Per AGENTS.md Pattern 0 / godlike/07 minimum-blast-radius: a production
// file carrying any of these signatures AND exceeding 300 LoC is an
// extraction candidate (extract to <feature>/middleware_auth.go). This
// fixture simulates the regex-detectable signature without the LoC
// compound; the production-tree gate does the compound check.
package zero_legacy

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminTokenProvider mirrors the canonical script-side interface. Listed
// first so the regex anchors have a deterministic capture site.
type AdminTokenProvider interface {
	EnableAuth() bool
	AdminToken() string
}

// extractHeaderToken mirrors the canonical script-side helper. Lowercase
// identifier so the regex matches via case-sensitive ripgrep.
func extractHeaderToken(c *gin.Context) string { return c.GetHeader("X-Admin-Token") }

// RequireAdminToken mirrors the canonical admin-token middleware ctor.
// Uppercase function name so the regex matches the canonical pattern.
func RequireAdminToken(cfg AdminTokenProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.EnableAuth() {
			c.Status(http.StatusOK)
			c.Next()
			return
		}
		if extractHeaderToken(c) == cfg.AdminToken() {
			c.Next()
			return
		}
		c.AbortWithStatus(http.StatusUnauthorized)
	}
}

// fakeAuthProvider is a fixture-side concrete that satisfies the
// AdminTokenProvider port. The EnableAuth method matches the regex.
type fakeAuthProvider struct{ token string }

func (f *fakeAuthProvider) EnableAuth() bool   { return f.token != "" }
func (f *fakeAuthProvider) AdminToken() string { return f.token }

var _ AdminTokenProvider = (*fakeAuthProvider)(nil)
