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
