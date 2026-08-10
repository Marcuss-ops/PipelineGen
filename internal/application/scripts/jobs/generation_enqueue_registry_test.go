// Package scripts -- generation_enqueue_registry_test.go pins the
// Issue 4 (June 2026, P1) contract: EnqueueGenerationJob sources
// MaxRetries from registry.DefaultMaxRetries(script.generate)
// instead of the pre-Issue-4 hard-coded 3-retry fallback that the
// JobsService silently applied when the request's MaxRetries was
// zero.
//
// What this test exercises:
//  1. fakeJobEnqueuer captures every Enqueue call's *job.EnqueueRequest
//     so the assertion can read the MaxRetries value the helper set.
//  2. A fresh *appjobs.Registry is composed with script.generate
//     registered at a CUSTOM DefaultMaxRetries value (7) so the
//     assertion is unambiguous: 7 == registry, 3 == legacy fallback.
//  3. Two boundary contracts:
//     a) registry attached, request MaxRetries=0 -> Enqueue gets 7
//     b) registry==nil -> Enqueue gets 0 (delegated to JobsService
//     fallback which now applies the new conditional logic).
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// fakeJobEnqueuer records every EnqueueRequest it receives. The
// returned *job.Job reflects the request's Type + MaxRetries so the
// caller (e.g. EnqueueGenerationJob) sees the round-trip success
// path it normally would walking the real JobsService.Enqueue.
type fakeJobEnqueuer struct {
	lastReq *job.EnqueueRequest
	calls   int
}

func newFakeJobEnqueuer() *fakeJobEnqueuer {
	return &fakeJobEnqueuer{}
}

func (f *fakeJobEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.calls++
	f.lastReq = req
	return &job.Job{
		ID:         "job_fake",
		Type:       req.Type,
		Status:     job.StatusQueued,
		MaxRetries: req.MaxRetries,
	}, nil
}

// newRegistryWithScriptGenerateRetry builds a fresh registry with
// the canonical TypeScriptGenerate entry at the supplied retry
// value. Other types are intentionally absent -- this test isolates
// the script.generate path so the assertion surface is small and
// clear.
func newRegistryWithScriptGenerateRetry(retries int) *appjobs.Registry {
	r := appjobs.NewRegistry()
	_ = r.Register(appjobs.RegistryEntry{
		Completion: appjobs.CompletionDeclaration{
			JobType:              job.TypeScriptGenerate,
			ArtifactOwnership:    appjobs.ArtifactOwnershipNone,
			FinalizationStrategy: appjobs.FinalizationStrategyLegacyComplete,
		},
		Description:       "test fixture for Issue 4 plumbing assertions",
		Timeout:           60 * time.Minute,
		DefaultMaxRetries: retries,
	})
	r.Freeze()
	return r
}

// minimalEnvelope constructs a small but valid GenerationEnvelopeV2
// for the EnqueueGenerationJob happy-path code path. One item with
// a title and source.type=SourceText so the helper's JSON-marshal
// flow can run. The helper itself does not run envelope validation,
// so any non-empty item is sufficient for THIS test.
func minimalEnvelope() domainScript.GenerationEnvelopeV2 {
	return domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items: []domainScript.GenerationItemV2{
			{
				ID:    "item-1",
				Title: "Test Item",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "irrelevant-topic-for-this-test",
					SourceText: "irrelevant-source-text-for-this-test",
				},
			},
		},
	}
}

// TestEnqueueGenerationJob_UsesRegistryMaxRetries is the canonical
// Issue 4 / P1 helper-layer contract pin. When the registry is
// attached AND the caller did not pre-set EnqueueRequest.MaxRetries,
// the helper MUST source the value from
// registry.DefaultMaxRetries(script.generate)=7 (the test fixture
// value) -- NOT from the pre-Issue-4 hard-coded 3.
//
// The test fixture uses 7 instead of the canonical Compose() value
// of 2 to make the assertion unambiguous: any value other than 7
// would surface as a clear contract violation.
func TestEnqueueGenerationJob_UsesRegistryMaxRetries(t *testing.T) {
	registry := newRegistryWithScriptGenerateRetry(7)

	captured := newFakeJobEnqueuer()
	req := NewGenerateEnqueueRequest(minimalEnvelope())

	enqueued, err := EnqueueGenerationJob(
		context.Background(),
		captured,
		req,
		zap.NewNop(),
		registry,
	)
	require.NoError(t, err, "registry-supplied retries should not change error contract")
	require.NotNil(t, enqueued, "enqueued job must be returned")
	require.Equal(t, 1, captured.calls, "EnqueueGenerationJob must call jobsSvc.Enqueue exactly once")

	assert.Equal(t, "script.generate", captured.lastReq.Type,
		"EnqueueRequest.Type must be the canonical script.generate")
	assert.Equal(t, 7, captured.lastReq.MaxRetries,
		"EnqueueRequest.MaxRetries must be sourced from registry.DefaultMaxRetries(script.generate)=7 (Issue 4 / P1 contract)")
	assert.Equal(t, 7, enqueued.MaxRetries,
		"Job.MaxRetries must echo the EnqueueRequest value the helper set")
}

// TestEnqueueGenerationJob_NilRegistryPassesThroughZero pins the
// nil-registry boundary: when registry==nil the helper does NOT
// touch MaxRetries (enqueueReq.MaxRetries stays at the zero default),
// delegating MaxRetries resolution to the JobsService.Enqueue
// fallback. The JobsService fallback itself is now registry-aware
// (Service.hasRegistry -> DefaultMaxRetries OR legacy 3) -- see
// the matching test in registry_wiring_test.go for the service-side
// behaviour.
//
// This preserves pre-Issue-4 test-fixture wiring without breaking
// any caller that doesn't yet pass a registry.
func TestEnqueueGenerationJob_NilRegistryPassesThroughZero(t *testing.T) {
	captured := newFakeJobEnqueuer()
	req := NewGenerateEnqueueRequest(minimalEnvelope())

	enqueued, err := EnqueueGenerationJob(
		context.Background(),
		captured,
		req,
		zap.NewNop(),
		nil, // nil registry: pre-Issue-4 path preserved
	)
	require.NoError(t, err, "nil-registry path must not error")
	require.NotNil(t, enqueued)
	assert.Equal(t, 0, captured.lastReq.MaxRetries,
		"with nil registry, EnqueueRequest.MaxRetries stays at zero so the JobsService fallback can resolve it")
	assert.Equal(t, 0, enqueued.MaxRetries,
		"job echoes the EnqueueRequest value (zero) -- JobsService applies its own fallback downstream")
}

// TestNewGenerateEnqueueRequest_PropagatesCorrelationID — Issue 5 /
// P1 fix-minimo contract pin. The pre-Issue-5 NewGenerateEnqueueRequest
// dropped env.CorrelationID silently (only Envelope was copied). The
// fix propagates a whitespace-trimmed value so EnqueueGenerationJob
// can forward it to the broker without the caller re-threading it.
//
// Two boundary contracts covered:
//  1. Non-empty env.CorrelationID is propagated verbatim (minus
//     whitespace trim).
//  2. Empty / whitespace-only env.CorrelationID stays empty — the
//     downstream corid.FromContext(ctx) fallback (verified in the
//     EnqueueGenerationJob log path) then kicks in at helper-time,
//     keeping the typed-closed GenerateEnqueueRequest contract
//     intact without growing its surface.
func TestNewGenerateEnqueueRequest_PropagatesCorrelationID(t *testing.T) {
	env := domainScript.GenerationEnvelopeV2{
		Version:       2,
		Preset:        domainScript.PresetCustom,
		CorrelationID: "  trace-abc-123  ",
		Items: []domainScript.GenerationItemV2{
			{
				ID:    "item-1",
				Title: "Test Item",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "trace-test-topic",
					SourceText: "trace-test-source",
				},
			},
		},
	}

	req := NewGenerateEnqueueRequest(env)
	require.NotNil(t, req, "NewGenerateEnqueueRequest must not return nil")
	assert.Equal(t, "trace-abc-123", req.CorrelationID,
		"Issue 5 / P1: env.CorrelationID must propagate -- whitespace trimmed -- into GenerateEnqueueRequest.CorrelationID")

	// Empty correlation: stays empty at the helper boundary. The
	// corid.FromContext(ctx) fallback in EnqueueGenerationJob is the
	// canonical recovery path; assert the field itself stays empty
	// here so the typed-closed GenerateEnqueueRequest surface is
	// honest about its inputs.
	envEmpty := domainScript.GenerationEnvelopeV2{
		Version: 2,
		Preset:  domainScript.PresetCustom,
		Items: []domainScript.GenerationItemV2{
			{
				ID:    "item-empty",
				Title: "Empty Trace",
				Source: domainScript.SourceSpec{
					Type:       domainScript.SourceText,
					Topic:      "empty",
					SourceText: "empty",
				},
			},
		},
	}
	reqEmpty := NewGenerateEnqueueRequest(envEmpty)
	assert.Equal(t, "", reqEmpty.CorrelationID,
		"empty env.CorrelationID stays empty at the helper boundary; EnqueueGenerationJob applies the corid fallback")
}
