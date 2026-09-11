# `/api/clips/render` — benchmark 2026-09-11

> Superseded by [`clip-render-final-20260911.md`](clip-render-final-20260911.md):
> the earlier “OK” labels were timing-only and did not include the later PNG
> visual certification. Watermark/background outputs from that first battery
> are not certified as visually correct.

## Ambiente

- Sorgente principale: `yt_T6x-kDiQsWM_203_252_v1`, 49 s, 1176 frame.
- Output: 1920x1080, 24 fps, H.264/NVENC.
- PipelineGen live ricostruito con il prefetch asset concorrente.
- Canary RenderingGen: 3 pipeline worker, 6 GPU lane configurate, Chronon via IPC,
  preset NVENC p1, workspace/cache in `/dev/shm`.
- Il worker systemd originale è rimasto attivo a 2 lane, `/tmp`, preset p2:
  non è stato sostituito perché l'host richiede una password sudo per installare
  il nuovo binario/config. È stato sospeso solo durante i test canary e poi riattivato.

## Render singoli

| Caso | Esito | total_ms | render_wall_ms | render_loop_ms | fps loop | realtime | fallback | readback | conversioni |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Plain, no overlay | OK | 4,662 | 4,108 | 1,595 | 737.5 | 10.5x | 0 | 0 B | 0 |
| Plain + subtitles sidecar | OK | 9,560* | 4,143 | 1,600 | 735.2 | 5.1x* | 0 | 0 B | 0 |
| Text watermark | OK | 28,292 | 27,876 | 22,817 | 51.5 | 1.73x | 0 | 0 B | 1176 NV12→RGBA + 1176 RGBA→NV12 |
| Image watermark | OK | 30,022 | 29,662 | 21,090 | 55.8 | 1.63x | 0 | 0 B | 1176 NV12→RGBA + 1176 RGBA→NV12 |
| Subtitles burn-in | OK | 41,708 | 41,227 | 35,530 | 33.1 | 1.17x | 0 | 0 B | 1176 NV12→RGBA + 1176 RGBA→NV12 |
| Background video | OK | 44,098 | 43,263 | 34,889 | 33.7 | 1.11x | 0 | 0 B | 2352 NV12→RGBA + 1176 RGBA→NV12 |

`*` Il totale sidecar include circa 4,928 ms di upload Drive; il rendering
GPU resta circa 4,1 s.

Tutti i casi riusciti hanno backend `chronon_vulkan`, NVENC e zero fallback
software/readback. Il fast path plain è quindi già molto veloce; il costo è
nel passaggio da DirectYUV al compositor quando compare testo, immagine o una
seconda sorgente video.

## Concorrenza canary 6-lane

| Job simultanei | Completati | Falliti | Batch wall | Risultato |
|---:|---:|---:|---:|---|
| 1 | 1 | 0 | 7 s | OK, 6.3 s/job |
| 2 | 2 | 0 | 11 s | OK, 6.0 s e 10.1 s: due sessioni in onda |
| 4 | 2 | 2 | 11 s | gli altri ricevono `no device satisfies requested capabilities` |
| 6 | 2 | 4 | 11 s | gli altri ricevono `no device satisfies requested capabilities` |

Conclusione: su questa RTX A4000 e sul daemon IPC attuale, il limite effettivo
è 2 sessioni GPU contemporanee. Portare `gpu_lanes` a 4 o 6 non aumenta il
throughput e introduce errori; il valore operativo corretto è 2 finché non
si aumenta la capacità del daemon/GPU.

## Errori trovati e correzioni applicate al codice

1. Burn subtitles non inviava sempre un font materializzato. Ora Montserrat o
   Poppins viene incluso negli asset del job.
2. Watermark immagine richiedeva erroneamente `font_ref`; ora il font è
   obbligatorio solo per watermark testuale.
3. Layer source/watermark mancanti di `size` facevano fallire la validazione
   dello schema Chronon nel full graph.
4. Due layer video venivano classificati come DirectYUV; ora la selezione
   richiede il compositor quando esistono più sorgenti video.
5. Il prefetch PipelineGen usa probe/upload concorrenti con limite 4.

Test unitari eseguiti:

```text
go test ./internal/platform/renderinggen ./internal/capabilities/cliprender -count=1
go test ./renderinggen/internal/overlay ./renderinggen/internal/processor -count=1
```

## Da sistemare per la massima velocità

- Il burn-in sottotitoli e il watermark devono avere un fast path GPU che
  mantenga superfici native; oggi 1176 conversioni per frame fanno scendere
  il throughput a 33–56 fps.
- Il background blur usa `fit=blur_cover`, ma lo schema Chronon accetta solo
  `cover|contain|stretch|none`: il caso `/api/clips/render` blur è quindi
  ancora non certificabile e deve essere implementato come effetto compositor
  reale o come asset blur precomputato GPU.
- Il background video deve essere almeno lungo quanto la clip o avere looping
  effettivo; un asset più corto ha causato EOF al frame 450.
- Le metriche `chronon_queue_wait_ms`, `chronon_service_ms`, decode/composite e
  GPU/NVENC utilization risultano ancora `NOT_INSTRUMENTED`; sono necessarie
  per separare attesa IPC, compositing e encode.
- Il worker systemd va riallineato al canary con una procedura privilegiata,
  ma solo dopo una prova operativa mantenendo `gpu_lanes=2`; p1 e `/dev/shm`
  sono candidati al deploy, non ancora certificati come miglioramento single-job.

## Output Drive

Gli output video sono stati caricati nella cartella indicata dall'utente:

- `render-test-plain-sidecar-20260911.mp4`
- `render-test-watermark-image-49s-20260911.mp4`
- `render-test-background-video-49s-20260911.mp4`
- `render-test-subtitles-burn-49s-20260911.mp4`
