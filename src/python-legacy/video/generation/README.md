# Video Generation – modulo orchestrator

Cartella che contiene la pipeline di generazione video modularizzata (estratta da `Generatevideoparallelized`).

## Struttura

```
generation/
├── orchestrator.py      # Cervello: crea il Context ed esegue le fasi in ordine
├── common/
│   ├── context.py      # Stato condiviso (paths, config, runtime)
│   └── helpers.py      # Utility (map_audio_to_video, get_stock_clip_for_overlay, ecc.)
├── phases/             # Fasi sequenziali della pipeline
│   ├── initialization.py
│   ├── audio.py
│   ├── intros.py
│   ├── segments.py     # Segmenti stock + middle clips
│   ├── assembly.py     # Concatenazione base video
│   ├── overlays.py     # Gestione entità (testo, immagini, sottotitoli)
│   └── finalization.py # Merge overlays + cleanup
└── entities/           # Handler per tipo di entità overlay
    ├── base.py
    ├── manager.py      # Dispatcher per categoria
    ├── frasi_importanti.py
    ├── nomi_speciali.py
    ├── nomi_con_testo.py
    ├── numeri.py
    ├── date.py
    ├── parole_importanti.py
    ├── entita_senza_testo.py  # Immagini
    └── subtitles.py
```

## Flusso (orchestrator.run)

1. **InitializationPhase** – Valida input, crea temp dir.
2. **AudioPhase** – Legge durata audio, la salva in `ctx.audio_duration`.
3. **IntrosPhase** – Calcola durata start/middle clips, aggiorna `ctx.total_start_duration` e `ctx.middle_clip_actual_durations`.
4. **SegmentsPhase** – Calcola segmenti, genera clip stock in parallelo, popola `ctx.stock_segment_results` e `ctx.stock_tasks_args_list`.
5. **AssemblyPhase** – Concatena start + (segmenti + middle) + end, eventuale musica, restituisce `base_video_path`.
6. **OverlayPhase** – Usa `EntityManager` e handler (Frasi, Nomi, Date, Numeri, Parole, Immagini, Subtitles), popola `ctx.all_rendered_overlay_files_for_ffmpeg_merge`.
7. **FinalizationPhase** – Merge overlays su base video, cleanup, restituisce il path del video finale.

## Entry point

`Generatevideoparallelized.generate_video_parallelized()` costruisce `VideoGenerationOrchestrator` con i parametri ricevuti e chiama `orchestrator.run()`.

## Dipendenze esterne (modules.video)

- `video_audio._generate_voiced_stock_segment_task` (SegmentsPhase)
- `ffmpeg_utils.concatenate_videos_fast`, `get_ffmpeg_processor` (AssemblyPhase)
- `video_ffmpeg.merge_overlays_ffmpeg` (FinalizationPhase)
- `remotion_renderer`, `remotion_style_manager` (dagli entity handler, via config overlay_engine)
