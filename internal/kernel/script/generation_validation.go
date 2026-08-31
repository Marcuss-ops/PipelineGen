package script

import (
	"strconv"
	"strings"
)

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
		if details := validateFixedSections(item, ref); len(details) > 0 {
			return &PlanInvalidError{ItemID: item.ID, Details: details}
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

func validateFixedSections(item GenerationItemV2, ref string) []string {
	var d []string
	for _, pair := range []struct {
		name string
		sec  *FixedSection
	}{{name: "intro", sec: item.Intro}, {name: "outro", sec: item.Outro}} {
		sec := pair.sec
		if sec == nil {
			continue
		}
		if err := ValidateSpeakableText(sec.Text); err != nil {
			d = append(d, ref+": "+pair.name+".text "+err.Error())
		}
		ids := sec.NormalizedClipIDs()
		if len(ids) == 0 {
			d = append(d, ref+": "+pair.name+".clip_ids requires exactly one clip_id")
		} else if len(ids) != 1 {
			d = append(d, ref+": "+pair.name+".clip_ids must contain exactly one clip_id (got "+strconv.Itoa(len(ids))+")")
		}
		for _, id := range ids {
			if id == "" {
				d = append(d, ref+": "+pair.name+".clip_ids cannot be empty or whitespace-only")
			}
		}
		if dup := firstDuplicate(ids); dup != "" {
			d = append(d, ref+": duplicate "+pair.name+" clip_id "+dup)
		}
		// literal sections require at least one clip-bearing source (clips/catalog/search/curate)
		// or a stock-only item; text-only sources cannot host an intro clip.
		if !IsClipSourceType(item.Source.Type) && item.Source.Type != SourceCatalog && item.Source.Type != SourceSearch && item.Source.Type != SourceCurate && item.MediaMode != MediaModeStockOnly {
			d = append(d, ref+": "+pair.name+" requires a clip-bearing source (clips, catalog, search, curate)")
		}
		// intro/outro clip_ids must not duplicate source clips when explicit segment ownership is absent
		all := append([]string(nil), item.Source.ClipIDs...)
		all = append(all, item.Source.IntroClipIDs...)
		for _, seg := range item.ScriptParams.Segments {
			all = append(all, seg.ClipIDs...)
		}
		for _, id := range ids {
			for _, existing := range all {
				if strings.TrimSpace(existing) == strings.TrimSpace(id) {
					d = append(d, ref+": "+pair.name+".clip_ids duplicate clip_id "+id+" already in source/segments")
					break
				}
			}
		}
	}
	// intro and outro must not share the same clip
	if item.Intro != nil && item.Outro != nil {
		introIDs := item.Intro.NormalizedClipIDs()
		outroIDs := item.Outro.NormalizedClipIDs()
		for _, a := range introIDs {
			for _, b := range outroIDs {
				if a == b {
					d = append(d, ref+": intro and outro cannot share clip_id "+a)
				}
			}
		}
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
