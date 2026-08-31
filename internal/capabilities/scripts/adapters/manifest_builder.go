// Package adapters — manifest_builder.go (PR 1, SCRIPT-DOWNSTREAM-CUTOVER wave)
//
// buildManifestV2 is the canonical constructor of the ManifestV2
// envelope (the typed surface that REPLACES the legacy inline
// voice/image collection on the script manifest). It is the SOLE
// writer of the Items slice (godlike/06 SSOT one-owner-per-fact).
//
// The build logic is a pure function of (plan, input) — it does NOT
// touch the database, the drive SDK, or any external service. This
// isolation lets the TDD surface exercise every branch via
// process_segment-style hermetic tests (see processor_manifest_v2_test.go)
// without standing up SQLite or Drive.
//
// Postprocessor-driven Item assembly:
//
//	plan.HasPostprocessor(string(ProcessorVoiceover)) → emit a
//	    DownstreamRequest{Kind: DownstreamVoiceover, ...} entry
//	    with the canonical VoiceoverRequirements sub-struct.
//	plan.HasPostprocessor(string(ProcessorImages))    → emit a
//	    DownstreamRequest{Kind: DownstreamImages, ...} entry
//	    with canonical ImagesRequirements (Count=1, defaults).
//	    DownstreamRequest{Kind: DownstreamDocument, ...} entry.
//
// When the plan has NO postprocessors, the returned manifest is
// the canonical NEW-mode empty envelope (NoInlineAssets=true,
// Items=[]) — distinct from the LEGACY zero-value &ManifestV2{}
// (NoInlineAssets=false) which the dispatcher's fail-closed
// branch rejects with ErrLegacyManifestRejected.
//
// The ItemRef field of each DownstreamRequest is the plan.ID
// (the canonical per-item identifier — each PersistenceProcessor
// invocation is keyed on one GenerationItemV2.ID). The Required
// flag is the canonical fail-closed signal for the Step 11B
// dispatcher: true = parent script.generate propagates FAILED
// if this downstream sibling cannot be produced.
//
// OutputDest is currently the canonical zero-value
// (Kind="drive_folder", FolderID="", DocumentTitle=""); the
// drive-folder resolution lives in a follow-up PR that wires
// the LocationResolver port (canonical Surface from the
// SEMANTIC-LOCATION-API wave) into the manifest builder. Until
// that lands, the dispatcher reads the empty FolderID and
// falls back to the canonical default destination.
package adapters

import (
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// canonicalDefaultVoiceID returns the canonical non-empty placeholder
// VoiceID for a (Language) pair. The dispatcher is the canonical SOLE
// owner of selecting the actual voice id (it reads
// cfg.VoiceoverConfig.VoiceByLanguage[plan.Language] at sibling-spawn-time);
// the manifest emit only carries a sentinel non-empty VoiceID so
// VoiceoverRequirements is non-nil (NewVoiceoverRequirements FAIL-CLOSED
// on VoiceID == "" per godlike/07 NO-FAKE-AVAILABILITY contract — see
// downstream.go).
//
// godlike/07 NO-FAKE-AVAILABILITY defense-in-depth: the placeholder
// format uses a namespaced sentinel pattern that CANNOT collide
// with any real voice identifier from canonical TTS providers
// (Google Cloud TTS, Edge TTS, ElevenLabs etc. emit names like
// "en-US-AriaNeural" or "Rachel" — none contain "placeholder" or
// leading underscores). If a future agent breaks the dispatcher's
// override logic and accidentally reads the manifest's VoiceID as a
// production value, the canonical provider's voice-name validation
// will REJECT this placeholder with a typed error ("voice not found")
// instead of silently routing audio to a plausible-but-wrong voice.
//
// Format: `__placeholder_voiceid__:<language>` (e.g.
// "__placeholder_voiceid__:it" for Italian). Empty language falls
// back to "__placeholder_voiceid__:default".
func canonicalDefaultVoiceID(language string) string {
	if language == "" {
		return "__placeholder_voiceid__:default"
	}
	return "__placeholder_voiceid__:" + language
}

// buildManifestV2 constructs the canonical NEW-mode ManifestV2
// envelope from the plan's postprocessor list. The returned
// manifest is always NoInlineAssets=true (canonical migration
// marker per the SCRIPT-DOWNSTREAM-CUTOVER wave).
//
// Per godlike/06 SSOT (one canonical owner per fact):
//   - this function is the SOLE owner of the Items slice assembly
//     (no other code path builds the manifest).
//   - canonical postprocessor identifiers come from
//     processor_names.go (ProcessorVoiceover, ProcessorImages).
//     The HasPostprocessor check is the plan-level
//     "this processor is registered" predicate (one canonical
//     helper in the domain layer). The document.generate
//     downstream job is owned by the canonical
//     internal/capabilities/document/usecase.go pipeline
//     (Sprint 1.0 retirement — script path no longer emits it).
//
// Per godlike/07 fail-closed:
//   - nil plan → canonical empty NEW-mode manifest (NOT a panic).
//   - plan with no postprocessors → canonical empty NEW-mode manifest
//     (NOT a silent legacy zero-value).
//
// Per godlike/07 minimum-blast-radius:
//   - the function is pure (no I/O, no globals, no clock state).
//   - the input parameter is named `_` for input.SpecScene — the
//     canonical per-scene binding fields are reachable via the
//     plan's Postprocessors list, not via the typed model output.
//     Keeping the input parameter means callers don't need to
//     refactor their existing call sites.
//
// The OutputDest for each DownstreamRequest is the canonical
// zero-value (Kind="drive_folder", FolderID="", DocumentTitle="")
// — the drive-folder resolution is a follow-up PR (see
// PR-DOCUMENT-AND-IMAGES-FOLDER-RESOLUTION forward-pointer).
func buildManifestV2(plan *scriptpkg.ResolvedGenerationPlan, _ ProcessInput) *scriptpkg.ManifestV2 {
	m := scriptpkg.NewManifestV2()
	if plan == nil {
		return m
	}

	// Voiceover sibling: emit a per-item DownstreamRequest
	// envelope with the canonical VoiceoverRequirements sub-struct
	// (the dispatcher's fail-closed branch requires Voiceover !=
	// nil for a DownstreamVoiceover envelope).
	if plan.HasPostprocessor(string(ProcessorVoiceover)) {
		m.Items = append(m.Items, *scriptpkg.NewDownstreamRequestVoiceover(
			plan.ID,
			true, // Required: fail-closed at Step 11B
			scriptpkg.NewVoiceoverRequirements("edge-tts", // Provider — canonical default
				canonicalDefaultVoiceID(plan.Language), // VoiceID — non-empty placeholder (see canonicalDefaultVoiceID goddoc for the namespaced sentinel rationale)
				"",                                     // Pace — provider default
				"",                                     // StylePreset — provider default
			),
			scriptpkg.OutputDestination{}, // canonical zero-value; folder resolution is forward-pointer
		))
	}

	// Image siblings: emit a per-item DownstreamRequest envelope
	// with the canonical ImagesRequirements sub-struct (Count=1,
	// defaults to google_slides + 1920x1080 via NewImagesRequirements).
	if plan.HasPostprocessor(string(ProcessorImages)) {
		m.Items = append(m.Items, *scriptpkg.NewDownstreamRequestImages(
			plan.ID,
			true,                                   // Required: fail-closed at Step 11B
			scriptpkg.NewImagesRequirements(1, ""), // Count=1 + default style preset
			scriptpkg.OutputDestination{},
		))
	}

	return m
}
