// Package script — handler_generate_images_smoke_test.go pins the
// HTTP-level smoke contract for POST /api/script/generate with scene
// images requested in the payload.
//
// The test intentionally stays at the router seam: it verifies the
// endpoint accepts the request, enqueues script.generate, and preserves
// the generate_scene_images flag in the marshaled job payload.
package script

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)
