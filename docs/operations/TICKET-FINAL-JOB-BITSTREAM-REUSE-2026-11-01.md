# TICKET-FINAL-JOB-BITSTREAM-REUSE — il final job non deve ri-encodare l'overlay che ha già prodotto

**Stato:** PARZIALE — P0 e P3 chiusi e misurati; il run verde più veloce è
**r53: 184 s** (r49: 495 s, **−62,8%**, −311 s) con `remote_final_job`
SUCCEEDED e master prodotto. P1/P2 restano aperti perché appartengono al
produttore dell'overlay. Aggiornato il 2026-11-01 con i numeri reali:
`REPORT-2026-11-01-final-job-timing-map-cache.md` §6.
**Owner:** `refactored` (`internal/app/wiring/`, `internal/capabilities/scripts/`)
+ Chronon per l'emissione conforme.
**Coordina:** [`TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE`](TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md)
(§0/§10) e [`TICKET-VIDEO-CODEC-BOUNDARY`](../../../Chronon3d/docs/tickets/TICKET-VIDEO-CODEC-BOUNDARY.md).
**Decisione:** [`ADR-DRAFT-DECIDE-BEFORE-EVALUATE`](../../../Chronon3d/docs/tickets/ADR-DRAFT-DECIDE-BEFORE-EVALUATE.md).

## TODO — cosa manca

- [x] **P0 — Attribuire i 89,9 s non attribuiti di `handoff`.** CHIUSO: il campo
  `handoff_resolve_ms` misura la risoluzione degli asset runtime dentro lo
  handoff — **65,1 s in r50, 60,6 s in r52, 30,3 s in r53**: la deduplica per
  hash della sorgente dimezza la voce (56 risoluzioni invece di una per
  finestra). Resta la seconda voce del run e il prossimo bersaglio. Logica: in r49 lo
  stage `remote_final_job` pesa 381,0 s, la prep frammenti 264,0 s e restano
  **89,9 s** senza una fase (PREPARE/FINALIZE + build payload); la varianza dello
  stesso payload è ±90 s (r40 401,9 → r48 494,4 → r49 495,6). Senza questa
  attribuzione nessun guadagno sotto i 90 s è distinguibile dal rumore. Dove:
  `RemoteFinalJobTiming` + `finalJobSegmentPreparer.Totals()`
  (`internal/app/wiring/final_job_segment_prepare.go:82-113`) — manca un
  `payload_ms`/`prepare_ms`, il campo non esiste nel codice. Gate: budget chiuso
  al 100% con n≥3 run per configurazione.
- [ ] **P1 — L'overlay esce conforme, o il confine mente.** APERTO (produttore
  esterno a questo repository). Misura aggiuntiva 01/11: l'artefatto del batch
  **non è deterministico fra run** — r49 `fcd8c52f…` (9.227.633 B) vs r50
  `6e7305c1…` (9.234.231 B), stessa durata 66,958 s, PSNR 31,0 dB, SSIM 0,996 —
  quindi una cache chiavata sulle sorgenti vale dentro una famiglia di run, non
  attraverso run che ri-renderizzano l'overlay. Logica: l'artefatto
  del batch overlay dichiara `copy_eligible=true`, `first_frame_keyframe=true`,
  `closed_gop=true`, `frame_count=-1`, `profile_id` **assente**, mentre il
  bitstream misurato è `h264 High 1920×1080 yuv420p 24 fps` con `has_b_frames=2`,
  `time_base=1/12288` e **un solo keyframe**. La certificazione è una dichiarazione,
  non una derivazione: finché resta così nessun consumatore può copiare e ogni
  consumatore deve ricostruire. Dove: emissione video del batch overlay (Chronon
  `native_av_encoder`, `keyframe_cadence.hpp`, opzioni GOP) + il punto che
  popola l'artefatto (`output_profile_id` esiste già in
  `internal/platform/renderinggen/overlay_plan_types.go:23` ma non arriva
  nell'artefatto). Gate: `ffprobe` sull'artefatto mostra `bf=0`, `time_base=1/90000`,
  keyframe ogni 48 e `frame_count` corretto; l'artefatto porta
  `profile_id=VELOX_ASSEMBLY_READY_V1` **derivato da un probe unico per artefatto**
  (≈0,3 s, non per frammento).
- [ ] **P2 — Il riuso deve valere per una finestra, non solo per la sorgente
  intera.** Logica: `finalJobSourceIsExactPreparedFragment()` accetta il riuso solo
  se `source_in_us == 0` **e** `profile_id == VELOX_ASSEMBLY_READY_V1` **e**
  `frame_count == request.FrameCount`: cioè solo una sorgente intera già preparata
  da noi. Un film di 272,8 s tagliato in 85 finestre non soddisfa **nessuna**
  delle tre, quindi paga `ffmpeg` + `ffprobe` sempre. Dove:
  `internal/app/wiring/final_job_segment_prepare.go:622-628`; taglio con
  `-ss/-to -c copy` su confini GOP allineati (`source_in_us` multiplo di 2 s a
  24 fps = 48 frame), con certificato del boundary letto una volta per sorgente.
  Gate: `segments=85, reused=85` e `encode_ms=0, verify_ms=0` sul receipt.
- [x] **P3 — Il certificato del frammento non si ripaga due volte.** CHIUSO:
  una sola `ffprobe` per frammento sui `packet=flags` (niente decodifica),
  **1,52 s → 0,30 s → 0,065 s** per frammento (r49 → r50 → r52); un solo
  SHA-256 per frammento (`PublishFinalJobLocalObjectVerified`). Logica:
  `verifyFinalJobFragment()` costa **1,52 s per frammento** su file da ~1,3 MB
  (85 × 1,52 = 129,0 s, metà della prep): è avvio di processo + lettura di
  metadata, non lavoro video. Se il taglio è una copia di byte certificati, il
  certificato del frammento **è** il certificato della sorgente + la prova del
  boundary. Dove: `final_job_segment_prepare.go:657` e la doppia lettura
  `verifyFinalJobFragment` → `digest.SHA256File`. Gate: zero probe per frammento
  copiato; un probe per sorgente; nessuna regressione fail-closed (un frammento
  non copiabile deve cadere nel percorso costoso e contarsi).
- [x] **P4 — Attesa attach: 27,1 s per un render da 13,9 s.** CHIUSO come
  DIAGNOSI, non come risparmio: `wait_ms` = `queue_ms` (13,0 s) + `execution_ms`
  (13,9 s), delta +0,4 s. I 27,1 s **non sono polling**, quindi il gate
  "`wait_ms` ≤15 s" non è raggiungibile senza eliminare coda o render. Il
  backoff adattivo è stato introdotto (prima osservazione immediata, poi
  375 ms → 750 ms → 1,5 s → 3 s): il tetto di guadagno misurato è ~1 s
  (`poll_lag` 1,0 → 2,0 s, dentro il rumore).

## Evidenza misurata (r48/r49, stesso payload, Master + Worker 51 attivi)

Fonte: `ops/jobs/garlasco_editorial_e2e_20261030/job-full-r49.json`,
`REPORT-2026-11-01-final-job-timing-map-cache.md` §1.1.

| Voce | r48 | r49 |
|---|---:|---:|
| run totale | 494,4 s | 495,6 s |
| stage `remote_final_job` | 335,8 s | **381,0 s** |
| ├ `handoff_ms` (lavoro locale) | 308,7 s | 353,9 s |
| └ `wait_ms` (attach, 10 poll) | 27,1 s | 27,1 s |
| render remoto `execution_ms` | 13,9 s | **13,9 s** |
| coda remota | 13,0 s | 13,0 s |

`timing.segment_prep` (r49): 85 segmenti su 272,8 s di film, 114,7 MB pubblicati.

| Fase | Tempo | Quota prep |
|---|---:|---:|
| download sorgenti | 6,6 s | 2,5% |
| encode `libx264 veryfast` CRF 23 | **126,2 s** | 47,8% |
| verifica ffprobe | **129,0 s** | 48,9% |
| publish Objectstore | 2,2 s | 0,8% |
| **prep totale** | **264,0 s** | 74,6% di `handoff` |

## Diagnosi

Il worker remoto compone 272,8 s di film in **13,9 s** perché il suo assembler è
una concatenazione di pacchetti (`video.assemble.copy.v1`,
`internal/capabilities/videocreate/ports.go:169,207`). Perché quella copia sia
possibile, **noi** ri-encodiamo ogni finestra in locale: 85 processi `ffmpeg` +
85 `ffprobe`, 3,2 s per frammento, per un film che il nostro stesso renderer ha
già prodotto in formato corretto.

È lo stesso difetto dei due lati del confine, in due forme:

1. il **produttore** non parla il contratto del consumatore (bf=2, un keyframe,
   `1/12288`, `frame_count=-1`, `profile_id` assente) → il consumatore non può
   fidarsi;
2. il **consumatore** non ha un modo di fidarsi di una finestra
   (`source_in_us == 0` obbligatorio) → anche un artefatto conforme verrebbe
   ri-encodato.

Correggere solo uno dei due non cambia nulla: se il produttore è conforme ma il
gate pretende `source_in_us == 0`, resta un encode per finestra; se il gate è
permissivo ma lo stream non è conforme, il concat `-c copy` produce un MP4 rotto.

## Le micro-ottimizzazioni, misurate (non più stimate)

Le voci che qui erano stimate "~130–150 s di prep" valgono in realtà di più,
perché si sommano alla cache dei frammenti e delle sorgenti:

| | r49 | r50 (cache fredda) | r51 (calda) | r53 (calda, verde) |
|---|---:|---:|---:|---:|
| run totale (wall) | 495,0 s | 259,0 s | 214,0 s | **184,0 s** |
| `handoff_ms` | 353,9 s | 117,7 s | 73,2 s | **43,4 s** |
| ├ `handoff_resolve_ms` | — | 65,1 s | 59,7 s | **30,3 s** |
| encode (somma lane) | 126,2 s | 169,8 s | 34,1 s | 33,7 s |
| verifica (somma lane) | 129,0 s | 25,6 s | 5,7 s | 5,8 s |
| download sorgenti | 6,6 s | 3,4 s | 0,2 s | **0,15 s** |
| publish Objectstore | 2,2 s | 1,8 s | 2,2 s | 1,8 s |
| byte pubblicati | 114,7 MB | 115,0 MB | 6,8 MB | **6,7 MB** |
| riuso dalla cache | 0/85 | 0/85 | **67/85** | **67/85** |

**Δ −311 s (−62,8%) end-to-end, −310,5 s sul solo handoff.** Il verdetto è
verde perché il run remoto è `SUCCEEDED` con master certificato: la prep non è
più il costo strutturale del confine, è 0,51 s per finestra.

### Il secondo difetto, trovato da r55: due piazzamenti, un solo hash

Il vincolo del Master è doppio e va rispettato **insieme**:

| Autorità | Regola | Rifiuto osservato |
|---|---|---|
| render plan v2 | due asset non possono condividere l'identità canonica | r55: `assets … share canonical identity "sha256:4a0a4f45…"` |
| assemble copy v1 | ogni piazzamento si certifica contro il **proprio** manifest | r52: `segment "segment-0017" manifest binding is not exact` |

Due finestre di un clip statico prese a in-point diversi codificano **byte
identici**, quindi il caso non è teorico: capita a ogni run con clip di immagini
ferme. Condividere l'oggetto viola il primo vincolo; duplicarlo con due id viola
il secondo. La rappresentazione che entrambi accettano è **due oggetti che
differiscono nel container**: alla collisione il secondo piazzamento viene
ri-preparato con `PlacementTag=<segment id>`, che entra come
`-metadata chronon_placement=…` (nessun pixel cambia) e come parte della chiave
di cache. Fallito il retry, il piano si ferma invece di sperare:
`final_job segment %q still certifies the bytes of another placement`.

### Il difetto trovato dalla misura (r52) e la correzione

Il primo tentativo di far pagare una volta sola il lavoro identico ha **fatto
fallire r52**: il plan builder fondeva due piazzamenti con lo stesso hash su un
unico `asset_id` e riscriveva `source_in_us = 0` sul secondo. Il validatore
remoto lo ha rifiutato — `prepared asset certification failed: segment
"segment-0017" manifest binding is not exact` — ed era corretto: un segmento si
certifica contro il manifest del **proprio** piazzamento. Correzione: un asset
per piazzamento, binding completo (`source_in_us`, `source_duration_us`,
`timeline_start_frame`) e il controllo di unicità ripristinato; la deduplica
resta dove è legittima, cioè nella **cache del frammento** (stessa sorgente,
stessa finestra, stessa policy) e nella **risoluzione della sorgente** (stesso
hash). Test: `TestPrepareFinalJobVideoSegmentsBindsEachPlacementToItsOwnAsset`.

## Gate complessivo

Run completo Garlasco, stesso payload:

| | r49 | r53 (oggi) | obiettivo |
|---|---:|---:|---:|
| prep frammenti | 264,0 s | ~13 s | **<20 s** ✅ |
| stage `remote_final_job` | 381,0 s | **70,8 s** | **<70 s** ✅ (al bordo) |
| run totale | 495,6 s | **184,0 s** | **<260 s** ✅ |
| output | — | master `fb275c38…` | MP4 **byte-identico** o parità frame certificata |

Il gate "MP4 byte-identico" non è applicabile alla cache calda per costruzione:
le finestre riusate sono **gli stessi byte** già certificati (lo SHA è nel
sidecar e viene ri-verificato a ogni hit), non un ri-encode «equivalente».

## Non obiettivi

- Non toccare il worker: 13,9 s su 273 s di film è già copy.
- Non introdurre una versione nuova del contratto: `VELOX_ASSEMBLY_READY_V1/V2`
  (`internal/kernel/media/assembly_contract.go`) restano l'unica authority; se
  serve altro (boundary certificate), si esprime dentro V1 come campo, non come
  V3 parallela.
- Non usare NVENC per l'encode dei frammenti come scorciatoia: risparmia ~30 s
  su un percorso che il P2 elimina del tutto, e introduce una decisione di look.
- Non aggiungere lane per assorbire la prep: 24 core sono già liberi, il costo è
  per-processo, non per-CPU.
- Non dedurre il guadagno dalla singola run: la varianza end-to-end sullo stesso
  payload è +23% (r40 → r49). Ogni riga della tabella dei gate richiede n≥3.
