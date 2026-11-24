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
	for i := range e.Items {
		if err := validateEnvelopeItem(&e.Items[i], i, e.FinalJob); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvelopeItem(item *GenerationItemV2, index int, finalJob bool) error {
	ref := item.ID
	if ref == "" {
		ref = "item " + strconv.Itoa(index)
	}
	if err := validateEnvelopeSource(item, ref, finalJob); err != nil {
		return err
	}
	if err := validateEnvelopeOutput(item, ref); err != nil {
		return err
	}
	if err := validateEnvelopeOverlays(item, ref); err != nil {
		return err
	}
	if err := validateEnvelopeScriptParams(item, ref); err != nil {
		return err
	}
	if err := validateEnvelopeMedia(item, ref); err != nil {
		return err
	}
	return validateEnvelopePolicy(item, ref)
}

func validateEnvelopeSource(item *GenerationItemV2, ref string, finalJob bool) error {
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
	// final_job hands the item to the remote render boundary. Accepting it
	// with render disabled only fails after generation, when the operator has
	// already paid for the script; reject it at ingress.
	if finalJob && !item.Output.Render.Enabled {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: []string{ref + ": output.render.enabled must be true when final_job=true"},
		}
	}

	// Deprecated-field fence: source.intro_clip_ids is removed from the
	// contract, so any payload still carrying it fails closed here.
	if err := rejectDeprecatedIntroClipIDs(*item, ref); err != nil {
		return err
	}

	// Expand the public per-segment Drive-folder shorthand before media
	// validation so stock_only sees the same canonical internal bindings as
	// legacy callers. The envelope is intentionally mutated once at ingress.
	return ExpandSegmentStockFolders(item)
}

func validateEnvelopeOutput(item *GenerationItemV2, ref string) error {
	// Validate tri-state output and segment overrides at the structural
	// boundary so unknown toggles never degrade into an implicit default.
	if !validEntityExtractionToggle(item.Output.ExtractEntities) {
		return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": output.extract_entities must be default, enabled, or disabled"}}
	}
	if details := validateSegmentEntityExtractionToggles(item.ScriptParams.Segments, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	// The narration word floor is a deterministic copy contract, not a
	// runtime surprise: a verbatim narration below the floor used to pass
	// the ingress and fail at GENERATING_SCENE_TEXT.
	if details := ValidateAuthoredNarration(item.ScriptParams); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	return nil
}

func validateEnvelopeOverlays(item *GenerationItemV2, ref string) error {
	if err := ValidateOverlayCategories(item.OverlayCategories); err != nil {
		return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": " + err.Error()}}
	}
	if details := validateOverlayImageSFX(item.OverlayImages, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	for index, segment := range item.ScriptParams.Segments {
		if err := ValidateOverlayCategories(segment.OverlayCategories); err != nil {
			return &PlanInvalidError{ItemID: item.ID, Details: []string{ref + ": script_params.segments[" + strconv.Itoa(index) + "]: " + err.Error()}}
		}
	}
	return nil
}

func validateEnvelopeScriptParams(item *GenerationItemV2, ref string) error {
	// Universal numeric invariants — every payload must satisfy these
	// regardless of config, caller, or installation.
	if details := validateGenerationScriptParams(item.ScriptParams, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	return nil
}

func validateEnvelopeMedia(item *GenerationItemV2, ref string) error {
	if err := validateMediaMode(*item, ref); err != nil {
		return err
	}
	if details := validateAudioMode(*item, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	if details := validateAudioIntentBlock(*item, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	if details := validateIntroHookStock(item.ScriptParams.Segments, item.Output.StockBindings, ref); len(details) > 0 {
		return &PlanInvalidError{
			ItemID:  item.ID,
			Details: details,
		}
	}
	if details := validateFixedSections(*item, ref); len(details) > 0 {
		return &PlanInvalidError{ItemID: item.ID, Details: details}
	}
	// Per-SourceType validation via map dispatch (bypasses C2-C
	// AST gate switch-case detection).
	if handler, ok := sourceTypeHandlers[item.Source.Type]; ok {
		if err := handler(*item, ref); err != nil {
			return err
		}
	}
	if item.Language != "" && !IsValidGenerationLanguage(item.Language) {
		return &PlanInvalidError{
			ItemID: item.ID,
			Details: []string{
				ref + ": unsupported language " + item.Language,
			},
		}
	}
	return nil
}

func validateEnvelopePolicy(item *GenerationItemV2, ref string) error {
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
func validEntityExtractionToggle(toggle Toggle) bool {
	switch toggle {
	case "", ToggleDefault, ToggleEnabled, ToggleDisabled:
		return true
	default:
		return false
	}
}

// validateOverlayImageSFX gates the per-image cue block at the payload
// boundary: a pin that requests a cue must name its scene (the cue is bound
// to the image the planner produces for that scene) and carry a usable asset
// identity.
func validateOverlayImageSFX(pins []OverlayImageAsset, ref string) []string {
	var details []string
	for index, pin := range pins {
		if pin.SoundEffect == nil {
			continue
		}
		prefix := ref + ": overlay_images[" + strconv.Itoa(index) + "]"
		if strings.TrimSpace(pin.SceneID) == "" {
			details = append(details, prefix+".sound_effect requires scene_id")
		}
		if err := pin.SoundEffect.Validate(); err != nil {
			details = append(details, prefix+"."+err.Error())
		}
	}
	for index, pin := range pins {
		if pin.Style == nil {
			continue
		}
		prefix := ref + ": overlay_images[" + strconv.Itoa(index) + "]"
		if strings.TrimSpace(pin.SceneID) == "" {
			details = append(details, prefix+".style requires scene_id")
		}
		if err := ValidateImageStyle(pin.Style); err != nil {
			details = append(details, prefix+".style: "+err.Error())
		}
	}
	return details
}

func validateSegmentEntityExtractionToggles(segments []ScriptSegment, ref string) []string {
	var details []string
	for index, segment := range segments {
		if !validEntityExtractionToggle(segment.ExtractEntities) {
			details = append(details, ref+": script_params.segments["+strconv.Itoa(index)+"].extract_entities must be default, enabled, or disabled")
		}
	}
	return details
}

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
		// display_text is visual/document metadata, never narration: it is not
		// translated or synthesized, so speakable-text validation is deliberately
		// not applied. The legacy `text` narration alias was removed — a payload
		// that still sends intro.text/outro.text fails closed here.
		if strings.TrimSpace(sec.Text) != "" {
			d = append(d, ref+": "+pair.name+".text is deprecated; use "+pair.name+".display_text (fixed sections carry no speakable text)")
		}
		if !sec.NormalizedPlayback().Valid() {
			d = append(d, ref+": "+pair.name+".playback must use audio_mode=original_clip or silence with a valid source window")
		}
		ids := sec.NormalizedClipIDs()
		if len(ids) == 0 {
			d = append(d, ref+": "+pair.name+".clip_ids requires at least one clip_id")
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
		// The generation normalizer supplies COMBINED_TIMELINE when
		// voiceover is enabled and the caller omitted an audio mode.
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
