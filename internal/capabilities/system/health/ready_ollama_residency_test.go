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
}

// TestReadyOllamaNonResidentModelIsUnhealthy is ELIM-07: reachability alone
// used to report ready while the first job paid a 17-59 s model load.
func TestReadyOllamaNonResidentModelIsUnhealthy(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{model: "gemma4:e4b", resident: false}}
	resp := &HealthResponse{OK: true, Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.False(t, resp.OK, "a configured model that is not resident must degrade readiness")
	require.False(t, resp.Checks["ollama"]["ok"].(bool))
	require.Equal(t, false, resp.Checks["ollama"]["model_resident"])
	require.Contains(t, resp.Checks["ollama"]["error"], "gemma4:e4b is not resident")
}

// TestReadyOllamaResidencyProbeErrorIsUnhealthy keeps a probe failure visible
// instead of silently reporting ready.
func TestReadyOllamaResidencyProbeErrorIsUnhealthy(t *testing.T) {
	ready := &ReadyChecker{ollamaChecker: residencyChecker{model: "gemma4:e4b", err: errors.New("ps timeout")}}
	resp := &HealthResponse{OK: true, Checks: map[string]CheckResult{}}

	ready.runOllamaCheck(context.Background(), resp)

	require.False(t, resp.OK)
	require.Contains(t, resp.Checks["ollama"]["error"], "residency probe failed")
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
}
