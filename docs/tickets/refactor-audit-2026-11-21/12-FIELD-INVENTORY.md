# RA-12 — Inventario dei campi di `GenerateRequest` / `GenerateResult`

**Data:** 2026-11-21
**Metodo:** parsing dei due struct in `model.go` e `model_result.go`; la colonna
*Ref. prod* conta le occorrenze statiche di `.<Campo>` nei file non-test del package.
Non è una traccia runtime: un campo scritto in un literal struct o letto via JSON/reflection
può avere meno riferimenti di quanti ne abbia davvero.
**Esito:** `GenerateRequest` 78 campi; `GenerateResult` 67 campi.
Campi classificati: 78/78 e 67/67.
Nessun campo è stato rimosso o rinominato.

## 1. `GenerateRequest` (78 campi)

Fasi: audio=10, editorial=52, finaljob=1, publication=3, render=1, source=6, submission=2, translation=3.

| # | Campo | Tipo | JSON | Fase | Contratto | Ref. prod |
|---:|---|---|---|---|---|---:|
| 1 | `TranslationProvider` | `string` | `translation_provider,omitempty` | translation | `generation.Request` | 1 |
| 2 | `TranslationModel` | `string` | `translation_model,omitempty` | translation | `generation.Request` | 1 |
| 3 | `TranslationConcurrency` | `int` | `translation_concurrency,omitempty` | translation | `generation.Request` | 2 |
| 4 | `Model` | `string` | `model,omitempty` | editorial | `generation.Request` | 2 |
| 5 | `Tone` | `string` | `tone,omitempty` | editorial | `generation.Request` | 1 |
| 6 | `Style` | `string` | `style,omitempty` | editorial | `generation.Request` | 30 |
| 7 | `Render` | `scriptpkg.VideoRenderSpec` | `render,omitempty` | render | `generation.Request` | 66 |
| 8 | `ChannelID` | `string` | `channel_id,omitempty` | editorial | `generation.Request` | 7 |
| 9 | `ShortPhraseMotion` | `string` | `short_phrase_motion,omitempty` | editorial | `generation.Request` | 5 |
| 10 | `PhraseMotionFamily` | `string` | `phrase_motion_family,omitempty` | editorial | `generation.Request` | 4 |
| 11 | `EditorialBulletStyles` | `[]scriptpkg.EditorialBulletStyle` | `editorial_bullet_styles,omitempty` | editorial | `generation.Request` | 6 |
| 12 | `DateMotionFamily` | `string` | `date_motion_family,omitempty` | editorial | `generation.Request` | 4 |
| 13 | `MotionSeed` | `string` | `motion_seed,omitempty` | editorial | `generation.Request` | 4 |
| 14 | `ImageMotions` | `[]string` | `image_motions,omitempty` | editorial | `generation.Request` | 8 |
| 15 | `ImageWithTextMotions` | `[]string` | `image_with_text_motions,omitempty` | editorial | `generation.Request` | 4 |
| 16 | `EntityImageMotions` | `[]string` | `entity_image_motions,omitempty` | editorial | `generation.Request` | 6 |
| 17 | `EntityCaptionMotions` | `[]string` | `entity_caption_motions,omitempty` | editorial | `generation.Request` | 6 |
| 18 | `RandomLightLeakOnClipStart` | `bool` | `random_light_leak_on_clip_start,omitempty` | editorial | `generation.Request` | 4 |
| 19 | `RandomLightLeakOnOverlayStart` | `bool` | `random_light_leak_on_overlay_start,omitempty` | editorial | `generation.Request` | 4 |
| 20 | `RandomImageEffects` | `bool` | `random_image_effects,omitempty` | editorial | `generation.Request` | 4 |
| 21 | `OverlayCategories` | `map[scriptpkg.OverlayCategory]scriptpkg.Toggle` | `overlay_categories,omitempty` | editorial | `generation.Request` | 15 |
| 22 | `OverlayImages` | `[]scriptpkg.OverlayImageAsset` | `overlay_images,omitempty` | editorial | `generation.Request` | 6 |
| 23 | `MapType` | `string` | `map_type,omitempty` | editorial | `generation.Request` | 4 |
| 24 | `MapCameraAnimation` | `string` | `map_camera_animation,omitempty` | editorial | `generation.Request` | 4 |
| 25 | `MapLabelAnimation` | `string` | `map_label_animation,omitempty` | editorial | `generation.Request` | 4 |
| 26 | `MapAnimation` | `string` | `map_animation,omitempty` | editorial | `generation.Request` | 4 |
| 27 | `MapLevel` | `string` | `map_level,omitempty` | editorial | `generation.Request` | 7 |
| 28 | `MapPresentation` | `*scriptpkg.MapPresentation` | `map_presentation,omitempty` | editorial | `generation.Request` | 22 |
| 29 | `MaxPhraseOverlays` | `int` | `max_phrase_overlays,omitempty` | editorial | `generation.Request` | 12 |
| 30 | `MaxOverlaysPerScene` | `int` | `max_overlays_per_scene,omitempty` | editorial | `generation.Request` | 10 |
| 31 | `MapsOnly` | `bool` | `maps_only,omitempty` | editorial | `generation.Request` | 6 |
| 32 | `MaxMapOverlays` | `int` | `max_map_overlays,omitempty` | editorial | `generation.Request` | 10 |
| 33 | `MaxImageOverlays` | `int` | `max_image_overlays,omitempty` | editorial | `generation.Request` | 15 |
| 34 | `EditorialDensity` | `string` | `editorial_density,omitempty` | editorial | `generation.Request` | 6 |
| 35 | `AnimationCounts` | `map[string]int` | `animation_counts,omitempty` | editorial | `generation.Request` | 10 |
| 36 | `OverlayGroupLimits` | `map[string]int` | `overlay_group_limits,omitempty` | editorial | `generation.Request` | 8 |
| 37 | `EntityStyleID` | `string` | `entity_style_id,omitempty` | editorial | `generation.Request` | 10 |
| 38 | `EntityBadgeColor` | `string` | `entity_badge_color,omitempty` | editorial | `generation.Request` | 3 |
| 39 | `DisableNumberOverlays` | `bool` | `disable_number_overlays,omitempty` | editorial | `generation.Request` | 4 |
| 40 | `DisableRecapOverlays` | `bool` | `disable_recap_overlays,omitempty` | editorial | `generation.Request` | 4 |
| 41 | `DisableChartOverlays` | `bool` | `disable_chart_overlays,omitempty` | editorial | `generation.Request` | 5 |
| 42 | `DisableEditorialOverlays` | `bool` | `disable_editorial_overlays,omitempty` | editorial | `generation.Request` | 5 |
| 43 | `EnableCharts` | `bool` | `enable_charts,omitempty` | editorial | `generation.Request` | 5 |
| 44 | `HeavyPhrasePriority` | `float64` | `heavy_phrase_priority,omitempty` | editorial | `generation.Request` | 8 |
| 45 | `OverlayBackground` | `*scriptpkg.OverlayBackgroundSpec` | `overlay_background,omitempty` | editorial | `generation.Request` | 19 |
| 46 | `OverlayStyle` | `*scriptpkg.OverlayStyleSpec` | `overlay_style,omitempty` | editorial | `generation.Request` | 8 |
| 47 | `Audio` | `capabilityaudio.AudioMode` | `audio_mode,omitempty` | audio | `audio.Config` | 62 |
| 48 | `VoiceoverLanguages` | `[]Language` | `voiceover_languages,omitempty` | audio | `audio.Config` | 9 |
| 49 | `Timing` | `*capabilityaudio.TimingRequest` | `voiceover_timing,omitempty` | audio | `audio.Config` | 29 |
| 50 | `RandomSFXOnClipStart` | `bool` | `random_sfx_on_clip_start,omitempty` | audio | `audio.Config` | 6 |
| 51 | `RandomSFXOnImageOverlay` | `bool` | `random_sfx_on_image_overlay,omitempty` | audio | `audio.Config` | 3 |
| 52 | `MixPolicy` | `capabilityaudio.AudioMixPolicy` | `mix_policy,omitempty` | audio | `audio.Config` | 11 |
| 53 | `BackgroundMusic` | `[]scriptpkg.BackgroundMusicIntent` | `background_music,omitempty` | audio | `audio.Config` | 9 |
| 54 | `SoundEffects` | `[]scriptpkg.SoundEffectIntent` | `sound_effects,omitempty` | audio | `audio.Config` | 10 |
| 55 | `IdempotencyKey` | `string` | `idempotency_key` | submission | `—` | 6 |
| 56 | `ForceRefresh` | `bool` | `force_refresh,omitempty` | submission | `—` | 9 |
| 57 | `FinalJob` | `bool` | `final_job,omitempty` | finaljob | `—` | 21 |
| 58 | `Source` | `Source` | `source` | source | `—` | 87 |
| 59 | `MediaMode` | `scriptpkg.MediaMode` | `media_mode,omitempty` | source | `—` | 11 |
| 60 | `StockBindings` | `[]scriptpkg.StockBindingInput` | `stock_bindings,omitempty` | source | `—` | 5 |
| 61 | `ScriptParams` | `scriptpkg.ScriptSpec` | `script_params,omitempty` | editorial | `generation.Request` | 52 |
| 62 | `MediaPlan` | `mediadomain.MediaPlanSpec` | `media_plan,omitempty` | source | `—` | 67 |
| 63 | `ExtractEntities` | `scriptpkg.Toggle` | `extract_entities,omitempty` | source | `—` | 17 |
| 64 | `GenerateSceneImages` | `scriptpkg.Toggle` | `generate_scene_images,omitempty` | source | `—` | 3 |
| 65 | `SourceLanguage` | `Language` | `source_language` | editorial | `generation.Request` | 125 |
| 66 | `Languages` | `[]Language` | `languages` | editorial | `generation.Request` | 35 |
| 67 | `GenerateTimeline` | `bool` | `generate_timeline,omitempty` | editorial | `generation.Request` | 2 |
| 68 | `EnforceClipIdentity` | `bool` | `enforce_clip_identity,omitempty` | editorial | `generation.Request` | 2 |
| 69 | `Docs` | `scriptpkg.DocumentsSpec` | `docs` | publication | `—` | 4 |
| 70 | `SaveToDB` | `bool` | `save_to_db,omitempty` | publication | `—` | 3 |
| 71 | `DocsEnabled` | `bool` | `docs_enabled,omitempty` | publication | `—` | 1 |
| 72 | `Title` | `string` | `title,omitempty` | editorial | `generation.Request` | 21 |
| 73 | `Project` | `string` | `project,omitempty` | editorial | `generation.Request` | 15 |
| 74 | `OutputName` | `string` | `output_name,omitempty` | editorial | `generation.Request` | 5 |
| 75 | `VoiceoverFolderID` | `string` | `voiceover_folder_id,omitempty` | audio | `audio.Config` | 6 |
| 76 | `TTSRate` | `string` | `tts_rate,omitempty` | audio | `audio.Config` | 2 |
| 77 | `Intro` | `*scriptpkg.FixedSection` | `intro,omitempty` | editorial | `generation.Request` | 14 |
| 78 | `Outro` | `*scriptpkg.FixedSection` | `outro,omitempty` | editorial | `generation.Request` | 14 |

## 2. `GenerateResult` (67 campi)

Fasi: audio=9, delivery=1, finaljob=5, overlay=9, publication=1, render=12, runtime=3, scene=5, semantic=6, source=1, text=15.

| # | Campo | Tipo | JSON | Fase | Contratto | Ref. prod |
|---:|---|---|---|---|---|---:|
| 1 | `LanguageDeliveries` | `[]LanguageDeliveryResult` | `language_deliveries,omitempty` | delivery | `—` | 0 |
| 2 | `BinarySHA256` | `string` | `binary_sha256,omitempty` | text | `generation.Result` | 3 |
| 3 | `RustRelease` | `string` | `rust_release,omitempty` | text | `generation.Result` | 1 |
| 4 | `renderFirstStartedAt` | `time.Time` | `-` | runtime | `—` | 6 |
| 5 | `renderLastFinishedAt` | `time.Time` | `-` | runtime | `—` | 5 |
| 6 | `phraseAligner` | `PhraseAligner` | `-` | runtime | `—` | 7 |
| 7 | `Output` | `GenerateOutput` | `output` | text | `generation.Result` | 46 |
| 8 | `Render` | `scriptpkg.VideoRenderSpec` | `render,omitempty` | render | `—` | 66 |
| 9 | `SourceTrace` | `scriptpkg.SourceTrace` | `source,omitempty` | source | `—` | 17 |
| 10 | `Scenes` | `[]Scene` | `scenes` | scene | `—` | 221 |
| 11 | `Summary` | `string` | `summary,omitempty` | text | `generation.Result` | 5 |
| 12 | `BulletPoints` | `[]string` | `bullet_points,omitempty` | text | `generation.Result` | 4 |
| 13 | `HeavySentences` | `[]scriptpkg.ImportantSentence` | `heavy_sentences,omitempty` | text | `generation.Result` | 5 |
| 14 | `ChapterManifest` | `*scriptpkg.ChapterManifest` | `chapter_manifest,omitempty` | text | `generation.Result` | 11 |
| 15 | `SceneHighlights` | `[]scriptpkg.SceneHighlight` | `scene_highlights,omitempty` | text | `generation.Result` | 13 |
| 16 | `Editorial` | `*scriptpkg.EditorialManifest` | `editorial,omitempty` | text | `generation.Result` | 7 |
| 17 | `SemanticBindingIssues` | `[]SemanticBindingIssue` | `semantic_binding_issues,omitempty` | semantic | `—` | 3 |
| 18 | `EntityImageDecisionOwner` | `EntityImageDecisionOwner` | `entity_image_decision_owner,omitempty` | semantic | `—` | 1 |
| 19 | `Segments` | `[]scriptpkg.VidRushSegmentResult` | `segments,omitempty` | scene | `—` | 88 |
| 20 | `OverlayImages` | `[]scriptpkg.OverlayImageAsset` | `overlay_images,omitempty` | overlay | `—` | 6 |
| 21 | `Artifacts` | `*GenerateArtifacts` | `artifacts,omitempty` | render | `—` | 4 |
| 22 | `ResolvedScenes` | `[]ResolvedScene` | `resolved_scenes,omitempty` | scene | `—` | 12 |
| 23 | `CanonicalTimeline` | `*capabilityaudio.CanonicalTimeline` | `canonical_timeline,omitempty` | audio | `audio.Output` | 66 |
| 24 | `AudioPlan` | `*capabilityaudio.CompiledAudioPlan` | `audio_plan,omitempty` | audio | `audio.Output` | 8 |
| 25 | `PhraseTimings` | `[]capabilityaudio.PhraseTiming` | `phrase_timings,omitempty` | audio | `audio.Output` | 4 |
| 26 | `SceneSpeechTimings` | `[]capabilityaudio.SceneSpeechTiming` | `scene_speech_timings,omitempty` | audio | `audio.Output` | 3 |
| 27 | `Entities` | `*scriptpkg.EntityResult` | `entities,omitempty` | scene | `—` | 47 |
| 28 | `ScriptID` | `int64` | `script_id,omitempty` | publication | `—` | 2 |
| 29 | `EntityTimeline` | `*capabilityentities.EntityTimeline` | `entity_timeline,omitempty` | scene | `—` | 30 |
| 30 | `SemanticConsolidation` | `*SemanticVisualConsolidation` | `semantic_consolidation,omitempty` | semantic | `—` | 1 |
| 31 | `SemanticProductionPlan` | `*SemanticProductionPlan` | `semantic_production_plan,omitempty` | semantic | `—` | 10 |
| 32 | `SemanticSearchFeedback` | `*SemanticSearchFeedback` | `semantic_search_feedback,omitempty` | semantic | `—` | 2 |
| 33 | `SemanticSearchReport` | `*SemanticSearchBatchReport` | `semantic_search_report,omitempty` | semantic | `—` | 1 |
| 34 | `OverlayPlan` | `*capmodel.OverlayPlan` | `overlay_plan,omitempty` | overlay | `—` | 173 |
| 35 | `LocalizedOverlayPlans` | `map[Language]*capmodel.OverlayPlan` | `localized_overlay_plans,omitempty` | overlay | `—` | 20 |
| 36 | `PhraseOverlayBudget` | `*capabilityoverlay.PhraseOverlayBudget` | `phrase_overlay_budget,omitempty` | overlay | `—` | 4 |
| 37 | `OverlayPlanIntake` | `*capmodel.OverlayPlanIntake` | `overlay_plan_intake,omitempty` | overlay | `—` | 5 |
| 38 | `SemanticRenderBundle` | `*capabilityoverlay.SemanticRenderBundleV1` | `semantic_render_bundle,omitempty` | overlay | `—` | 7 |
| 39 | `OverlayAudit` | `*OverlayAudit` | `overlay_audit,omitempty` | overlay | `—` | 11 |
| 40 | `FinalJobPreflight` | `*FinalJobPreflight` | `final_job_preflight,omitempty` | finaljob | `—` | 7 |
| 41 | `OverlayRender` | `*RenderReference` | `overlay_render,omitempty` | render | `—` | 48 |
| 42 | `RemoteFinalJob` | `*RemoteFinalJobResult` | `remote_final_job,omitempty` | finaljob | `—` | 22 |
| 43 | `RemoteFinalJobHistory` | `[]RemoteFinalJobResult` | `remote_final_job_history,omitempty` | finaljob | `—` | 2 |
| 44 | `PrepareJoin` | `*scriptrunner.PrepareJoinBreakdown` | `prepare_join,omitempty` | render | `—` | 1 |
| 45 | `LocalizedOverlayRenders` | `map[Language]RenderReference` | `localized_overlay_renders,omitempty` | render | `—` | 9 |
| 46 | `OverlayRenderHistory` | `[]RenderReference` | `overlay_render_history,omitempty` | render | `—` | 6 |
| 47 | `OverlayIntents` | `[]capmodel.OverlayIntent` | `overlay_intents,omitempty` | overlay | `—` | 4 |
| 48 | `ResolvedOverlayIntents` | `[]capmodel.OverlayIntent` | `resolved_overlay_intents,omitempty` | overlay | `—` | 3 |
| 49 | `EditingTimeline` | `*EditingTimelineV1` | `editing_timeline,omitempty` | finaljob | `—` | 1 |
| 50 | `Title` | `string` | `title,omitempty` | text | `generation.Result` | 21 |
| 51 | `SourceLanguage` | `Language` | `source_language,omitempty` | text | `generation.Result` | 125 |
| 52 | `OutputName` | `string` | `output_name,omitempty` | text | `generation.Result` | 5 |
| 53 | `WordCount` | `int` | `word_count` | text | `generation.Result` | 3 |
| 54 | `VoiceoverGroup` | `string` | `voiceover_group,omitempty` | text | `generation.Result` | 4 |
| 55 | `AudioMode` | `capabilityaudio.AudioMode` | `audio_mode,omitempty` | audio | `audio.Output` | 29 |
| 56 | `AudioStrategy` | `capabilityaudio.AudioRenderStrategy` | `audio_strategy,omitempty` | audio | `audio.Output` | 3 |
| 57 | `FinalAudio` | `*FinalAudioReference` | `final_audio,omitempty` | audio | `audio.Output` | 57 |
| 58 | `LocalFinalVideo` | `*LocalFinalVideoArtifact` | `local_final_video,omitempty` | finaljob | `—` | 13 |
| 59 | `AudioMetrics` | `*AudioPipelineMetrics` | `audio_metrics,omitempty` | audio | `audio.Output` | 25 |
| 60 | `TranslationMetrics` | `*TranslationPipelineMetrics` | `translation_metrics,omitempty` | text | `generation.Result` | 1 |
| 61 | `LocalizedRenders` | `[]LocalizedRenderResult` | `localized_renders,omitempty` | render | `—` | 30 |
| 62 | `LocalizedRenderStaged` | `[]LocalizedRenderResult` | `localized_render_staged,omitempty` | render | `—` | 11 |
| 63 | `LocalizedRenderFailures` | `[]LocalizedRenderFailure` | `localized_render_failures,omitempty` | render | `—` | 15 |
| 64 | `RenderMetrics` | `*RenderMetrics` | `render_metrics,omitempty` | render | `—` | 24 |
| 65 | `ExpectedRenderCount` | `int` | `expected_render_count,omitempty` | render | `—` | 5 |
| 66 | `AudioPrefetch` | `*AudioPrefetchResult` | `audio_prefetch,omitempty` | audio | `audio.Output` | 12 |
| 67 | `StageProgress` | `map[string]job.StageProgress` | `stage_progress,omitempty` | render | `—` | 7 |

## 3. Contratti di fase disponibili

- `contracts/generation.Request` — input editoriale, traduzione, canale, lingua, budget,
  motion/overlay policy e identità dell'output. Sono esclusi di proposito source acquisition,
  audio, submission, publication e final job.
- `contracts/generation.Result` — narrazione, takeaway editoriali e provenance.
- `contracts/audio.Config` — modalità audio, voiceover, timing, SFX/BGM, mix policy e
  destinazione voiceover.
- `contracts/audio.Output` — timeline canonica, piano audio compilato, timing del parlato e
  audio finale certificato.
- `contracts/scene` — proiezione sealed della scena (già esistente).
- `kernel/script/finaljob` + `kernel/media/finaljob` — wire e contratto del segmento final job
  (già esistenti, RA-15).

I contratti sono **puri**: non importano `scriptgeneration` e non contengono I/O. La migrazione
dei lettori (step 3 del ticket) non è stata eseguita: i campi piatti restano l'unica fonte di
verità finché ogni fase non ha un accessor e un test di proprietà.

## 4. Campi ancora fuori dai contratti generation/audio

- **source** — `Source`, `MediaMode`, `StockBindings`, `MediaPlan`, `ExtractEntities`,
  `GenerateSceneImages`.
- **submission** — `IdempotencyKey`, `ForceRefresh`.
- **publication** — `Docs`, `DocsEnabled`, `SaveToDB`; nel result `ScriptID`.
- **final job** — `FinalJob`; nel result `FinalJobPreflight`, `RemoteFinalJob`,
  `RemoteFinalJobHistory`, `LocalFinalVideo`, `EditingTimeline`.
- **result scene/overlay/render/semantic** — le fasi restanti della tabella §2.
- **runtime** — i tre campi non serializzati (`renderFirstStartedAt`,
  `renderLastFinishedAt`, `phraseAligner`), che non devono entrare in un contratto wire.

Questi gruppi sono il perimetro delle prossime schede; non vengono dichiarati coperti.
