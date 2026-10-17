// Package scriptgeneration — remote video-computer job payload projection and
// the late-bound editorial catalogs it exposes (background/BGM/SFX/asset policy).
// Verbatim sibling split of runner_phase_document.go per the 600-LOC split wave;
// behaviour unchanged (same package, same types, same call sites).
package scriptgeneration

import (
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// buildRemoteJobPayload projects the late-bound, copy-only assembly contract
// for the remote video computer. The generation request remains intact, but
// the remote side receives one final audio asset, its deterministic clip
// windows, and the requested background/sound-effect inputs.
func buildRemoteJobPayload(req GenerateRequest, result *GenerateResult) json.RawMessage {
	base, err := json.Marshal(req)
	if err != nil {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(base, &payload); err != nil {
		return nil
	}

	remote := map[string]any{
		"final_job":                req.FinalJob,
		"audio_mode":               "FINAL_AUDIO_COPY",
		"audio_source":             "certified_final_audio",
		"scenes":                   remoteClipTimingScenes(result),
		"background_catalog":       remoteBackgroundCatalog(),
		"background_music":         req.BackgroundMusic,
		"background_music_catalog": remoteBackgroundMusicCatalog(),
		"sound_effects":            req.SoundEffects,
		"sound_effect_catalog":     remoteSoundEffectCatalog(),
		"editing_assets_policy":    remoteEditingAssetsPolicy(),
	}
	if result != nil && result.FinalAudio != nil {
		remote["final_audio"] = map[string]any{
			"asset_id":               result.FinalAudio.AssetID,
			"url":                    result.FinalAudio.DriveLink,
			"duration_ms":            result.FinalAudio.DurationMS,
			"duration_us":            result.FinalAudio.DurationUS,
			"sha256":                 result.FinalAudio.FinalAudioSHA256,
			"codec":                  result.FinalAudio.Codec,
			"profile":                result.FinalAudio.Profile,
			"sample_rate":            result.FinalAudio.SampleRate,
			"channels":               result.FinalAudio.Channels,
			"channel_layout":         result.FinalAudio.ChannelLayout,
			"copy_eligible":          result.FinalAudio.CopyEligible,
			"audio_contract_version": result.FinalAudio.AudioContractVersion,
			"audio_plan_version":     result.FinalAudio.AudioPlanVersion,
			"audio_plan_sha256":      result.FinalAudio.PlanSHA256,
		}
	}
	if result != nil && result.CanonicalTimeline != nil {
		remote["timeline_duration_us"] = result.CanonicalTimeline.DurationUS
		remote["timeline_duration_ms"] = (result.CanonicalTimeline.DurationUS + 999) / 1000
	}
	encodedRemote, err := json.Marshal(remote)
	if err != nil {
		return nil
	}
	payload["remote_render"] = encodedRemote
	out, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return out
}

// remoteEditingAssetsPolicy projects the canonical editorial-asset selection
// policy into the payload's late-bound assembly contract.
//
// The remote assembler is the component that legitimately selects an asset
// when a job did not name one, so it is the right place for the policy to be
// CONSUMABLE. Publishing it is purely additive: it changes no generated
// selection and therefore no rendered output, unlike applying the policy
// implicitly at request-build time.
func remoteEditingAssetsPolicy() map[string]any {
	policy := mediaregistry.DefaultEditingAssetsPolicy()
	return map[string]any{
		"backgrounds": map[string]any{
			"pool": policy.Backgrounds.Pool,
		},
		"bgm": map[string]any{
			"pool":                 policy.BGM.Pool,
			"gain_db":              policy.BGM.GainDB,
			"loop":                 policy.BGM.Loop,
			"duck_under_voiceover": policy.BGM.DuckUnderVoiceover,
			"duck_gain_db":         policy.BGM.DuckGainDB,
		},
		"sfx": map[string]any{
			"transition_pool": policy.SFX.TransitionPool,
			"gain_db":         policy.SFX.GainDB,
		},
	}
}

// remoteBackgroundCatalog is the stable alias-to-Drive mapping for every
// curated video background plate, exposed next to the BGM and SFX catalogs so
// the remote assembler selects every editorial asset from the SAME registry
// (mediaregistry). It is a projection of the registry, never a second catalog.
func remoteBackgroundCatalog() map[string]map[string]string {
	const driveBase = "https://drive.google.com/file/d/"
	catalog := make(map[string]map[string]string)
	for _, asset := range mediaregistry.EditorialBackgroundAssets() {
		catalog[asset.ID] = map[string]string{
			"asset_id":      asset.ID,
			"filename":      asset.Filename,
			"drive_file_id": asset.DriveFileID,
			"sha256":        asset.SHA256,
			"media_type":    asset.MediaType,
			"role":          asset.Role,
			"url":           "velox-drive://" + asset.DriveFileID,
			"drive_link":    driveBase + asset.DriveFileID + "/view?usp=drive_link",
		}
	}
	return catalog
}

func remoteBackgroundMusicCatalog() map[string]map[string]string {
	out := make(map[string]map[string]string)
	for _, asset := range mediaregistry.EditorialAudioAssets() {
		if asset.Family != "music" {
			continue
		}
		out[asset.Alias] = map[string]string{"asset_id": asset.Alias, "name": asset.Name, "drive_file_id": asset.DriveFileID, "url": "velox-drive://" + asset.DriveFileID, "drive_link": "https://drive.google.com/file/d/" + asset.DriveFileID + "/view?usp=drive_link"}
	}
	return out
}

// remoteSoundEffectCatalog is the stable alias-to-Drive mapping exposed in
// the final payload. The remote assembler may select any canonical transition
// effect directly (whop1..whop6 and the bound whoosh1..whoosh3); the generation
// job keeps the list empty unless an effect is explicitly selected in the
// request.
func remoteSoundEffectCatalog() map[string]map[string]string {
	const driveBase = "https://drive.google.com/file/d/"
	catalog := make(map[string]map[string]string)
	for _, asset := range mediaregistry.EditorialAudioAssets() {
		if asset.Family != "transition" {
			continue
		}
		catalog[asset.Alias] = map[string]string{
			"asset_id":      asset.Alias,
			"drive_file_id": asset.DriveFileID,
			"url":           "velox-drive://" + asset.DriveFileID,
			"drive_link":    driveBase + asset.DriveFileID + "/view?usp=drive_link",
		}
	}
	return catalog
}

func remoteClipTimingScenes(result *GenerateResult) []map[string]any {
	if result == nil || result.CanonicalTimeline == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(result.CanonicalTimeline.Segments))
	for _, segment := range result.CanonicalTimeline.Segments {
		clips := segment.EffectiveVideoSegments()
		clipRows := make([]map[string]any, 0, len(clips))
		for _, clip := range clips {
			clipRows = append(clipRows, map[string]any{
				"asset_id":             clip.AssetID,
				"source_in_ms":         clip.SourceInUS / 1000,
				"source_duration_ms":   clip.SourceDurationUS / 1000,
				"timeline_start_ms":    clip.TimelineOffsetUS / 1000,
				"timeline_duration_ms": clip.TimelineDurationUS / 1000,
				"freeze":               clip.Freeze,
			})
		}
		out = append(out, map[string]any{
			"id":             segment.ID,
			"index":          segment.Index,
			"start_ms":       segment.TimelineStartUS / 1000,
			"duration_ms":    segment.DurationUS / 1000,
			"end_ms":         (segment.TimelineStartUS + segment.DurationUS) / 1000,
			"clips":          clipRows,
			"audio_start_ms": segment.TimelineStartUS / 1000,
			"audio_end_ms":   (segment.TimelineStartUS + segment.DurationUS) / 1000,
			"audio_source":   "final_audio",
		})
	}
	return out
}
