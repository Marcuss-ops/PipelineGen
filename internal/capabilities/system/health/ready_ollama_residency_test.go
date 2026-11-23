package system

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type residencyChecker struct {
	model    string
	resident bool
	err      error
}

func (residencyChecker) CheckOllama(context.Context) error { return nil }

func (c residencyChecker) ModelResidency(context.Context) (string, bool, error) {
	return c.model, c.resident, c.err
}

// TestReadyOllamaResidentModelIsHealthy is the steady state: reachable and the
// configured model loaded with the production runner context.
func TestReadyOllamaResidentModelIsHealthy(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{model: "gemma4:e4b", resident: true}}
	resp := &HealthResponse{OK: true, Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.True(t, resp.OK)
	require.True(t, resp.Checks["ollama"]["ok"].(bool))
	require.Equal(t, "gemma4:e4b", resp.Checks["ollama"]["model"])
	require.Equal(t, true, resp.Checks["ollama"]["model_resident"])
	require.Equal(t, true, resp.Checks["ollama"]["warm"])
	require.NotNil(t, resp.Warm)
	require.True(t, *resp.Warm)
}

// TestReadyOllamaNonResidentModelIsReadyButCold separates warmth from health:
// reachable + configured model not resident is a warm-up fact (warm:false), not
// an outage. Treating it as unhealthy blocked jobs that never call Ollama —
// 2026-11-23: the local /ready was 503 on a cold gemma4:e2b while the master was
// healthy and a clip-compilation payload sat unsent.
func TestReadyOllamaNonResidentModelIsReadyButCold(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{model: "gemma4:e4b", resident: false}}
	resp := &HealthResponse{OK: true, Status: "ready", Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.True(t, resp.OK, "a cold model must not take the service out of readiness")
	require.Equal(t, "ready", resp.Status)
	require.True(t, resp.Checks["ollama"]["ok"].(bool))
	require.Equal(t, false, resp.Checks["ollama"]["model_resident"])
	require.Equal(t, false, resp.Checks["ollama"]["warm"])
	require.Contains(t, resp.Checks["ollama"]["note"], "gemma4:e4b is not resident")
	require.NotContains(t, resp.Checks["ollama"], "error")
	require.NotNil(t, resp.Warm)
	require.False(t, *resp.Warm)
}

// TestReadyOllamaResidencyProbeErrorIsUnhealthy keeps a probe failure visible
// instead of silently reporting ready.
func TestReadyOllamaResidencyProbeErrorIsUnhealthy(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{model: "gemma4:e4b", err: errors.New("ps timeout")}}
	resp := &HealthResponse{OK: true, Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.False(t, resp.OK)
	require.Contains(t, resp.Checks["ollama"]["error"], "residency probe failed")
	require.Nil(t, resp.Warm, "an unverifiable model reports neither warm nor cold")
}

// TestReadyOllamaNoConfiguredModelStaysApplicableButCannotDegrade: an empty
// model means "no model configured", never a false negative.
func TestReadyOllamaNoConfiguredModelStaysApplicableButCannotDegrade(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{}}
	resp := &HealthResponse{OK: true, Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.True(t, resp.OK)
	require.True(t, resp.Checks["ollama"]["ok"].(bool))
	require.NotContains(t, resp.Checks["ollama"], "error")
	require.Nil(t, resp.Warm, "no configured model means nothing to be warm about")
}
