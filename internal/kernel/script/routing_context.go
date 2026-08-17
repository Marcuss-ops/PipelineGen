// Package script — routing_context.go defines the canonical artifact
// routing decision for a generation item's published artifacts.
package script

import "strings"

// ResolveArtifactRoutingContext builds the canonical routing context from the
// generation input facts. It is the SINGLE resolution point for artifact
// routing: every downstream component (voiceover publish, document publish)
// must consume the resolved context and never re-derive Project, Language, or
// folder routing from another surface. Empty values mean "use the configured
// default"; Project is the only fact that may fail closed downstream when a
// publish is requested (godlike/07 NO-FAKE-AVAILABILITY).
func ResolveArtifactRoutingContext(project, language, voiceoverFolderID, docsFolderID string) ArtifactRoutingContext {
	return ArtifactRoutingContext{
		Project:           strings.TrimSpace(project),
		Language:          strings.TrimSpace(language),
		VoiceoverFolderID: strings.TrimSpace(voiceoverFolderID),
		DocsFolderID:      strings.TrimSpace(docsFolderID),
	}
}

// ArtifactRoutingContext is the single resolved routing decision for a
// generation item's downstream artifacts (voiceover, documents). It is
// resolved ONCE at generation start and propagated verbatim to every
// downstream artifact producer; no component downstream of resolution may
// derive or invent these values (godlike/06 SSOT — one owner per fact).
//
// It carries only routing facts, never model inputs or editorial content.
type ArtifactRoutingContext struct {
	// Project is the semantic project namespace used to route published
	// artifacts (voiceover, documents). It originates from the generation
	// input and is REQUIRED whenever a publish is requested. A
	// voiceover-enabled generation with an empty Project fails closed
	// BEFORE the first TTS call rather than silently inventing a fallback
	// namespace (godlike/07 NO-FAKE-AVAILABILITY).
	Project string `json:"project,omitempty"`

	// Language is the primary source language of the generation item.
	Language string `json:"language,omitempty"`

	// VoiceoverFolderID is the resolved Drive folder for voiceover
	// artifacts. Empty means "use the configured default".
	VoiceoverFolderID string `json:"voiceover_folder_id,omitempty"`

	// DocsFolderID is the resolved Drive folder for document artifacts.
	// Empty means "use the configured default".
	DocsFolderID string `json:"docs_folder_id,omitempty"`
}
