# TICKET-FINAL-JOB-BITSTREAM-REUSE — il final job non deve ri-encodare l'overlay che ha già prodotto

**Stato:** P0, P2 e P3 chiusi e misurati (P2 dal lato consumatore: la copia di
una finestra è attiva di default e certificata da censimento derivato). P1 resta
aperto perché appartiene al produttore dell'overlay. Il run verde più veloce è
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
- [x] **P2 — Il riuso deve valere per una finestra, non solo per la sorgente
  intera.** CHIUSO (01-11): `finalJobWindowOnGOPGrid()` decide se la finestra
  cade su un confine GOP, e la copia si fa con `-ss -c copy -frames:v` quando il
  **censimento della sorgente** la certifica — non quando la sorgente lo
  dichiara. Il censimento è derivato dalle stesse `ffprobe` del P3: frame count
  dai pacchetti, cadenza dei keyframe dai `packet=flags`, contratto di stream
  dall'header; una sorgente che dichiara `copy_eligible` ma ha B-frame **non** è
  copiabile, una che non dichiara nulla ma è conforme **lo è**. Il censimento
  costa un probe per hash di contenuto, memoizzato per run: 85 finestre di un
  film pagano la certificazione una volta, non 85. Copia attiva di default,
  `VELOX_FINAL_JOB_COPY_CERTIFIED_WINDOWS=0` come leva d'A/B, fallback
  all'encode su ogni rifiuto. Chiave di cache v7: il lavoro è l'identità, non il
  percorso che l'ha prodotto. Test:
  `TestFinalJobCopyWindowPreservesFramesAndCertificate` (frame decodificati
  uguali, certificato del frammento, fallback non allineato, leva off),
  `TestFinalJobSourceCensusIsDerivedNotDeclared`, `TestFinalJobCopyWindowLever`.
  Misura locale del costo che rimuove (720 frame 1080p sorgente, finestra 72
  frame, 3 run): encode **1,30 / 1,39 / 1,34 s**, copia **0,31 / 0,28 / 0,29 s**,
  probe del frammento **0,35 / 0,44 / 0,30 s**. Per finestra copiata il costo
  scende da encode+verifica (≈1,64 s) a copia+verifica (≈0,59 s): **−1,05 s per
  finestra**, ≈89 s su un handoff da 85 finestre a freddo.
  **Condizione dichiarata:** il guadagno esiste solo se la sorgente è conforme.
  L'artefatto overlay del batch (`bf=2`, un solo keyframe, `1/12288`,
  `frame_count=-1`) **non** lo è, quindi la prep del final job continua a
  ricadere sull'encode finché P1 non è chiuso dal produttore. Il riuso intero
  (`finalJobSourceIsExactPreparedFragment`) resta fidato sulla dichiarazione:
  quella sorgente non si scarica, e derivarne il certificato costerebbe il
  download dell'intero film che quel percorso esiste per evitare.
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

Run completo Garlasco, stesso payload (misure lette da `job-full-r*.json`):

| | r49 | r53 | r57 | **r64** | **r65** | obiettivo |
|---|---:|---:|---:|---:|---:|---:|
| `handoff_ms` | 353,9 s | 43,4 s | 25,8 s | **13,2 s** | **13,7 s** | **<20 s** ✅ |
| ├ `handoff_resolve_ms` | — | 30,3 s | 11,2 s | **10,3 s** | **10,6 s** | <12 s ✅ |
| prep frammenti | 264,0 s | ~13 s | ~0 s (67/85 cache) | **~0 s (87/89 cache)** | ~0 s | **<20 s** ✅ |
| run totale | 495,6 s | 184,0 s | 151,1 s | **73,4 s** | **69,7 s** | **<260 s** ✅ |
| mediana delle run VERDI sul codice finale (n=2) | — | — | — | — | **71,6 s** | <260 s, **n≥3** ⚠️ |
| output | — | master `fb275c38…` | `fb3d5e08…` | master certificato | master certificato | MP4 **byte-identico** o parità frame certificata |

### Cosa NON è ancora chiuso (agg. 2026-11-01, dopo r64/r65)

1. **70–74 s è la condizione in cui tutto è già caldo**, non solo i frammenti:
   voiceover (`cache HIT`), mappe (`Chronon dynamic map cache_hit=true`),
   **artefatto overlay riusato** (stesso SHA `b1486eecefc5` in r64 e r65, 165 KB,
   `render_loop_wall_ms` 6,4 s contro 74,3 s di r49) e frammenti 87/89. A freddo
   lo stesso payload resta ~259 s (r50). Il numero bandiera va citato con questa
   premessa o non è un numero.
2. **La mediana è n=2, non n=3** (correzione del 01-11). Le run verdi sul
   codice finale sono **r64 (73,4 s)** e **r65 (69,7 s)**; la terza esecuzione
   citata come verde (`job_1793533772591188059_4b0f553b`, 11:49) è in realtà
   **FAILED in 68,7 s** — è la quarta esecuzione, non una run di riferimento.
   Mediana reale: **71,6 s su n=2**. Il gate `<260 s` resta aperto finché non
   esistono tre run verdi dello stesso payload.
3. **Il difetto della run fallita è attribuito e corretto.** Il gate
   `final_job overlay batch "c2496a4d…" has inconsistent certification`
   richiedeva `frame_count > 0` e `duration_us > 0` sull'artefatto overlay. Ma
   il batch è un **composito packed** la cui certificazione riporta
   `frame_count: -1` (sconosciuto: i frame si indirizzano solo dalle finestre
   del manifest) — è esattamente la forma con cui r64/r65 sono passate
   (`frame_count=-1`, `duration_us=66.958.333`, `size_bytes=165.288`). Il gate
   rifiutava quindi un artefatto che l'assembler accetta, e lo faceva **dopo**
   averlo pagato. Correzione: una sola autorità di certificazione
   (`certifyFinalJobOverlayBatch`), usata sia dall'handoff sia dal confine di
   render; `frame_count=-1` è ammesso, un frame count diverso da -1 e negativo
   no, e quando l'artefatto dichiara una durata ogni piazzamento deve caderci
   dentro. Test: `TestCertifyFinalJobOverlayBatchAcceptsPackedSceneComposite`,
   `…FailsClosed`, `TestFinalJobOverlayHandoffCertifiesSplitSetAndIsDeterministic`.
   In più, il confine di render **non inoltra** più un set di batch che
   l'handoff dovrà rifiutare: ricade sulla submission single-timeline (un solo
   render extra, nessun loop). Test:
   `TestRenderOverlayPlanAtBoundaryFallsBackWhenSplitCannotBeCertified`.
   Il `plan_items=24` della diagnosi precedente **non era un planner
   non deterministico**: 24 = 22 overlay Chronon del batch + **2 mappe**
   (`static__pavia__map__01`, `static__garlasco__map__01`) che viaggiano sulle
   righe dedicate. Lo stesso conteggio è presente in r64, run verde. Il piano
   è stabile; l'identità content-addressed del manifest non dipende
   dall'ordine delle entry (`OverlayManifest.Digest()`), ed è ora asserito da
   test.
4. **Il master audio si ripagava a ogni run.** Il checkpoint audio è chiavato
   su `exec.JobID`, quindi può saltare lavoro solo dentro lo stesso job: una
   revisione, un retry dopo un fallimento non correlato o una seconda run dello
   stesso payload ri-mixavano e ri-encodavano 272,8 s di master. Misura in r64:
   `mix_ms` 2.270 + `aac_encode_ms` 9.476 + `probe_ms` 260 + `hash_ms` 44 =
   **12,1 s** per byte già prodotti (r57 e r64 condividono
   `audio_plan_sha256 6a1eb1d8…` e `final_audio_sha256 c2718881…`). Nuova cache
   `final_audio_cache.go`: chiave = hash del **piano compilato**, sidecar con
   SHA/size/durata/profilo, ri-hash dei byte a ogni hit, e la reference riusata
   passa dallo **stesso** `ValidateFinalAudioReference` del render (un entry
   stantio costa un render, mai un run fallito). Kill switch:
   `VELOX_FINAL_AUDIO_CACHE_DIR=off`. Test: `TestFinalAudioCache*`.
   Limite dichiarato: r65 ha `audio_plan_sha256 5086854446…` e audio
   `435b6fd0…`, cioè **audio diverso**, quindi lì il ricalcolo era dovuto. La
   cache riusa lavoro identico, non nasconde lavoro cambiato.
5. I ticket del motore (`FRAME-PROOF-CHEAP`, `TIMELINE-FLATBUFFER`,
   `GLOW-HALF-RES`) restano **non implementati**: valgono solo sulla run a
   freddo (overlay render ~80 s) e il loro gate è MP4 byte-identico.

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
