package script

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	jobpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func (h *HandlerGenerate) bindGenerateEnvelope(
	c *gin.Context,
) (scriptpkg.GenerationEnvelopeV2, bool) {
	var env scriptpkg.GenerationEnvelopeV2
	if err := c.ShouldBindJSON(&env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid payload: " + err.Error(),
		})
		return scriptpkg.GenerationEnvelopeV2{}, false
	}

	if err := h.validator.ValidateEnvelope(&env); err != nil {
		writeGenerateValidationError(c, err)
		return scriptpkg.GenerationEnvelopeV2{}, false
	}
	return env, true
}

func writeGenerateValidationError(c *gin.Context, err error) {
	var pve *scriptpkg.PayloadValidationError
	if errors.As(err, &pve) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": gin.H{
				"code":      pve.Code,
				"message":   pve.Message,
				"stage":     pve.Stage,
				"retryable": pve.Retryable,
				"extra":     pve.Extra,
			},
		})
		return
	}

	c.JSON(mapErrorToHTTP(err), gin.H{
		"ok": false,
		"error": gin.H{
			"code":      "INVALID_PAYLOAD",
			"message":   err.Error(),
			"stage":     "request.validation",
			"retryable": false,
		},
	})
}

func buildGenerateSubmitRequest(
	c *gin.Context,
	env *scriptpkg.GenerationEnvelopeV2,
) (opsapp.SubmitRequest, bool) {
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key header is required",
			"code":  "IDEMPOTENCY_KEY_REQUIRED",
		})
		return opsapp.SubmitRequest{}, false
	}
	if !isValidIdempotencyKey(idempotencyKey) {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Idempotency-Key must be printable ASCII and at most 255 characters",
			"code":  "INVALID_IDEMPOTENCY_KEY",
		})
		return opsapp.SubmitRequest{}, false
	}

	payload, err := json.Marshal(env)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "failed to marshal generation payload",
		})
		return opsapp.SubmitRequest{}, false
	}

	fingerprint := adapters.BuildEnvelopeIdentity(env)
	if fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "invalid generation payload identity",
			"code":  "INVALID_PAYLOAD",
		})
		return opsapp.SubmitRequest{}, false
	}
	sum := sha256.Sum256([]byte(fingerprint))

	return opsapp.SubmitRequest{
		Scope:          domainops.ScopeScriptGenerate,
		IdempotencyKey: idempotencyKey,
		RequestHash:    hex.EncodeToString(sum[:]),
		ForceRefresh:   env.ForceRefresh,
		JobType:        jobpkg.TypeScriptGenerate,
		JobPayload:     payload,
		JobPriority:    0,
		JobMaxRetries:  3,
	}, true
}
