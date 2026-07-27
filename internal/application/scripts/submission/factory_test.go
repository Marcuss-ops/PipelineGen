package submission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
	scriptdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestJobPolicyResolver_ResolveScriptGenerate(t *testing.T) {
	resolver := NewJobPolicyResolver()
	policy, err := resolver.Resolve(scriptdomain.TypeGenerate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if policy.Priority != 0 {
		t.Fatalf("expected priority 0, got %d", policy.Priority)
	}
	if policy.MaxRetries != 3 {
		t.Fatalf("expected max retries 3, got %d", policy.MaxRetries)
	}
}

func TestJobPolicyResolver_ResolveUnknownReturnsError(t *testing.T) {
	resolver := NewJobPolicyResolver()
	_, err := resolver.Resolve("unknown.job.type")
	if !errors.Is(err, ErrUnknownJobType) {
		t.Fatalf("expected ErrUnknownJobType, got %v", err)
	}
}

func TestSubmitRequestFactory_Build(t *testing.T) {
	factory := NewSubmitRequestFactory()
	env := &scriptdomain.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptdomain.PresetCustom,
		Items: []scriptdomain.GenerationItemV2{
			{
				ID:       "item-1",
				Title:    "Test",
				Language: "en",
				Source: scriptdomain.SourceSpec{
					Type:       scriptdomain.SourceText,
					Topic:      "test topic",
					SourceText: "test source text",
				},
			},
		},
	}

	cmd := GenerateCommand{
		Envelope:       env,
		IdempotencyKey: "idem-key-1",
	}

	req, err := factory.Build(cmd)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if req.Scope != domainops.ScopeScriptGenerate {
		t.Fatalf("expected scope %s, got %s", domainops.ScopeScriptGenerate, req.Scope)
	}
	if req.JobType != scriptdomain.TypeGenerate {
		t.Fatalf("expected job type %s, got %s", scriptdomain.TypeGenerate, req.JobType)
	}
	if req.IdempotencyKey != "idem-key-1" {
		t.Fatalf("expected idempotency key idem-key-1, got %s", req.IdempotencyKey)
	}
	if req.JobPriority != 0 {
		t.Fatalf("expected priority 0, got %d", req.JobPriority)
	}
	if req.JobMaxRetries != 3 {
		t.Fatalf("expected max retries 3, got %d", req.JobMaxRetries)
	}

	// Request hash must cover the complete canonical JSON payload.
	wantPayload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	sum := sha256.Sum256(wantPayload)
	wantHash := hex.EncodeToString(sum[:])
	if req.RequestHash != wantHash {
		t.Fatalf("expected request hash %s, got %s", wantHash, req.RequestHash)
	}

	// JobPayload must be the marshalled envelope.
	if string(req.JobPayload) != string(wantPayload) {
		t.Fatalf("expected job payload %s, got %s", string(wantPayload), string(req.JobPayload))
	}
}

func TestSubmitRequestFactory_Build_ForceRefreshPropagated(t *testing.T) {
	factory := NewSubmitRequestFactory()
	env := &scriptdomain.GenerationEnvelopeV2{
		Version:      2,
		Preset:       scriptdomain.PresetCustom,
		ForceRefresh: true,
		Items: []scriptdomain.GenerationItemV2{
			{
				ID:       "item-1",
				Title:    "Test",
				Language: "en",
				Source: scriptdomain.SourceSpec{
					Type:       scriptdomain.SourceText,
					Topic:      "test topic",
					SourceText: "test source text",
				},
			},
		},
	}

	req, err := factory.Build(GenerateCommand{
		Envelope:       env,
		IdempotencyKey: "idem-force-1",
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if !req.ForceRefresh {
		t.Fatal("expected ForceRefresh to be propagated to SubmitRequest")
	}
}

func TestSubmitRequestFactory_Build_NilEnvelope(t *testing.T) {
	factory := NewSubmitRequestFactory()
	_, err := factory.Build(GenerateCommand{IdempotencyKey: "idem-key"})
	if err == nil {
		t.Fatal("expected error for nil envelope")
	}
}

func TestSubmitRequestFactory_Build_EmptyIdentity(t *testing.T) {
	factory := NewSubmitRequestFactory()
	// An envelope with no items produces an empty identity.
	env := &scriptdomain.GenerationEnvelopeV2{
		Version: 2,
		Preset:  scriptdomain.PresetCustom,
	}
	_, err := factory.Build(GenerateCommand{
		Envelope:       env,
		IdempotencyKey: "idem-key",
	})
	if !errors.Is(err, ErrInvalidEnvelopeIdentity) {
		t.Fatalf("expected ErrInvalidEnvelopeIdentity, got %v", err)
	}
}
