// Package voiceover defines the canonical domain types for voiceover
// generation. PR 1 (voiceover cleanup, June 2026): single-source-of-truth
// contract — every producer (HTTP handler, script postprocessor, worker)
// builds and consumes these types exclusively.
//
// Rules:
//   - No map[string]any
//   - No any
//   - No client-controlled filename
//   - No string strategy
//   - force_regenerate boolean
//   - Locale normalized
//   - Deterministic ID via SHA256(text + locale + voice + destination)
package voiceover

import (
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
)

// GenerateVoiceoverCommand is the canonical command for a single voiceover
// generation. Every call site — HTTP handler, script postprocessor, or
// worker — forwards this struct to the voiceover service layer's typed
// Generator port (see internal/application/voiceover/ports.go).
type GenerateVoiceoverCommand struct {
	// Text is the exact string to convert to speech. Required.
	Text string

	// Locale is a BCP-47 language tag (e.g. "en-US", "it-IT", "pt-BR").
	// Normalized to lowercase before use. Required.
	Locale string

	// Voice is an optional explicit TTS voice name (e.g. "en-US-RogerNeural").
	// When empty, the VoiceRegistry resolves a default voice for the locale.
	Voice string

	// Destination is an optional Drive folder target. When empty, the file
	// is stored locally only (no Drive upload).
	Destination DestinationRef

	// Reference associates this voiceover with a script and/or scene.
	// Optional; zero value means unassociated.
	Reference Reference

	// ForceRegenerate controls deduplication. When false and an existing
	// artifact matches the deterministic ID, the cached result is returned.
	// When true, a new audio file is generated and the artifact is updated.
	ForceRegenerate bool
}

// ID returns the deterministic, sha256-based identifier for this command.
// The hash covers (text + locale + voice + destination.FolderID).
// Two commands with identical inputs produce identical IDs — this is the
// deduplication key.
func (c GenerateVoiceoverCommand) ID() string {
	h := digest.SHA256String(strings.Join([]string{c.Text, c.Locale, c.Voice, c.Destination.FolderID}, ":"))
	return fmt.Sprintf("vo_%s", h[:23]) // "vo_" + 23 hex chars (92 bits of entropy)
}

// Normalize returns a copy of the command with Locale lowercased and
// Text trimmed. Call this once at the boundary (handler or postprocessor).
func (c GenerateVoiceoverCommand) Normalize() GenerateVoiceoverCommand {
	c.Text = strings.TrimSpace(c.Text)
	c.Locale = strings.ToLower(strings.TrimSpace(c.Locale))
	c.Voice = strings.TrimSpace(c.Voice)
	return c
}

// Validate returns a typed error when required fields are missing or
// invalid. Call Normalize() first so trim+lowercase is applied before
// validation.
func (c GenerateVoiceoverCommand) Validate() error {
	if c.Text == "" {
		return ErrTextRequired
	}
	if c.Locale == "" {
		return ErrLocaleRequired
	}
	return nil
}

// Filename returns the deterministic filename derived from the command's
// identity. The client never chooses the filename — the server computes it.
// Format: vo_<ID>.mp3 (or vo_<scriptID>_<sceneID>_<locale>.mp3 when
// Reference is present).
func (c GenerateVoiceoverCommand) Filename() string {
	if c.Reference.ScriptID != "" && c.Reference.SceneID != "" {
		return fmt.Sprintf("vo_%s_%s_%s.mp3", c.Reference.ScriptID, c.Reference.SceneID, c.Locale)
	}
	return c.ID() + ".mp3"
}

// DestinationRef is the canonical Drive folder target. A single FolderID
// replaces the legacy DestinationRequest with its Group/FolderPath/
// SubfolderName/CreateSubfolder ambiguity.
type DestinationRef struct {
	// FolderID is the Google Drive folder ID. Empty means no upload.
	FolderID string
}

// Reference associates a voiceover with a script and scene for traceability.
type Reference struct {
	ScriptID string
	SceneID  string
}

// VoiceProfile is the resolved voice configuration returned by the
// VoiceRegistry. Contains everything the TTS provider needs to generate
// audio without further lookups.
type VoiceProfile struct {
	// Name is the canonical TTS voice name (e.g. "en-US-RogerNeural").
	Name string

	// Locale is the BCP-47 tag this voice serves (e.g. "en-US").
	Locale string

	// Provider is the TTS engine identifier (e.g. "edge", "azure").
	Provider string
}
