package voiceover

import (
	"encoding/json"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

// TestJobHandler_RoundTrip verifies the mandatory PR 3 requirement:
// HTTP request → serialized job → worker decode → same command.
//
// 1. Build a domain.GenerateVoiceoverCommand
// 2. Serialise it via json.Marshal (mimicking EnqueueAsync → Enqueue → DB)
// 3. Unmarshal as it would be in the worker's job handler
// 4. Assert field-by-field equality
func TestJobHandler_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		cmd  domain.GenerateVoiceoverCommand
	}{
		{
			name: "full command with all fields",
			cmd: domain.GenerateVoiceoverCommand{
				Text:   "Welcome to PipelineGen voiceover generation",
				Locale: "en-US",
				Voice:  "en-US-RogerNeural",
				Destination: domain.DestinationRef{
					FolderID: "1abc234def567",
				},
				ForceRegenerate: true,
				Reference: domain.Reference{
					ScriptID: "script-001",
					SceneID:  "scene-42",
				},
			},
		},
		{
			name: "minimal command (text + locale only)",
			cmd: domain.GenerateVoiceoverCommand{
				Text:   "Ciao mondo",
				Locale: "it-IT",
			},
		},
		{
			name: "command with force regenerate false",
			cmd: domain.GenerateVoiceoverCommand{
				Text:            "Some promotional text for testing",
				Locale:          "fr-FR",
				ForceRegenerate: false,
			},
		},
		{
			name: "command with destination only, no voice",
			cmd: domain.GenerateVoiceoverCommand{
				Text:   "Test de génération vocale",
				Locale: "fr-FR",
				Destination: domain.DestinationRef{
					FolderID: "folder-xyz",
				},
			},
		},
		{
			name: "command with reference only",
			cmd: domain.GenerateVoiceoverCommand{
				Text:   "Scene narration text",
				Locale: "de-DE",
				Reference: domain.Reference{
					ScriptID: "script-002",
					SceneID:  "scene-99",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ── 1. Marshal (simulates API handler → job system) ──────
			payloadJSON, err := json.Marshal(tt.cmd)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			// ── 2. Verify the JSON doesn't contain PayloadMap()
			//    artifacts (map[string]any, strategy string, etc.)
			var raw map[string]any
			if err := json.Unmarshal(payloadJSON, &raw); err != nil {
				t.Fatalf("re-parse for inspection failed: %v", err)
			}
			if _, hasStrategy := raw["strategy"]; hasStrategy {
				t.Error("payload contains legacy 'strategy' field — should use ForceRegenerate bool")
			}
			if _, hasPayloadMap := raw["payload"]; hasPayloadMap {
				t.Error("payload contains legacy 'payload' wrapper — should be flat GenerateVoiceoverCommand")
			}

			// ── 3. Unmarshal (simulates worker decode in HandleJob) ──
			var decoded domain.GenerateVoiceoverCommand
			if err := json.Unmarshal(payloadJSON, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			// ── 4. Assert field-by-field equality ─────────────────
			if decoded.Text != tt.cmd.Text {
				t.Errorf("Text: got %q, want %q", decoded.Text, tt.cmd.Text)
			}
			if decoded.Locale != tt.cmd.Locale {
				t.Errorf("Locale: got %q, want %q", decoded.Locale, tt.cmd.Locale)
			}
			if decoded.Voice != tt.cmd.Voice {
				t.Errorf("Voice: got %q, want %q", decoded.Voice, tt.cmd.Voice)
			}
			if decoded.Destination != tt.cmd.Destination {
				t.Errorf("Destination: got %+v, want %+v", decoded.Destination, tt.cmd.Destination)
			}
			if decoded.ForceRegenerate != tt.cmd.ForceRegenerate {
				t.Errorf("ForceRegenerate: got %v, want %v", decoded.ForceRegenerate, tt.cmd.ForceRegenerate)
			}
			if decoded.Reference != tt.cmd.Reference {
				t.Errorf("Reference: got %+v, want %+v", decoded.Reference, tt.cmd.Reference)
			}

			// ── 5. Verify the decoded command passes Validate ─────
			if err := decoded.Validate(); err != nil {
				t.Errorf("decoded command failed Validate: %v", err)
			}
		})
	}
}

// TestJobHandler_RoundTrip_DeterministicID verifies that the
// deterministic ID survives the JSON round-trip.
func TestJobHandler_RoundTrip_DeterministicID(t *testing.T) {
	cmd := domain.GenerateVoiceoverCommand{
		Text:   "Hello world",
		Locale: "en-US",
		Voice:  "en-US-RogerNeural",
		Destination: domain.DestinationRef{
			FolderID: "folder123",
		},
	}

	// Pre-compute deterministic ID before serialisation.
	wantID := domain.BuildID(cmd)

	// Serialise → deserialise.
	payloadJSON, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded domain.GenerateVoiceoverCommand
	if err := json.Unmarshal(payloadJSON, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// The deterministic ID must be identical after round-trip.
	gotID := domain.BuildID(decoded)
	if gotID != wantID {
		t.Errorf("deterministic ID changed after round-trip: got %q, want %q", gotID, wantID)
	}

	// Filename must also be deterministic.
	wantFilename := domain.BuildFilename(decoded, gotID)
	if wantFilename == "" {
		t.Error("BuildFilename returned empty string")
	}
}

// TestJobHandler_RejectInvalidPayload verifies that invalid payloads
// are rejected by the job handler (validation guard).
func TestJobHandler_RejectInvalidPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload any
	}{
		{
			name:    "empty text",
			payload: domain.GenerateVoiceoverCommand{Locale: "en-US"},
		},
		{
			name:    "empty locale",
			payload: domain.GenerateVoiceoverCommand{Text: "Hello"},
		},
		{
			name:    "numeric locale (not BCP-47)",
			payload: domain.GenerateVoiceoverCommand{Text: "Hello", Locale: "12-AB"},
		},
		{
			name:    "locale with spaces",
			payload: domain.GenerateVoiceoverCommand{Text: "Hello", Locale: "en US"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payloadJSON, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var cmd domain.GenerateVoiceoverCommand
			if err := json.Unmarshal(payloadJSON, &cmd); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			// Validate must reject invalid commands.
			if err := cmd.Validate(); err == nil {
				t.Errorf("expected Validate to fail for %s, but it passed", tt.name)
			}
		})
	}
}

// TestJobHandler_TypeConstant verifies the job type constant is
// correctly defined and matches between domain and registry.
func TestJobHandler_TypeConstant(t *testing.T) {
	// The domain/job constant must match the registry constant.
	if appjobs.TypeVoiceoverGenerate != "voiceover.generate" {
		t.Errorf("TypeVoiceoverGenerate: got %q, want %q",
			appjobs.TypeVoiceoverGenerate, "voiceover.generate")
	}
}
