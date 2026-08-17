// Package entities owns the canonical EntityTimeline projection: the
// deterministic, SSOT mapping of every extracted entity occurrence onto the
// voiceover that was actually synthesized for the scene.
//
// The rule that makes this package the SSOT: an entity's timestamp is NEVER
// estimated from the length of its text. Every audio boundary comes from the
// canonical word-level SpeechTimingArtifact captured in the same synthesis
// stream as the published voiceover (audio.LocatePhrase), and every global
// boundary is the scene's canonical timeline offset plus that local span —
// exactly the local→global contract audio.PhraseTiming already enforces for
// script phrases.
//
// Pipeline:
//
//	GenerateScript → scene text → NLP entity extraction (PERSON/ORG/GPE/...)
//	→ entity occurrences → word timing of the real TTS → EntityTimeline
//
// The timeline is the source the overlays read (see ResolveEntityOverlayPlan):
// an entity card never guesses WHEN to appear — it appears exactly while the
// entity is being spoken, as certified by CertifyEntityTimingChain.
//
// This package is pure: zero I/O, zero external dependencies. Adapters in the
// composition root map scene annotations + voiceover timing artifacts into
// BuildInput.
package entities
