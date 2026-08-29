package job

import "encoding/json"

// CanonicalManifest wraps operator-specific deterministic inputs. Keys are
// sorted by encoding/json and values are validated as JSON before persistence.
type CanonicalManifest struct {
	Kind   CanonicalManifestKind
	Fields InputManifest
}

func (m CanonicalManifest) JSON() (json.RawMessage, error) {
	if m.Fields == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.Marshal(m.Fields)
}

func LLMManifest(sourceFingerprint, model, modelVersion, promptVersion, language, tone string, temperature float64, seed, maxTokens int64, schemaVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestLLM, InputManifest{"source_fingerprint": sourceFingerprint, "model": model, "model_version": modelVersion, "prompt_version": promptVersion, "language": language, "tone": tone, "temperature": temperature, "seed": seed, "max_tokens": maxTokens, "structured_output_schema_version": schemaVersion}}
}

func ResearchManifest(topic string, queries []string, language, freshnessPolicy, providerPolicy, plannerVersion, rankerVersion, fetchPolicy, sourcePolicy, promptVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestResearch, InputManifest{"canonical_topic": topic, "query_set": queries, "language": language, "freshness_policy": freshnessPolicy, "provider_policy": providerPolicy, "search_planner_version": plannerVersion, "ranker_version": rankerVersion, "fetch_policy_version": fetchPolicy, "source_policy_version": sourcePolicy, "research_prompt_version": promptVersion}}
}

func TTSManifest(textSHA, language, provider, model, modelVersion, voice string, rate, pitch float64, sampleRate, channels int64, codec, timingPolicy, normalizationVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestTTS, InputManifest{"normalized_text_sha256": textSHA, "language": language, "provider": provider, "model": model, "model_version": modelVersion, "voice_id": voice, "rate": rate, "pitch": pitch, "sample_rate": sampleRate, "channels": channels, "codec": codec, "timing_policy": timingPolicy, "normalization_version": normalizationVersion}}
}

func TranslationManifest(sourceSHA, sourceLanguage, targetLanguage, model, modelVersion, promptVersion, glossaryVersion, stylePolicy string) CanonicalManifest {
	return CanonicalManifest{ManifestTranslation, InputManifest{"source_text_sha256": sourceSHA, "source_language": sourceLanguage, "target_language": targetLanguage, "model": model, "model_version": modelVersion, "prompt_version": promptVersion, "glossary_version": glossaryVersion, "style_policy": stylePolicy}}
}

func ClipManifest(sourceSHA string, startUS, endUS int64, width, height, fpsNum, fpsDen int64, codec, profile, pixelFormat, audioPolicy, normalizationPolicy, processorVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestClip, InputManifest{"source_sha256": sourceSHA, "start_us": startUS, "end_us": endUS, "width": width, "height": height, "fps_num": fpsNum, "fps_den": fpsDen, "codec": codec, "profile": profile, "pixel_format": pixelFormat, "audio_policy": audioPolicy, "normalization_policy": normalizationPolicy, "processor_version": processorVersion}}
}

func VidRushManifest(sceneSHA, semanticFingerprint, semanticVersion, providerPolicy string, durationMS int64, candidateLimit int64, rankingStrategy, rankerVersion, rerankerModel, rerankerVersion, rightsPolicy string) CanonicalManifest {
	return CanonicalManifest{ManifestVidRush, InputManifest{"scene_text_sha256": sceneSHA, "semantic_profile_fingerprint": semanticFingerprint, "semantic_model_version": semanticVersion, "provider_policy_version": providerPolicy, "target_duration_ms": durationMS, "candidate_limit": candidateLimit, "ranking_strategy": rankingStrategy, "ranker_version": rankerVersion, "reranker_model": rerankerModel, "reranker_version": rerankerVersion, "rights_policy_version": rightsPolicy}}
}

func OverlayManifest(planFingerprint, templateID, templateVersion, timingHash, rendererVersion, mediaContract, gpuMode, chrononVersion string, assetSHA256 []string, startUS, endUS, width, height, fpsNum, fpsDen int64) CanonicalManifest {
	return CanonicalManifest{ManifestOverlay, InputManifest{"plan_fingerprint": planFingerprint, "template_id": templateID, "template_version": templateVersion, "asset_sha256_list": assetSHA256, "scene_timing_hash": timingHash, "start_us": startUS, "end_us": endUS, "canvas_width": width, "canvas_height": height, "fps_num": fpsNum, "fps_den": fpsDen, "renderer_version": rendererVersion, "media_contract": mediaContract, "gpu_mode": gpuMode, "chronon_version": chrononVersion}}
}

func AudioManifest(voiceoverSHA, bgmSHA, sfxSHA, clipAudioSHA, mixPolicy, duckingConfig string, targetLUFS float64, sampleRate, channels int64, rendererVersion string) CanonicalManifest {
	return CanonicalManifest{ManifestAudio, InputManifest{"voiceover_sha256": voiceoverSHA, "bgm_sha256": bgmSHA, "sfx_sha256": sfxSHA, "clip_audio_sha256": clipAudioSHA, "mix_policy": mixPolicy, "ducking_configuration": duckingConfig, "target_lufs": targetLUFS, "sample_rate": sampleRate, "channels": channels, "audio_renderer_version": rendererVersion}}
}

func RenderManifest(planFingerprint, renderer, rendererVersion, gpuHotPath, decoderPolicy, encoderPolicy string, width, height, fpsNum, fpsDen int64) CanonicalManifest {
	return CanonicalManifest{ManifestRender, InputManifest{"plan_fingerprint": planFingerprint, "renderer": renderer, "renderer_version": rendererVersion, "gpu_hot_path_mode": gpuHotPath, "decoder_policy": decoderPolicy, "encoder_policy": encoderPolicy, "width": width, "height": height, "fps_num": fpsNum, "fps_den": fpsDen}}
}
