// registry_codec_wiring_test.go — the C3 replacement for the retired
// typed-adapter completeness guard.
//
// The old guard built *TypedCodecAdapter[T,R] instances in the test and
// asserted round-trips on them. Nothing in production constructed those
// adapters, so the guard pinned a scaffold instead of the real wiring. This
// test walks the production registry instead: every canonical JobDefinition
// must carry BOTH codecs, with the definition's job type and a non-empty
// schema version, and the payload/result bodies must round-trip through the
// codec the dispatcher will actually call.
package jobs

import (
	"encoding/json"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestCanonicalDefinitionsCarryTheRegistryCodecWiring(t *testing.T) {
	registry := job.NewMutableJobRegistry()
	for _, def := range job.CanonicalJobDefinitions {
		if err := registry.RegisterDefinition(def); err != nil {
			t.Fatalf("register canonical definition %q: %v", def.Type, err)
		}
	}
	compiled, err := registry.Freeze()
	if err != nil {
		t.Fatalf("freeze registry: %v", err)
	}

	definitions := compiled.AllDefinitions()
	if len(definitions) != len(job.CanonicalJobDefinitions) {
		t.Fatalf("registry holds %d definitions, want %d", len(definitions), len(job.CanonicalJobDefinitions))
	}

	for _, def := range definitions {
		def := def
		t.Run(def.Type, func(t *testing.T) {
			if _, ok := compiled.Definition(def.Type); !ok {
				t.Fatalf("definition %q is missing from the frozen registry", def.Type)
			}
			if def.PayloadCodec == nil {
				t.Fatalf("definition %q has no PayloadCodec: the dispatcher cannot decode its payload", def.Type)
			}
			if def.ResultCodec == nil {
				t.Fatalf("definition %q has no ResultCodec: the sender cannot encode its result", def.Type)
			}
			if got := def.PayloadCodec.JobType(); got != def.Type {
				t.Errorf("PayloadCodec.JobType() = %q, want %q", got, def.Type)
			}
			if got := def.ResultCodec.JobType(); got != def.Type {
				t.Errorf("ResultCodec.JobType() = %q, want %q", got, def.Type)
			}
			if got := def.PayloadCodec.SchemaVersion(); got == "" {
				t.Errorf("PayloadCodec.SchemaVersion() is empty for %q", def.Type)
			}
			if got := def.ResultCodec.SchemaVersion(); got == "" {
				t.Errorf("ResultCodec.SchemaVersion() is empty for %q", def.Type)
			}

			payload := map[string]any{"job_type": def.Type, "probe": "c3-wiring"}
			rawPayload, err := def.PayloadCodec.EncodePayload(payload)
			if err != nil {
				t.Fatalf("encode payload: %v", err)
			}
			decodedPayload, err := def.PayloadCodec.DecodePayload(rawPayload)
			if err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if _, ok := decodedPayload.(map[string]any); !ok {
				t.Fatalf("decoded payload is %T, want map[string]any", decodedPayload)
			}

			rawResult, err := def.ResultCodec.EncodeResult(payload)
			if err != nil {
				t.Fatalf("encode result: %v", err)
			}
			decodedResult, err := def.ResultCodec.DecodeResult(rawResult)
			if err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if _, ok := decodedResult.(map[string]any); !ok {
				t.Fatalf("decoded result is %T, want map[string]any", decodedResult)
			}

			// The wire shape must be valid JSON, not just round-trippable.
			var probe map[string]any
			if err := json.Unmarshal(rawPayload, &probe); err != nil {
				t.Fatalf("encoded payload is not JSON: %v", err)
			}
		})
	}
}
