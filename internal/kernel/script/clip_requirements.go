package script

// clip_requirements.go — the canonical answer to "which clips does this
// envelope bind, and does each one need a spoken track?".
//
// Why a kernel type (2026-11-23): the Augias r42 Stage 0 gate lived only in
// `cmd/velox`, where it walked Source.ClipIDs and the segments itself. The
// server needs the same walk to answer POST /api/script/preflight, and two
// copies of a rule this small are exactly how the CLI and the server end up
// disagreeing about which clip is "expected to speak". One owner, two callers.
//
// The walk is deliberately envelope-only (no catalog, no DB): it says WHAT to
// verify, never whether the asset is healthy.

import "strings"

// ClipRequirement is one clip the envelope binds, with the strongest speech
// requirement any binding imposes and the scenes that bind it.
type ClipRequirement struct {
	// ClipID is the media asset id exactly as written in the payload.
	ClipID string
	// NeedSpeech is true when at least one binding renders subtitles over the
	// clip: a muted clip is legitimate only for no_voiceover cutaways and for
	// the protected intro/outro sections, which keep their own audio.
	NeedSpeech bool
	// Scenes lists the segment ids that bind the clip, in binding order, so a
	// failure can name where the clip is used instead of only its id.
	Scenes []string
}

// CollectClipRequirements walks every place an envelope binds media and
// returns one entry per clip in first-binding order.
//
// Sources of bindings, in the order they are walked:
//
//   - item.source.clip_ids — the section's authoritative ordered binding;
//   - item.script_params.segments[].clip_ids — per-segment bindings, which
//     carry the no_voiceover flag (a no_voiceover cutaway shows the clip with
//     its original audio and burns no subtitles);
//   - item.intro / item.outro fixed sections — protected verbatim media whose
//     playback policy selects the original clip audio.
func CollectClipRequirements(env *GenerationEnvelopeV2) []ClipRequirement {
	if env == nil {
		return nil
	}
	byID := make(map[string]*ClipRequirement)
	order := make([]string, 0, len(env.Items))

	add := func(clipID string, needSpeech bool, sceneID string) {
		clipID = strings.TrimSpace(clipID)
		if clipID == "" {
			return
		}
		req, ok := byID[clipID]
		if !ok {
			req = &ClipRequirement{ClipID: clipID}
			byID[clipID] = req
			order = append(order, clipID)
		}
		// One binding that needs speech is enough: the clip is rendered with
		// subtitles for that scene whatever the others allow.
		if needSpeech {
			req.NeedSpeech = true
		}
		sceneID = strings.TrimSpace(sceneID)
		if sceneID == "" {
			return
		}
		for _, existing := range req.Scenes {
			if existing == sceneID {
				return
			}
		}
		req.Scenes = append(req.Scenes, sceneID)
	}

	for i := range env.Items {
		item := &env.Items[i]
		for _, clipID := range item.Source.ClipIDs {
			add(clipID, true, "")
		}
		for _, segment := range item.ScriptParams.Segments {
			for _, clipID := range segment.ClipIDs {
				add(clipID, !segment.NoVoiceover, segment.ID)
			}
		}
		for _, section := range []*FixedSection{item.Intro, item.Outro} {
			if section == nil {
				continue
			}
			for _, clipID := range section.ClipIDs {
				add(clipID, false, "")
			}
		}
	}

	out := make([]ClipRequirement, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}
