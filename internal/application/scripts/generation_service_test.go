// Package scripts — generation_service_test.go pins the PJ-WITH-IMAGES
// (June 2026) envelope contract: EnqueueWithImages forces the four
// canonical PresetWithImages flags and stamps the JSON payload with
// the versioned envelope (scriptpkg.NewGeneratePayload) so the worker's
// DecodeGeneratePayload recognises the request as preset-driven rather
// than flag-driven.
//
// Without this test the audit-verdict risk (#2 from the previous
// reviewer feedback) silently re-introduces flat-payload emission.
package scripts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// fakeJobEnqueuer is a JobEnqueuer stub that records the last
// EnqueueRequest and returns a canned Job. Sufficient for verifying
// payload shape without spinning up the real jobs broker.
//
// Payload is stored as []byte (raw JSON) so callers can assert the
// wire-shape downstream — the JobEnqueueRequest's Payload field is
// typed as `any` in the domain, so we keep the assertion surface
// close to the wire shape rather than re-encoding for inspection.
type fakeJobEnqueuer struct {
	lastReq *job.EnqueueRequest
	err     error
}

func (f *fakeJobEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &job.Job{ID: "job-test-fixture", Status: job.StatusQueued, Type: req.Type}, nil
}

// payloadBytes returns the captured payload as []byte. The
// JobEnqueueRequest.Payload field is `any`; the production caller
// (jobs.Service) always passes []byte, so a type assertion is the
// right shape here. Returns nil if the test never reached Enqueue.
func (f *fakeJobEnqueuer) payloadBytes(t *testing.T) []byte {
	t.Helper()
	if f.lastReq == nil {
		t.Fatal("expected fakeJobEnqueuer to capture EnqueueRequest, got nil")
	}
	raw, ok := f.lastReq.Payload.([]byte)
	if !ok {
		t.Fatalf("expected lastReq.Payload to be []byte, got %T", f.lastReq.Payload)
	}
	return raw
}

// TestEnqueueWithImages_ForcesPresetSemantics pins that the four
// canonical PresetWithImages flags survive the override in the spec
// the handler forwards (a client might request extract_entities=true
// or generate_scene_images=false; the preset wins).
func TestEnqueueWithImages_ForcesPresetSemantics(t *testing.T) {
	t.Parallel()

	enq := &fakeJobEnqueuer{}
	g := NewGenerationService(enq, nil, nil)

	// Caller pre-populates the spec with contradictory knobs.
	spec := scriptpkg.GenerationSpec{
		Topic:               "kafka observability",
		Language:            "en",
		GenerateSceneImages: false, // Caller says no — preset must override.
		ExtractEntities:     true,  // Caller says yes — preset must override.
		GenerateMetadata:    true,  // Caller says yes — preset must override.
	}

	if _, err := g.EnqueueWithImages(context.Background(), spec); err != nil {
		t.Fatalf("EnqueueWithImages returned error: %v", err)
	}

	if enq.lastReq.Type != job.TypeClipScriptGenerate {
		t.Errorf("expected job type=%q (canonical constant, no literal), got %q",
			job.TypeClipScriptGenerate, enq.lastReq.Type)
	}

	// Decode the envelope the worker would actually see. We assert
	// BOTH that the envelope-shape is used AND the spec mutations
	// reached the encoded payload.
	var envelope scriptpkg.GeneratePayload
	if err := json.Unmarshal(enq.payloadBytes(t), &envelope); err != nil {
		t.Fatalf("payload is not a valid GeneratePayload envelope: %v", err)
	}
	if envelope.Preset != scriptpkg.PresetWithImages {
		t.Errorf("expected preset=%q, got %q (envelope preset is non-canonical)",
			scriptpkg.PresetWithImages, envelope.Preset)
	}
	if envelope.Version == 0 {
		t.Errorf("expected Version > 0 (envelope marker), got 0 — payload is the legacy flat shape")
	}
	// The spec the worker decodes must hold the forced flags.
	if !envelope.Spec.GenerateSceneImages {
		t.Error("expected spec.GenerateSceneImages=true (preset force ON)")
	}
	if !envelope.Spec.GenerateVoiceover {
		t.Error("expected spec.GenerateVoiceover=true (preset force ON)")
	}
	if envelope.Spec.ExtractEntities {
		t.Error("expected spec.ExtractEntities=false (preset force OFF)")
	}
	if envelope.Spec.GenerateMetadata {
		t.Error("expected spec.GenerateMetadata=false (preset force OFF)")
	}
}

// TestEnqueueWithImages_NilEnqueuer_SurfacesError pins that nil JobEnqueuer
// surfaces an explicit error rather than a silent no-op — the audit
// rejected the latter because it hides missing wiring at first integration.
func TestEnqueueWithImages_NilEnqueuer_SurfacesError(t *testing.T) {
	t.Parallel()

	g := NewGenerationService(nil, nil, nil)

	_, err := g.EnqueueWithImages(context.Background(), scriptpkg.GenerationSpec{Topic: "t"})

	if err == nil {
		t.Fatal("expected explicit error for nil JobEnqueuer, got nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("expected error to mention 'not initialized' (missing wiring surface), got: %v", err)
	}
}
