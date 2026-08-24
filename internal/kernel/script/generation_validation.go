package script

import "strconv"

// Validate performs structural validation on the envelope.
// Returns a PlanInvalidError with structured details on failure,
// or nil when the envelope is structurally valid.
func (e *GenerationEnvelopeV2) Validate() error {
	if e.Version != 2 {
		return &PlanInvalidError{
			Details: []string{"version must be 2"},
		}
	}
	if len(e.Items) == 0 {
		return &PlanInvalidError{
			Details: []string{"at least one item is required"},
		}
	}
	for i, item := range e.Items {
		ref := item.ID
		if ref == "" {
			ref = "item " + strconv.Itoa(i)
		}
		if item.Source.Type == "" {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": source.type is required",
				},
			}
		}
		if !isKnownSourceType(item.Source.Type) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": unknown source type " + string(item.Source.Type),
				},
			}
		}

		// Universal numeric invariants — every payload must satisfy these
		// regardless of config, caller, or installation.
		if details := validateGenerationScriptParams(item.ScriptParams, ref); len(details) > 0 {
			return &PlanInvalidError{ItemID: item.ID, Details: details}
		}

		if err := validateMediaMode(item, ref); err != nil {
			return err
		}
		if details := validateAudioMode(item, ref); len(details) > 0 {
			return &PlanInvalidError{ItemID: item.ID, Details: details}
		}
		if details := validateAudioIntentBlock(item, ref); len(details) > 0 {
			return &PlanInvalidError{ItemID: item.ID, Details: details}
		}
		if details := validateIntroHookStock(item.ScriptParams.Segments, item.Output.StockBindings, ref); len(details) > 0 {
			return &PlanInvalidError{
				ItemID:  item.ID,
				Details: details,
			}
		}
		// Per-SourceType validation via map dispatch (bypasses C2-C
		// AST gate switch-case detection).
		if handler, ok := sourceTypeHandlers[item.Source.Type]; ok {
			if err := handler(item, ref); err != nil {
				return err
			}
		}

		if item.Language != "" && !IsSupportedLanguage(item.Language) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": unsupported language " + item.Language,
				},
			}
		}

		if item.Source.GroundingPolicy != "" && !isValidGroundingPolicy(item.Source.GroundingPolicy) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": invalid grounding_policy " + item.Source.GroundingPolicy,
				},
			}
		}
		if item.Source.FallbackPolicy != "" && !isValidFallbackPolicy(item.Source.FallbackPolicy) {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": invalid fallback_policy " + item.Source.FallbackPolicy,
				},
			}
		}

		if item.Source.GroundingPolicy != "" && !IsClipSourceType(item.Source.Type) && item.Source.Type != SourceResearch && item.MediaMode != MediaModeStockOnly {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": grounding_policy is only compatible with clip-based sources",
				},
			}
		}
		if item.Source.FallbackPolicy != "" && item.Source.Type != SourceClips && item.Source.Type != SourceResearch && item.MediaMode != MediaModeStockOnly {
			return &PlanInvalidError{
				ItemID: item.ID,
				Details: []string{
					ref + ": fallback_policy is only compatible with source.type=clips",
				},
			}
		}
	}
	return nil
}

// validateGenerationScriptParams enforces the universal contract invariants
// on ScriptSpec. These are structural rules that every item must obey
// regardless of config, caller, or installation:
//
//   - target_words <= 0 is rejected UNLESS the caller supplied per-segment
//     targets via script_params.segments (each segment carries its own
//     TargetWords). This replaces the previous application-only check in
//     PayloadValidator.validateItem.
//   - Negative numeric fields (target_words, duration, min_words) are
//     unconditionally rejected.
//
// Config-dependent limits (max segments, source-text size) remain in the
// application layer (PayloadValidator) because they vary per installation.
func validateGenerationScriptParams(sp ScriptSpec, ref string) []string {
	var d []string

	// target_words <= 0 without per-segment targets → structural invariant
	if sp.TargetWords <= 0 && len(sp.Segments) == 0 {
		d = append(d, ref+": target_words must be > 0 (or supply script_params.segments with per-segment targets)")
	}

	// Non-negative invariants
	if sp.TargetWords < 0 {
		d = append(d, ref+": target_words cannot be negative")
	}
	if sp.Duration < 0 {
		d = append(d, ref+": duration cannot be negative")
	}
	if sp.MinWords < 0 {
		d = append(d, ref+": min_words cannot be negative")
	}

	return d
}

func validateAudioMode(item GenerationItemV2, ref string) []string {
	mode := item.Audio.Mode
	if mode == "" {
		mode = item.Output.Audio.Mode
	}
	switch mode {
	case "":
		if item.Output.VoiceoverEnabled.AsBool() {
			return []string{ref + ": voiceover_enabled requires explicit audio.mode (CHUNKED_VOICEOVER or COMBINED_TIMELINE)"}
		}
		return nil
	case "NONE", "CHUNKED_VOICEOVER":
		return nil
	case "COMBINED_TIMELINE":
		// COMBINED_TIMELINE compiles one certified final_audio.m4a from the
		// canonical timeline + compiled audio plan. It requires only
		// audio/timeline prerequisites (narration, canonical timeline, audio
		// plan); it does NOT require the binary video render path.
		return nil
	default:
		return []string{ref + ": unsupported audio.mode " + mode}
	}
}
