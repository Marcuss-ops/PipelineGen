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

---

## Stato 2026-09-15 — watermark, sottotitoli e background: certificati

Il verdetto del 2026-09-11 su watermark, sottotitoli e background è SUPERATO.
Gli stessi tre elementi, che allora erano frammentati o bloccati, ora si
compongono in un solo passaggio Chronon Vulkan e sono verificati sui byte dei
MP4 pubblicati.

### Payload certificato

`background={mode:asset, asset_id:classic1, kind:video}` + `subtitles={enabled,
burn}` + `watermark={enabled, text:"VeloxEditing", position:top_right}` +
`execution={require_gpu:true}` + `output.foreground_scale_percent:80`, destinazione
`1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`.

Il plate "Pale Olive Classic" è l'asset `classic1`: `media_type='video'`,
`data/backgrounds/classic1.mp4`, colore misurato RGB(237,237,232). NON è una
immagine: un `background.kind=image` su questo id è un errore di produttore.

### Trappola: `background.mode=asset` è invisibile al default

`normalizeForegroundScale(0) == 100` e il mapper emette il campo solo se `<100`.
A 100 il sorgente copre il canvas intero: il plate viene materializzato,
hash-ato e sigillato nel piano, ma non compare in nessun pixel. Un primo batch
con il default è stato prodotto e verificato (0 pixel Pale Olive in ogni
regione); tutti gli artefatti consegnati usano 80, come i fixture canonici
(`ops/jobs/*.generate.json`, `ops/benchmarks/clip-render-background-payload.json`).
Contratto ora pinnato da `TestCompile_ForegroundScaleGatesBackgroundVisibility`.

### Verifica visiva — metodo ed esito

Ogni riga è misurata sui byte del MP4 pubblicato, mai dedotta dal log.

| Elemento | Metodo | Esito |
|---|---|---|
| Background visibile | margini puri (fuori dal foreground all'80%) confrontati con il plate | plate ink=0; margini del render RGB 228-241 = plate |
| Sottotitoli burn | A/B a parità di tutto tranne `subtitles.enabled` | 5.35-7.65% di pixel cambiati in bottom-center, `max_delta` 607-723; il margine di solo background cambia 0 pixel |
| Watermark testo | A/B a parità di tutto tranne `watermark.enabled` | ink 229x36 px a frame x 1545..1773, y 136..171; 4118 px di differenza SOPRA il video (x<1728) e 532 sul plate; margine sinistro e fascia x1600..1900/y0..100 = 0 ink |
| Contratto output | ffprobe su tutti e 5 | h264 Main / yuv420p / 1920x1080 / 24-1 fps / aac LC 48000 Hz 2ch / 2 stream |

Il watermark è quindi composto SOPRA il foreground (non occluso) e resta
nell'angolo alto-destro: l'assenza di ink nel margine sinistro e nella fascia
superiore esclude `top_left`, `center` e le posizioni basse.

Limite dichiarato: la certificazione è statistica di regione + diff A/B
(presenza di glifi, struttura di stem, assenza di ink dove non deve esserci).
Non sostituisce un'ispezione umana della leggibilità dei glifi.

### Metriche GPU — 5 clip concorrenti su 1x RTX A4000 16 GB

`backend=chronon_vulkan` su tutte e 5, `fallback_count=0`,
`verification_passed=1` (policy 1 = fast).

| sorgente | frame | render wall | render loop | encode | prepare | queue wait | speed | render fps |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 17 s | 408 | 4 519 ms | 1 850 ms | 154 ms | 933 ms | 9 ms | 3.76x | 220.5 |
| 19 s | 456 | 9 058 ms | 2 274 ms | 232 ms | 856 ms | 16 ms | 2.10x | 200.5 |
| 34 s | 816 | 16 577 ms | 5 485 ms | 277 ms | 811 ms | 4 473 ms | 2.05x | 148.8 |
| 49 s | 1 176 | 28 257 ms | 8 558 ms | 453 ms | 819 ms | 8 904 ms | 1.73x | 137.4 |
| 67 s | 1 608 | 45 050 ms | 14 187 ms | 335 ms | 938 ms | 16 351 ms | 1.49x | 113.3 |

Aggregati: 4 464 frame, 103.46 s di render work, batch wall 45.84 s su 186 s di
media = **4.06x realtime**, parallelismo effettivo **2.26x**, throughput di
compositing GPU **138 fps**.

`chronon_queue_wait_ms` domina la varianza fra le clip (9 ms -> 16 351 ms): con 5
render su una GPU il collo di bottiglia è l'ammissione, non il loop. Il render
loop è quasi identico fra fg=100 e fg=80, quindi scalare il foreground non
cambia il costo del plate. Coerente con la conclusione operativa del
2026-09-11: non alzare le lane GPU oltre 2.

### Livello HTTP

| Metrica | Valore |
|---|---:|
| `POST /api/clips/render/batch` (accettazione 5 item) | 31.8 ms |
| `POST /api/clips/render` (singolo) | 49 ms |
| Latenza handler, stub senza I/O | 194 us |
| Latenza media su 100 chiamate | 55 us |

### Artefatti su Drive

Tutti e 5 presenti in `1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`, non trashed,
`modifiedTime` 06:35:10-06:35:52 (consegna asincrona via outbox, nessun upload
sincrono). Dimensione e `md5Checksum` di ogni file Drive corrispondono
esattamente agli artefatti di questo run (es. 2 2354937 B / `0712b3d7...` e
5 470187 B / `98f08a58...` verificati anche in locale).

| asset | sorgente | durata | Drive file id |
|---|---|---:|---|
| `cliprender_0eca432bc5836eb05f151392` | yt_Gcgdk1gEo8U_285_302_v1 | 17 s | 1bgUjtrfC0B5PoqTT8LQiDuUtTxvU6tVn |
| `cliprender_c1f237c78842078c9c42f80c` | yt_0ElQTzSx3ec_72_91_v1 | 19 s | 1JXTYHC5wOS4i8UYoUlu-STcWV0CTpOOV |
| `cliprender_2f2bfe9ef0941dea6ff67358` | yt_ERzbkt5r5Gg_32_66_v1 | 34 s | 17CoEOXZGd0V9XZE6kgVH3wna78vs8skO |
| `cliprender_20ab57972e294c0a5e4f1c10` | yt_T6x-kDiQsWM_203_252_v1 | 49 s | 1Mvb765rjiy2OL7j-KKJsA1n5uhPZsyO8 |
| `cliprender_8f002cd68eb3374902cf71f0` | yt_S6ADB98CR7g_358_425_v1 | 67 s | 1wIYUbRVudhsyJI5S2-ryTazgXPLhl5US |

Il render è deterministico: lo stesso payload, rieseguito in un secondo momento,
ha riprodotto gli stessi byte (stesso `content_sha256` -> stesso asset id, stessa
dimensione). `execution.require_gpu` fa parte del fingerprint canonico, quindi
un artefatto prodotto senza quel requisito non può soddisfare una richiesta che
lo esige.

### Difetto rilevato, non corretto: la consegna Drive non è content-addressed

Due `content_sha256` distinti (`0eca432bc5836eb0` e `d7aa611376b18072`) sono
finiti sullo STESSO `drive_file_id` nella stessa cartella: un re-render con lo
stesso `filename` verso la stessa cartella riusa/sovrascrive il file Drive e
ri-punta anche la riga d'asset precedente. L'ultima scrittura vince (qui i byte
corretti sono quelli dell'ultimo batch), ma il puntatore storicizzato della riga
precedente diventa stale. La mitigazione già prevista dal contratto è
`destination.subfolder_name`, che namespacea il batch: usarlo quando si
ri-renderizza lo stesso clip nella stessa cartella radice.

Nota operativa: `GET /ready` segnala `drive_canary` e `drive_root` in timeout
(`context deadline exceeded`) mentre la consegna reale dei clip completa
regolarmente (124+ delivery `completed`, ~8 s ciascuna). Le due sonde hanno un
budget più stretto dell'upload reale e non vanno lette come "Drive rotto".

---

## Verdict aggiornato (2026-09-15)

Il vincolo che bloccava il verdetto del 2026-09-11 — "watermark, sottotitoli e
background devono prima essere visivamente corretti" — è rimosso: tutti e tre
sono composti correttamente e verificati sui byte in un unico passaggio GPU.
Il limite residuo non è piu' la correttezza visiva ma l'ammissione GPU: con 5
render concorrenti su una sola GPU il `chronon_queue_wait` arriva a 16 s e il
parallelismo effettivo si ferma a 2.26x. Il percorso plain/native resta al
limite pratico della macchina, come già concluso a settembre.

### Sweep completo del catalogo (2026-09-15, mattina)

Obiettivo: rendere **ogni** clip del media DB con trascrizione READY, non solo le
5 del primo batch, con lo stesso payload canonico (plate Pale Olive
`classic1`, sottotitoli bruciati, watermark testuale `top_right`,
`foreground_scale_percent=80`, `execution.require_gpu=true`,
destination = `1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`).

12 sorgenti hanno una trascrizione READY. **9 renderizzate e consegnate, 3
bloccate** da un difetto reale del percorso audio.

| # | sorgente | durata | esito | asset | note |
|---:|---|---:|---|---|---|
| 1 | yt_Gcgdk1gEo8U_285_302_v1 | 17 s | OK | `cliprender_0eca432bc5836eb0` | |
| 2 | yt_0ElQTzSx3ec_72_91_v1 | 19 s | OK | `cliprender_c1f237c78842078c` | |
| 3 | yt_ERzbkt5r5Gg_32_66_v1 | 34 s | OK | `cliprender_2f2bfe9ef0941dea` | |
| 4 | yt_T6x-kDiQsWM_203_252_v1 | 49 s | OK | `cliprender_20ab57972e294c0a` | |
| 5 | yt_S6ADB98CR7g_358_425_v1 | 67 s | OK | `cliprender_8f002cd68eb33749` | |
| 6 | 1C8v8v-FzLK0gOpGnP4Lu-TveDWaU6Rt1 | 7 s | OK | `cliprender_1088f53386b68e59` | sorgente Drive-only |
| 7 | 1lkmvsdJe6S0wFCEMJJJewrYlOfQ-xIpR | 11 s | OK | `cliprender_55399f95d3be4e30` | sorgente Drive-only |
| 8 | 1Q-nkAuu12_qX0sZ95jk5Gvl6fCBDq0FJ | 13 s | OK | `cliprender_09ef1f611e7a8c4d` | sorgente Drive-only |
| 9 | yt_TMzcXxQYGOk_0_60_v1 | 60 s | OK | `cliprender_bd0cfcd1202707ad` | whisper-cert |
| 10 | 18Amwc3aF8I5jjo9R1-PPsOh0vvx94Jl3 | 12.75 s | **BLOCCATA** | — | audio sorgente 44.1 kHz |
| 11 | 1K4X2QyNOwi0ksx-1Dfr51hW1QjisE4sy | 17.88 s | **BLOCCATA** | — | audio sorgente 44.1 kHz |
| 12 | 1I4JtZRMQw6PQpUw_Dv4mxPOZse3DKBZm | 21.71 s | **BLOCCATA** | — | audio sorgente 44.1 kHz |

Le sorgenti "Drive-only" (senza byte locali, solo `drive_file_id`) **sono
renderizzabili**: il materializzatore le scarica e il render procede
normalmente su `chronon_vulkan`. La raggiungibilità era un'ipotesi, non un
blocco.

### Difetto bloccante: l'audio non viene mai ricampionato

Le 3 sorgenti bloccate falliscono tutte con lo stesso errore tipizzato e
riproducibile (batch di retry incluso, anche con `audio.mode=transcode`):

```
clip.render: rendered output violates the output contract:
  audio timebase 1/44100 != 1/48000
```

Causa radice, confermata nel codice e sui byte: Chronon muxa l'audio della
sorgente **copiandolo** (`avcodec_parameters_copy` + copia del `time_base`),
non lo transcodifica. RenderingGen lo dichiara esplicitamente —
`render_selection.go` (`audioModeCopyOnly`, *"the native A/V path copies the
source stream and never transcodes"*) e `pipeline.go:401` (*"audio
codec/sample_rate/channels are inert (Chronon copies source audio, no
transcode)"*), registrando `AudioModeUnsupported` quando il chiamante chiede
`transcode`. Chronon3d non linka `libswresample` da nessuna parte
(`grep -i swresample|aresample` → 0 hit), quindi non esiste oggi alcuno stadio
che possa cambiare il sample rate.

Correlazione sui byte, 6/6 sorgenti (ffprobe diretto sulle sorgenti Drive):

| sorgente | esito | codec | sample_rate | timebase |
|---|---|---|---:|---|
| 18Amwc3aF8I5jjo9R1-PPsOh0vvx94Jl3 | BLOCCATA | aac | 44100 | 1/44100 |
| 1K4X2QyNOwi0ksx-1Dfr51hW1QjisE4sy | BLOCCATA | aac | 44100 | 1/44100 |
| 1I4JtZRMQw6PQpUw_Dv4mxPOZse3DKBZm | BLOCCATA | aac | 44100 | 1/44100 |
| 1lkmvsdJe6S0wFCEMJJJewrYlOfQ-xIpR | OK | aac | 48000 | 1/48000 |
| 1Q-nkAuu12_qX0sZ95jk5Gvl6fCBDq0FJ | OK | aac | 48000 | 1/48000 |
| 1C8v8v-FzLK0gOpGnP4Lu-TveDWaU6Rt1 | OK | aac | 48000 | 1/48000 |

Il sample rate della sorgente è l'unico discriminatore: 44.1 kHz → violazione,
48 kHz → successo. Con le 5 matt-damon (tutte 48 kHz) la regola è 12/12.

Il gate ha fatto la cosa giusta: **fail-closed**. Per le 3 sorgenti bloccate
questo sweep non ha pubblicato nessun asset (`count(*) = 0` con
`created_at >= 2026-09-15 06:20`), quindi nessun artefatto non conforme è
arrivato su Drive. Il comportamento è ora pinnato da
`TestAudioTimebaseRejectsNonContractSampleRate`
(`internal/capabilities/cliprender/contract_registry_test.go`), che verifica il
dettaglio tipizzato esatto e l'assenza di qualunque whitelist di tolleranza su
questa dimensione.

### Cartella di destinazione: contenuto verificato

`1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K` contiene 9 clip + 1 sottocartella run
(`PaleOlive Render 20260915T062102Z`):

| file | byte | md5 | modificato |
|---|---:|---|---|
| matt-damon-000445-000502.mp4 | 5 470 187 | `98f08a58...` | 06:35:10 |
| matt-damon-000112-000131.mp4 | 6 906 900 | `542adfe9...` | 06:35:15 |
| matt-damon-000032-000106.mp4 | 13 389 929 | `40801253...` | 06:35:23 |
| matt-damon-000323-000412.mp4 | 19 292 154 | `bd3410d9...` | 06:35:36 |
| matt-damon-000558-000705.mp4 | 22 354 937 | `0712b3d7...` | 06:35:52 |
| cliprender_1088f53386b68e59fa23a031.mp4 | 2 184 610 | `f28ed7a0...` | 06:45:26 |
| cliprender_55399f95d3be4e30138acaef.mp4 | 3 834 813 | `2a554b6e...` | 06:45:46 |
| cliprender_09ef1f611e7a8c4d9c23f5d4.mp4 | 3 800 615 | `4fb33dcb...` | 06:45:54 |
| whisper-cert-first-minute.mp4 | 17 095 383 | `5ac6d0eb...` | 06:46:16 |

Tutti e 9 gli artefatti dello sweep corrispondono per **md5 + dimensione** ai
byte renderizzati (9/9), e tutti rispettano il contratto
`h264 Main / yuv420p / 1920x1080 / 24 fps / aac 48000 Hz 2ch / 2 stream`.
**Zero cache hit**: tutti i 19 job sottomessi nei 4 batch sono tornati
`QUEUED` (mai `CACHED`), e la tabella `clip_render_cache` è cresciuta di 21
righe nella sessione — ogni render è lavoro GPU reale.

### Decisione aperta (prodotto, non del consumatore)

Le 3 clip residue richiedono uno stadio di ricampionamento audio verso il
contratto (48 kHz). Due strade, entrambe fuori dal perimetro di uno sweep di
benchmark perché cambiano il comportamento di produzione di **tutti** i render:

1. **Chronon** — aggiungere `libswresample` al target native-ffmpeg e
   ricampionare in `MuxSession` quando `sample_rate` richiesto ≠ sorgente
   (richiede rebuild del daemon Vulkan). È la correzione "giusta", onora il
   plan sigillato.
2. **Confine del chiamante** — normalizzare l'audio della sorgente prima di
   sigillare il piano (es. via il percorso Rust/FFmpeg già presente in
   `internal/platform/media/rustexec`), lasciando il mux in copia.

Fino a una di queste due, la risposta corretta del sistema per una sorgente a
44.1 kHz resta il fallimento tipizzato (mai un artefatto silenziosamente
sbagliato).
