# Baseline `10-clip-cold` — num_clips=10, force_refresh=true — scaling con N scene

- Job: `job_1787742604386297795_7b0af271` — Run: `run_1787742607243524793_75280c6ea622`
- Correlation: `matt-damon-10-clips-baseline-cold-20260826-105515-request`
- Status: SUCCEEDED — num_clips=10, force_refresh=True
- `2026-08-26T11:10:07.243516886Z` → `2026-08-26T11:12:07.96719736Z`

## TOTAL WALL

| Metrica | Valore |
| --- | --- |
| **TOTAL WALL** | **120.7s** |
| wall_time_ms (RunReport) | 120.7s |
| queue_wait | 3.0s |
| attributed stage | 120.3s |
| unattributed | 377ms (0.31%)
| bottleneck | post_writer_finalize / None |

## STAGES (wall, aggregati per nome)

| Stage | wall | # |
| --- | --- | --- |
| post_writer_finalize | 40.8s | 1 |
| generate | 38.5s | 1 |
| tts | 34.9s | 10 |
| audio_compile | 34.0s | 1 |
| document | 7.0s | 1 |
| document.publish | 6.8s | 1 |
| checkpoint | 100ms | 9 |
| persistence | 44ms | 1 |
| complete_finalize | 14ms | 1 |
| voiceover | 13ms | 1 |
| normalize | 4ms | 1 |
| document.prepare | 3ms | 1 |
| translation | 1ms | 1 |
| begin_vidrush | 0ms | 1 |
| media_preflight_join | 0ms | 1 |
| publish_pool_drain | 0ms | 1 |
| prepare_join | 0ms | 1 |

## OPERATIONS (accumulati per stage/component/operation)

| Operazione | tot ms | # | queue ms | items |
| --- | --- | --- | --- | --- |
| generate/ollama/generate | 115507 | 10 | 6021 | 10 |
| voiceover/tts/synthesize | 35010 | 10 | 0 | 0 |
| audio_compile/rust/audio_render | 29336 | 1 | 0 | 0 |
| audio_compile/audio/aac_encode | 15150 | 1 | 0 | 0 |
| audio_compile/audio/mix | 10398 | 1 | 0 | 0 |
| document.publish/google_docs/publish | 6738 | 1 | 0 | 0 |
| audio_compile/drive/upload | 4314 | 1 | 0 | 0 |
| audio_compile/audio/audio_plan_compile | 289 | 1 | 0 | 0 |
| audio_compile/audio/probe | 239 | 1 | 0 | 0 |
| audio_compile/audio/hash | 64 | 1 | 0 | 0 |

## AUDIO

| Metrica | ms |
| --- | --- |
| tts_ms | 35010 |
| media_fetch_ms | 0 |
| timeline_compile_ms | 0 |
| audio_plan_compile_ms | 289 |
| clip_audio_prepare_ms | 0 |
| mix_ms | 10398 |
| aac_encode_ms | 15150 |
| probe_ms | 239 |
| hash_ms | 64 |
| upload_ms | 4314 |
| total_ms | 29336 |

| Campo | Valore |
| --- | --- |
| audio_duration_ms | 619760 |
| tts_calls | 10 |
| audio_rtf | 0.047334452045953275 |
| audio_speed | 21.126261248977364 |
| audio_encode_passes | 1 |

## LLM / DOCS / ARTIFACTS

- LLM (translation_metrics): {'calls': 0, 'concurrency': 4, 'wall_ms': 0}
- Docs en link: https://docs.google.com/document/d/1WCYzeBSNFG2kOXlokI5UpkBHygOk6c0w81Y44Dfy9Rs/edit
- Artifacts: 13 → {'script_json': 1, 'scenes': 1, 'voiceover': 10, 'final_audio': 1}
- Final audio: aac 48000Hz 2ch, 619760ms, 10152857B, bitrate 131055
