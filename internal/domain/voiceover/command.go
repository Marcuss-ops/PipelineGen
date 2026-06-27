// Package voiceover defines the canonical domain types for voiceover
// generation — the single source of truth for the voiceover contract.
//
// PR 1 (June 2026): introduces the canonical command, result, destination,
// reference, and voice-profile types. Replaces the legacy BatchRequest /
// DestinationRequest / PromoRequest / VoiceoverResult scattered across
// internal/application/voiceover/types.go.
//
// Design rules (per AGENTS.md § Pattern 0 — Port abstraction layer):
//   - No map[string]any — every field is typed.
//   - No interface{} — every method returns concrete types.
//   - No filename from client — the server owns naming deterministically.
//   - No string strategy — replaced by ForceRegenerate bool.
//   - ID is deterministic: SHA256(text + locale + voice + destination).
package voiceover

import (
	"strings"
)

// Locale is a BCP-47 language tag (e.g. "en-US", "it-IT", "pt-BR").
// Normalised to lowercase on construction.
type Locale string

// Normalize returns the locale lowercased and trimmed.
func (l Locale) Normalize() Locale {
	return Locale(strings.ToLower(strings.TrimSpace(string(l))))
}

// String returns the raw locale string.
func (l Locale) String() string { return string(l) }

// IsZero reports whether the locale is empty.
func (l Locale) IsZero() bool { return strings.TrimSpace(string(l)) == "" }

// ── Destination ──────────────────────────────────────────────────────────

// DestinationRef is the canonical destination for a voiceover artifact.
// Exactly one field — FolderID — so the caller expresses intent without
// surfacing internal resolution details (Group, FolderPath, SubfolderName).
type DestinationRef struct {
	FolderID string `json:"folder_id,omitempty"`
}

// IsZero reports whether the destination carries no observable intent.
func (d DestinationRef) IsZero() bool {
	return strings.TrimSpace(d.FolderID) == ""
}

// String returns the folder ID for use in hash computation.
func (d DestinationRef) String() string {
	return strings.TrimSpace(d.FolderID)
}

// ── Reference ────────────────────────────────────────────────────────────

// Reference links a voiceover artifact back to the script and scene
// that requested it. Both fields are optional — a standalone voiceover
// generation (no script context) omits both.
type Reference struct {
	ScriptID string `json:"script_id,omitempty"`
	SceneID  string `json:"scene_id,omitempty"`
}

// IsZero reports whether the reference carries no observable data.
func (r Reference) IsZero() bool {
	return r.ScriptID == "" && r.SceneID == ""
}

// ── VoiceProfile ─────────────────────────────────────────────────────────

// VoiceProfile is the resolved voice for TTS generation.
// VoiceCode is the full Edge-TTS voice identifier (e.g. "en-US-RogerNeural").
type VoiceProfile struct {
	Locale    Locale `json:"locale"`
	VoiceName string `json:"voice_name"`
	VoiceCode string `json:"voice_code"`
}

// IsZero reports whether the profile is empty.
func (p VoiceProfile) IsZero() bool {
	return p.Locale.IsZero() && p.VoiceName == "" && p.VoiceCode == ""
}

// ── GenerateVoiceoverCommand ────────────────────────────────────────────

// GenerateVoiceoverCommand is the single canonical command for voiceover
// generation. Every caller — HTTP handler, script postprocessor, batch
// orchestrator — constructs this same type and passes it to the single
// GenerateVoiceoverUseCase.
//
// Fields:
//
//	Text            — required; the text to synthesise.
//	Locale          — required; BCP-47 language tag.
//	Voice           — optional; when empty the VoiceRegistry picks the default.
//	Destination     — optional; when set the output is uploaded to Drive.
//	ForceRegenerate — when true, re-generates even if a cache hit exists.
//	Reference       — optional; links back to script/scene for observability.
type GenerateVoiceoverCommand struct {
	Text            string         `json:"text"`
	Locale          Locale         `json:"locale"`
	Voice           string         `json:"voice,omitempty"`
	Destination     DestinationRef `json:"destination,omitempty"`
	ForceRegenerate bool           `json:"force_regenerate,omitempty"`
	Reference       Reference      `json:"reference,omitempty"`
}

// Validate checks the command invariants. Returns nil on success, or an
// error describing the first violation.
func (c GenerateVoiceoverCommand) Validate() error {
	if strings.TrimSpace(c.Text) == "" {
		return ErrTextRequired
	}
	if c.Locale.Normalize().IsZero() {
		return ErrLocaleRequired
	}
	if !c.Locale.Normalize().IsSupported() {
		return &LocaleNotSupportedError{Locale: c.Locale.Normalize()}
	}
	// Destination: if set, FolderID must be meaningful.
	if !c.Destination.IsZero() && c.Destination.String() == "" {
		return ErrDestinationInvalid
	}
	return nil
}

// IsSupported reports whether l is in the set of known-locale patterns.
// The canonical registry lives in voice_registry.go; this fast-path guard
// rejects obviously malformed tags without importing the full registry.
func (l Locale) IsSupported() bool {
	s := string(l.Normalize())
	// Accept any BCP-47-like pattern (xx or xx-XX).
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' {
			continue
		}
		return false
	}
	return true
}
