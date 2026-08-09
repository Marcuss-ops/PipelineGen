package config

// MultilingualConfig holds settings for multilingual media-language
// generation. The canonical runtime SSOT is Languages.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the
// language policy shifted from hardcoded fallbacks to a config-driven,
// BCP-47-normalized registry. godlike/06 SSOT: the canonical source
// for pipeline language capabilities is `Languages`.
type MultilingualConfig struct {
	Enabled        bool   `yaml:"enabled" default:"false"`
	SourceLanguage string `yaml:"source_language" default:"en"`
	// Languages is the canonical SSOT for the pipeline's
	// language capabilities (PR-CATALOG-MULTILINGUA step 3,
	// July 2026). Loaded from `multilingual.languages:` in the
	// YAML; accepts BOTH the legacy CSV shape
	// ( `[it, en, es, ...]`, auto-promoted) AND the typed
	// struct-list shape
	// ( `[{code: it, enabled: true, translate_clips: true, generate_tts: false}, ...]` ).
	// The composition root constructs a single
	// asset.LanguageRegistry from this slice and threads it
	// into every pipeline that needs to know which languages
	// are enabled (texttracks materializer is the
	// first-migrated consumer; future steps migrate voices,
	// scripts, etc.). godlike/06 SSOT: this is the canonical
	// YAML surface for pipeline language capabilities.
	Languages LanguageSpecSlice `yaml:"languages"`
	// RequireLanguageCertainty, when true, makes the YouTube
	// acquisition chain (TextTrackResolver.AcquireSegmentText) fail
	// with asset.ErrLanguageUndeterminable PRE-STEP-9 if no chain
	// level (1: payload, 2: DB READY, 3+4: YT subtitles, 5: Whisper)
	// surfaces a real BCP-47 language. Default false preserves the
	// pre-Fase-1.b behavior where the chain degrades to "und" silently.
	// godlike/07 fail-closed at the policy gate: when this is true
	// the writer (CommitClipTextAndIndexEvent) ALSO surfaces
	// ErrClipLocaleNotReady if a non-und language was never resolved.
	RequireLanguageCertainty bool `yaml:"require_language_certainty" default:"false"`

	// RequireTranscriptReady is the Fase 5 (PR-PY-CLIPS-CORRETTE-TRADOTTE,
	// July 2026) wire-up of the pre-existing
	// localized.CommitLocalizedClipCommand.RequireTranscriptReady
	// policy gate. When true, the YouTube segment pipeline's
	// Step 9 super-tx fails PRE-TX with
	// localized.ErrClipLocaleNotReady if no transcript-origin
	// READY track is present in the command's TextTracks.
	// Default false preserves the Fase 2.b atomic-super-tx
	// behaviour (every well-formed clip is persisted; backfill
	// is decoupled from clip-write). Operators flip to true
	// after a successful Fase 5 admin backfill pass to harden
	// the pipeline (cmd/admin/text_tracks_backfill.go).
	RequireTranscriptReady bool `yaml:"require_transcript_ready" default:"false"`

	// RequireAllLanguagesBeforeVideo is the Fase 5.1 (Aug 2026)
	// decoupled policy gate. When true, the YouTube segment pipeline's
	// Step 9 super-tx fails PRE-TX with
	// localized.ErrClipLocaleNotReady unless EVERY PreferredLanguage has
	// a READY transcript-origin track. Default false: the pipeline
	// persists well-formed clips with only the languages actually
	// produced (e.g. a single "en" Whisper transcript) without waiting
	// for the full multilingual fan-out. This flag is independent from
	// Enabled: an operator may keep the multilingual registry active
	// for voiceover/subtitle generation while NOT gating clip-write on
	// full translation coverage.
	RequireAllLanguagesBeforeVideo bool `yaml:"require_all_languages_before_video" default:"false"`

	// MigrationFallbackLegacyMetadata REMOVED in Fase 4 strict cutover (July 2026).
	// The legacy metadata_json["transcript"] / metadata_json["clean_transcript"] read is
	// RETIRED; the video pipeline reads transcripts EXCLUSIVELY from asset_text_tracks
	// via the TextTrackReader port. See
	// internal/application/scripts/usecase/clip_source_builder_transcript.go for the
	// canonical audit trail.
	// TranslationPolicy controls the application-layer model
	// selection passed to the TranslationPort for the
	// TextTrackMaterializer (Fase 3, PR-PY-CLIPS-CORRETTE-TRADOTTE,
	// July 2026). Maps onto the canonical `domain.ModelPolicy`
	// enum values:
	//   - "auto"    → server default (translation.TranslationPort
	//                 resolves the model from source/target
	//                 language pair + content length)
	//   - "fast"    → fast model (e.g. ollama gemma3:4b)
	//   - "quality" → quality model (e.g. ollama llama3:70b)
	//
	// Default "auto" — matches the pre-Fase-3 server-default
	// behaviour. Operators wanting explicit control set this to
	// "fast" or "quality" in config/multilingual.yaml.
	//
	// godlike/07 fail-closed: an invalid value is a startup
	// error (the composition root validates against the
	// domain.ModelPolicy enum at boot time, not a silent
	// fallback to "auto").
	TranslationPolicy string `yaml:"translation_policy" default:"auto"`
}
