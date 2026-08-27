# Baseline `20-clip-cold` — num_clips=20, force_refresh=true — benchmark canonico

- Job: `job_1787741752772649756_cf2ad88e` — Run: `run_1787741851825987481_cf2437f8a83f`
- Correlation: `matt-damon-20-clips-baseline-cold-20260826-105515-request`
- Status: SUCCEEDED — num_clips=20, force_refresh=True
- `2026-08-26T10:57:31.825983555Z` → `2026-08-26T11:01:07.02824916Z`

## TOTAL WALL

| Metrica | Valore |
| --- | --- |
| **TOTAL WALL** | **215.2s** |
| wall_time_ms (RunReport) | 215.2s |
| queue_wait | 99.0s |
| attributed stage | 214.7s |
| unattributed | 536ms (0.25%)
| bottleneck | generate / ollama.generate |

## STAGES (wall, aggregati per nome)

| Stage | wall | # |
| --- | --- | --- |
| tts | 76.6s | 20 |
| generate | 71.8s | 1 |
| post_writer_finalize | 68.5s | 1 |
| audio_compile | 60.3s | 1 |
| document | 14.1s | 1 |
| document.publish | 14.0s | 1 |
| checkpoint | 103ms | 9 |
| complete_finalize | 34ms | 1 |
| voiceover | 22ms | 1 |
| persistence | 22ms | 1 |
| document.prepare | 4ms | 1 |
| normalize | 1ms | 1 |
| begin_vidrush | 0ms | 1 |
| media_preflight_join | 0ms | 1 |
| translation | 0ms | 1 |
| publish_pool_drain | 0ms | 1 |
| prepare_join | 0ms | 1 |

## OPERATIONS (accumulati per stage/component/operation)

| Operazione | tot ms | # | queue ms | items |
| --- | --- | --- | --- | --- |
| generate/ollama/generate | 239300 | 20 | 6492 | 20 |
| voiceover/tts/synthesize | 76609 | 20 | 0 | 0 |
| audio_compile/rust/audio_render | 53912 | 1 | 0 | 0 |
| audio_compile/audio/mix | 24590 | 1 | 0 | 0 |
| audio_compile/audio/aac_encode | 23016 | 1 | 0 | 0 |
| document.publish/google_docs/publish | 13998 | 1 | 0 | 0 |
| audio_compile/drive/upload | 6085 | 1 | 0 | 0 |
| audio_compile/audio/probe | 284 | 1 | 0 | 0 |
| audio_compile/audio/audio_plan_compile | 180 | 1 | 0 | 0 |
| audio_compile/audio/hash | 158 | 1 | 0 | 0 |

## AUDIO

| Metrica | ms |
| --- | --- |
| tts_ms | 76609 |
| media_fetch_ms | 0 |
| timeline_compile_ms | 0 |
| audio_plan_compile_ms | 180 |
| clip_audio_prepare_ms | 0 |
| mix_ms | 24590 |
| aac_encode_ms | 23016 |
| probe_ms | 284 |
| hash_ms | 158 |
| upload_ms | 6085 |
| total_ms | 53912 |

| Campo | Valore |
| --- | --- |
| audio_duration_ms | 1022887 |
| tts_calls | 20 |
| audio_rtf | 0.052705724092690594 |
| audio_speed | 18.973271256863036 |
| audio_encode_passes | 1 |

## LLM / DOCS / ARTIFACTS

- LLM (translation_metrics): {'calls': 0, 'concurrency': 4, 'wall_ms': 0}
- Docs en link: https://docs.google.com/document/d/1Fd_u7OU3LIUjMIc7GGW4QfdjfpvMEUD0QKSKyEnnTJQ/edit
- Artifacts: 23 → {'script_json': 1, 'scenes': 1, 'voiceover': 20, 'final_audio': 1}
- Final audio: aac 48000Hz 2ch, 1022887ms, 16789904B, bitrate 131313
