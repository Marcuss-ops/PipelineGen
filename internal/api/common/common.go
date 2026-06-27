// Package common — shared internal helpers used across the api/ tree.
//
// Note: this file is a compatibility stub created during
// `fix(build): resolve remaining conflict markers from unmerged mega-commit`.
// Reason: composition.go and youtube_handlers.go (both production imports
// of `internal/api/common`) survived the mega-commit but the directory's
// production files were removed during the scripts/usecase sweep. Without
// this stub, the package compiles to zero symbols and `go vet ./...` fails
// with `build constraints exclude all Go files`. The integration-only
// health_integration_test.go (//go:build integration) is preserved;
// production wiring consumes this stub.
//
// Future cleanup: refactor composition.go + youtube_handlers.go to drop
// the `_ "github.com/Marcuss-ops/PipelineGen/internal/api/common"` import
// path once the cross-feature helpers (transport.EnqueueAsync, etc.)
// are inlined or replaced with direct port calls.
package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OK is a thin transport helper emitting a 200 response with the
// canonical `{"ok": true, ...}` envelope. Production handlers can
// use apiutil.OK in pkg/apiutil; this stub mirrors it for callers
// inside internal/ that historically reached for api/common.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": data})
}

// BindJSON is intentionally absent here — production wiring pulls
// it from pkg/apiutil. Defining it in internal/api/common would
// recreate the layering flip AGENTS.md §Pattern 0 forbids.
