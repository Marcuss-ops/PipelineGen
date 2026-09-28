// Package scriptgeneration — runner_nlp_pool_test.go certifies that the
// NLP/entity-extraction fan-out is bound by the CONFIGURED concurrency.
//
// Regression guard: the SceneReadyCoordinator pool was pinned to the
// DefaultNLPConcurrency constant, so scripts.nlp_concurrency reached only the
// Ollama gate. An operator lowering the value (to stop extraction competing
// with TTS/LLM inference on the same host) had no effect on the fan-out that
// actually issues the work, and raising it could not use the extra gate
// capacity either. The TTS and translation pools were already configurable,
// which made the asymmetry easy to miss.
package scriptgeneration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestSetNLPConcurrency_ZeroFallsBackToDefault pins the fail-safe: a
// non-positive override restores the certified default, exactly like the TTS
// pool. A zero width would deadlock the fan-out.
func TestSetNLPConcurrency_ZeroFallsBackToDefault(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()

	runner.SetNLPConcurrency(0)
	require.Equal(t, DefaultNLPConcurrency, runner.nlpConcurrency)

	runner.SetNLPConcurrency(6)
	require.Equal(t, 6, runner.nlpConcurrency)
}

// TestSceneReadyCoordinator_PoolsFollowConfiguredConcurrency pins that each of
// the coordinator's own bounded pools follows the runner's configured value
// instead of a constant, so the configured parallelism is authoritative for
// every branch of the SceneTextReady fan-out.
func TestSceneReadyCoordinator_PoolsFollowConfiguredConcurrency(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()
	runner.SetNLPConcurrency(7)
	runner.SetTTSConcurrency(3)
	runner.SetTranslationConcurrency(2)

	coordinator := newSceneReadyCoordinator(context.Background(), runner, "run-nlp-pool",
		defaultTestRequest(), kernelscript.ArtifactRoutingContext{}, ExecutionContext{})

	require.Equal(t, 7, coordinator.nlpSlots.Cap(),
		"extraction pool must follow scripts.nlp_concurrency")
	require.Equal(t, 3, coordinator.ttsSlots.Cap(),
		"TTS pool must follow scripts.tts_concurrency")
	require.Equal(t, 2, coordinator.translationSlots.Cap(),
		"translation pool must follow scripts.translation_concurrency")
}

// TestSceneReadyCoordinator_NLPSlotsUseCertifiedDefaultWhenUnset pins the
// fallback on a default-constructed runner: an untouched runner keeps the
// certified extraction width.
func TestSceneReadyCoordinator_NLPSlotsUseCertifiedDefaultWhenUnset(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()

	coordinator := newSceneReadyCoordinator(context.Background(), runner, "run-nlp-pool-default",
		defaultTestRequest(), kernelscript.ArtifactRoutingContext{}, ExecutionContext{})

	require.Equal(t, DefaultNLPConcurrency, coordinator.nlpSlots.Cap())
}
