# `/api/clips/render` — verifica visiva e performance finale — 2026-09-11

## Stato

La verifica visiva è stata eseguita estraendo frame PNG dai file MP4 prodotti
dall'endpoint. Il video plain è corretto: sorgente centrata, 1920×1080, 24 fps,
49 s, H.264/AAC. Il watermark testuale non è certificato: il fast path GPU lo
renderizza come frammenti/pixel e non come testo leggibile. Non viene pubblicato
come artefatto valido.

## Modifiche applicate

- corretto il centro geometrico del layer foreground quando
  `foreground_scale_percent < 100`;
- corretto il riconoscimento delle trasformazioni affini nel native renderer;
- aggiunte guardie fail-closed per trasformazioni native non valide;
- il rasterizer ignora glyph advance-only, come lo spazio, invece di fallire;
- il mapper rispetta la famiglia font Poppins quando richiesta;
- il CUDA text compositor usa la signed-distance alpha canonica e una soglia
  stabile per il fill DirectYUV;
- ricompilato Chronon con `cmake --build ... --target chronon3d_cli -j20`;
- avviato il server PipelineGen ricompilato in verifica su porta locale 18000.

## Render plain API certificato

Job: `job_1789147377163411663_9832e044`

| Metrica | Valore |
|---|---:|
| totale | 4,887 ms |
| render | 3,666 ms |
| render loop | 1,531 ms |
| render loop FPS | 767.9 |
| realtime factor | 26.4× |
| frame | 1,176 |
| NVENC frames | 1,176 |
| decode native surface frames | 1,176 |
| NV12→RGBA / RGBA→NV12 | 0 / 0 |
| software fallback / readback | 0 / 0 B |

Il PNG verificato è `api-plain-fast-t02.png`.

## Concorrenza plain — quattro job simultanei

Tutti i job sono completati senza errore sul worker `renderinggen-verify-20260911`.
Il piano è però chiaramente limitato dalla contesa/scheduling: il render loop
rimane circa 1.52–1.56 s per job, mentre il tempo totale cresce.

| Job | Totale | Render | Job wall | Completamento relativo |
|---|---:|---:|---:|---:|
| `3f1dc7d6` | 4,761 ms | 3,571 ms | 1,861 ms | 4.8 s |
| `a34703c7` | 8,412 ms | 7,159 ms | 1,863 ms | 8.4 s |
| `6ce8db2b` | 12,112 ms | 7,234 ms | 1,842 ms | 12.1 s |
| `c9dcacfa` | 15,541 ms | 7,233 ms | 1,841 ms | 15.5 s |

Conclusione operativa: non aumentare le lane GPU oltre 2. Quattro richieste
non aumentano il throughput in modo lineare e accumulano attesa/contesa fuori
dal puro loop.

## Watermark e background — non certificati

- Watermark Poppins `640×100`, DirectYUV: completato in circa 5–6.6 s, ma il
  frame estratto mostra testo frammentato; il difetto è visivo e blocca la
  pubblicazione.
- Watermark Montserrat con spazio: prima falliva sul glyph senza outline;
  questo specifico crash è stato corretto, ma il risultato resta frammentato.
- Background asset `yt_S6ADB98CR7g_358_425_v1`: fallisce prima di produrre un
  MP4. Il log mostra `DirectCudaYuv kernel dispatch failed` al frame 0: il
  piano con due sorgenti viene ancora instradato nel percorso DirectYUV, che
  deve essere riservato a una sola sorgente o implementare un compositor
  multi-source YUV reale.
- Burn-in subtitles e FullGraph con shadow/stroke restano bloccati dal native
  output `source surface is uninitialized`/percorso FullGraph, quindi non sono
  da dichiarare né veloci né visivamente certificati.

## Colli di bottiglia rimasti

1. Correggere il planner multi-source: background + foreground deve selezionare
   FullGraph valido oppure un compositor YUV a due sorgenti; mai DirectYUV
   singolo-source.
2. Sostituire il raster MTSDF del testo API con una coverage mask/atlas
   verificata, oppure correggere definitivamente il sampling CUDA: il testo
   non può essere promosso solo sulla base del tempo.
3. Riparare/certificare il native FullGraph per shadow, stroke, subtitles e
   compositing; solo dopo misurare blur/background.
4. Aggiungere `chronon_queue_wait_ms`, `chronon_service_ms`, decode/composite/
   encode wait e GPU/NVENC utilization: oggi parte della perdita concorrente
   è inferita dai timestamp, non osservata direttamente.
5. Allineare il deploy systemd al binario/config canary con procedura
   privilegiata; il canary usa 2 GPU lane, `/dev/shm`, NVENC p1.

## Verdict

Il plain/native GPU path è già vicino al limite pratico della macchina. Il
sistema complessivo `/api/clips/render` non è ancora “alla massima velocità”:
watermark, sottotitoli e background devono prima essere visivamente corretti.
Ottimizzare ulteriormente il plain path ora avrebbe ritorno marginale.
