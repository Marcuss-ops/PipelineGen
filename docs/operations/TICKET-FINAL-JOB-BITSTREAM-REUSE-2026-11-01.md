# TICKET-FINAL-JOB-BITSTREAM-REUSE — il final job non deve ri-encodare l'overlay che ha già prodotto

**Stato (aggiornato 2026-11-03):** P0, P2 e P3 chiusi e misurati; P1 resta
chiuso per il certificato di singolo artefatto ma **riaperto per la compatibilità
bitstream tra segmenti** (nuovo blocker P5, evidenza P4 sotto); il solo
`profile_id` non autorizza più il packet-copy tra sorgenti diverse. P1 chiuso sul consumatore, sul
`frame_count` derivato e sul `profile_id`: i batch manifest dichiarano
`VELOX_OVERLAY_VIDEO_ONLY_V1`, il profilo dedicato che descrive un artefatto
video-only (V1 richiede un audio stream e non era dichiarabile). La verifica
**sul campo** è chiusa: r80–r83 (9/9 batch) e r92–r94 pubblicano il campo con
`native_output_profiles=true`; non resta una decisione di contratto aperta. Il run verde più veloce è
**r78: 49,8 s** (r49: 495,6 s, **−90%**) con `handoff` 1,6 s e `resolve`
0,427 s. Aggiornato il 2026-11-02 con la misura sull'artefatto reale e la
correzione del probe. Fonte tempi:
`REPORT-2026-11-01-final-job-timing-map-cache.md` §6. **Serata 02-11:** r96 a
cache fredda chiude in **140,9 s** con `payload_ms` **70,3 s** (−19,5 s da r95) e
il certificato del frammento riportato a `VELOX_ASSEMBLY_READY_V1` dopo il
fallimento di r90 — evidenza in `REPORT-2026-11-02-r96-fragment-layer.md`.
**Owner:** `refactored` (`internal/app/wiring/`, `internal/capabilities/scripts/`)
+ RenderingGen (`internal/processor/`, certificazione dell'artefatto) + Chronon
per l'emissione conforme.
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
  (  `internal/app/wiring/final_job_segment_prepare.go:82-113`). **Aggiornamento
  02-11:** i campi `payload_ms` e `prepare_ms` ora esistono in
  `RemoteFinalJobTiming` e sono misurati nello handoff: `payload_ms` è la
  costruzione locale del payload (contiene `segment_prep` e
  `handoff_resolve_ms`), `prepare_ms` è il round-trip PREPARE, quindi FINALIZE è
  il resto (`handoff − payload − prepare`) e il budget chiude per costruzione.
  Test: `TestRemoteFinalJobTimingAttributesPayloadAndPrepare`. Gate residuo:
  budget verificato con n≥3 run per configurazione (misura live).
- [x] **P1 — L'overlay esce conforme, o il confine mente.** CHIUSO NEL CODICE
  (02-11): `frame_count` derivato e `profile_id=VELOX_OVERLAY_VIDEO_ONLY_V1`
  dichiarato dai batch manifest. Verifica live chiusa (r80–r83, r92–r94). Misura
  aggiuntiva 01/11: l'artefatto del batch
  **non è deterministico fra run** — r49 `fcd8c52f…` (9.227.633 B) vs r50
  `6e7305c1…` (9.234.231 B), stessa durata 66,958 s, PSNR 31,0 dB, SSIM 0,996 —
  quindi una cache chiavata sulle sorgenti vale dentro una famiglia di run, non
  attraverso run che ri-renderizzano l'overlay. Logica: l'artefatto
  del batch overlay dichiara `copy_eligible=true`, `first_frame_keyframe=true`,
  `closed_gop=true`, `frame_count=-1`, `profile_id` **assente**. **Misura
  2026-11-02 sull'artefatto di r64/r65** (`b1486eecefc5…`):
  `h264 High 4.0 1920×1080 yuv420p 24 fps`, `has_b_frames=0`, **34 keyframe con
  GOP esattamente 48** su 1607 frame, primo frame IDR, `time_base=1/12288`,
  `nb_frames` assente. La descrizione precedente (`bf=2`, un solo keyframe) vale
  per l'artefatto di r49 (`fcd8c52f…`), non per quello corrente: sul bitstream
  quel singolo bitstream è conforme. Il receipt r78 contiene invece nove batch
  distinti, con primo SHA `227da1b914f2…`; le misure di `b1486eecefc5…` non ne
  certificano i bitstream. Il consumatore non si fida più della dichiarazione:
  deriva il frame count dai pacchetti, e il produttore ora lo deriva con un
  probe quando il receipt non lo sa. Il `profile_id` non è più dichiarativo:
  `capoverlay.OverlayPlan.OutputProfileID` esiste, il batch manifest fused lo
  imposta a `kernelmedia.OverlayVideoOnlyProfileID` e il campo attraversa
  `marshalRenderingGenOverlayPlan` fino a `semanticPlan.OutputProfileID`.
  RenderingGen registra il profilo (`ProfileVeloxOverlayVideoOnlyV1`) con i
  fatti misurati: 1920×1080@24, H.264 High 4.0, yuv420p, SAR 1:1, tv/bt709,
  GOP chiuso 48, primo IDR, start PTS 0, **zero stream audio**. Gate: `ffprobe`
  sull'artefatto mostra `bf=0`, keyframe ogni 48 e `frame_count` corretto;
  `time_base=1/90000` è opzionale perché il consumatore accetta anche `1/12288`
  e rimuxa. L'artefatto porta `profile_id=VELOX_OVERLAY_VIDEO_ONLY_V1`; la
  certificazione usa il probe già fatto per il frame count, non un probe
  aggiuntivo.

  **Il blocco era nel consumatore (corretto 2026-11-02).** Il censimento
  pretendeva `time_base=1/90000` e un `nb_frames` dichiarato, quindi rifiutava
  un artefatto con GOP chiuso e zero B-frame e pagava 85 encode.
  `finalJobSourceTimeBaseCertified` accetta ora le due autorità (`1/90000` e il
  timebase x264 `1/(fps·512)` = `1/12288` a 24 fps), il frame count usa i
  pacchetti quando `nb_frames` è assente, e ogni frammento resta rimuxato a
  `1/90000` prima della pubblicazione; GOP, B-frame, start a zero e durata
  restano fail-closed. Test: `TestFinalJobSourceTimeBaseCertified`,
  `TestFinalJobCertificateAcceptsUnknownDeclaredFrameCount`,
  `TestFinalJobCensusAcceptsX264TimeBase`,
  `TestFinalJobRealOverlayArtifactIsCopyable`. Resta al produttore solo il
  `profile_id` (vedi blocker) e, opzionalmente, `-video_track_timescale 90000`;
  il `frame_count` è ora derivato dal probe.

  **Il `frame_count` ora è derivato, non fidato (02-11).** RenderingGen
  preferisce il receipt canonico, ma un composito packed riporta
  `frame_count=-1`: quel valore non è un conteggio e lasciava l'artefatto senza
  la sola cifra che il certificato di finestra usa. `probeFactsForFinalize`
  tratta ora un receipt senza frame count positivo come non descrittivo e fa
  **una sola ffprobe** sull'artefatto (≈0,3 s), pubblicando il conteggio reale:
  sull'artefatto `b1486eecefc5…` sono **1607** pacchetti, non `-1`. I render
  normali restano sul receipt, senza probe. Test:
  `TestProbeFactsProbeLocallyWhenReceiptOmitsFrameCount` (+ i due test che
  pinnano receipt e fallback).

  **La decisione di contratto è presa: profilo dedicato (02-11).** `ffprobe`
  mostra **un solo stream video, zero audio**, mentre `assemblyReadyProfile()`
  pinna `AudioStreams: 1`: assegnare V1 a un artefatto video-only sarebbe un
  certificato falso. Invece di indebolire V1, `VELOX_OVERLAY_VIDEO_ONLY_V1`
  descrive ciò che l'artefatto è. Il profilo **non** pinna il colour transfer
  (l'artefatto riporta `iec61966-2-1`, V1 dichiara bt709: pinnare uno dei due
  sarebbe una decisione che nessuno ha preso), il video timebase (stessa
  regola di V1: il consumatore accetta `1/12288` e rimuxa) né il **closed GOP**
  (vedi la verifica live del 02-11: una finestra batch da 0,54 s non può
  dimostrare la cadenza, e il gate di copia resta il censimento del
  consumatore).
  `TestOverlayVideoOnlyProfileCertifiesTheMeasuredArtifact` pinna entrambi i
  lati: il profilo nuovo accetta i fatti reali, V1 continua a rifiutarli.
  `TestOverlayVideoOnlyProfileFailsClosedOnAudioAndCopyDrift` pinna che audio,
  drift del GOP, start PTS e livello siano fail-closed;
  `TestOverlayVideoOnlyProfileCertifiesShortBatchWindows` pinna che una
  finestra corta (cadenza non osservabile) sia certificata mentre un primo
  frame non IDR o un drift di cadenza restano rifiutati.
  `TestFusedOverlayBatchPlanDeclaresTheVideoOnlyProfile` pinna il campo sul
  piano e sul wire.  `output_profile_id` entra anche nella chiave della cache di
  render, così un artefatto certificato sotto un profilo non soddisfa un piano
  che ne chiede un altro. Il criterio live è **soddisfatto** (02-11, r80–r83).
  Nota operativa: il worker deve girare con `native_output_profiles=true`,
  altrimenti `pipeline.go:354` strippa il campo (con metrica e warning).

  **Il frammento resta V1 (corretto dopo r90).** Il tentativo di dichiarare il
  frammento `VELOX_OVERLAY_VIDEO_ONLY_V1` è stato ritirato: il gate remoto lega
  il frammento al profilo che esso certifica (V1), e r90 è fallita con
  `segment "segment-0000" manifest binding is not exact`. `finalJobSegmentProfile`
  è tornato `VELOX_ASSEMBLY_READY_V1`; il profilo video-only resta su
  `plan.Output` e sui batch manifest. r91 (FAILED, 98,9 s) ha riprodotto lo
  stesso errore prima del ripristino; r92–r94 (70,9 / 91,5 / 100,2 s) sono le
  verdi a cache calda che lo confermano.
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
  **Misura 2026-11-02 sull'artefatto reale** (`b1486eecefc5…`, finestra 72
  frame, 3 run): copia **0,31 / 0,34 / 0,31 s**, encode **1,21 / 1,18 / 1,25 s**
  → **−0,89 s per finestra** (≈−76 s di somma su 85 finestre; ~19 s di muro
  sulle 4 lane, proiezione). L'artefatto overlay corrente **è** conforme ai fatti
  che il copiatore usa (bf=0, GOP 48, primo IDR): il censimento lo certifica,
  quindi il guadagno non è più bloccato da P1. Resta fuori solo il riuso della
  sorgente intera, che si fida della dichiarazione. Il riuso intero
  (`finalJobSourceIsExactPreparedFragment`) resta fidato sulla dichiarazione:
  quella sorgente non si scarica, e derivarne il certificato costerebbe il
  download dell'intero film che quel percorso esiste per evitare.
- [x] **P3 — Il certificato del frammento non si ripaga due volte.** CHIUSO
  (02-11, seconda correzione): **zero probe per frammento**. La verifica runtime
  per frammento è stata eliminata da tutti e tre i percorsi — copia, encode e
  remux di un piazzamento duplicato. Il certificato è **derivato**: per la copia
  è il census della sorgente (GOP chiuso e fisso, primo IDR, frame count dai
  pacchetti) più la prova del boundary (inizio su multiplo di 48 frame); per
  l'encode è il comando stesso (High 4.0, 1920×1080, yuv420p, 24/1, bf=0,
  GOP 48, PTS 0), pinnato dai test su un frammento reale; per il remux sono i
  byte certificati del piazzamento originale più il tag di container. Resta
  **un probe per sorgente**, memoizzato per SHA: è l'unica derivazione che non
  si può fare a priori. Storia: 1,52 s/frammento con la decodifica dei frame
  (129,0 s su 85), 0,30 s con i `packet=flags`, 0,065 s in r52; ora 0 sul
  percorso di produzione. Gate: zero probe per frammento; un probe per
  sorgente; i test che decodificano il frammento prodotto
  (`TestFinalJobCopyWindowPreservesFramesAndCertificate`,
  `TestFinalJobSegmentPreparerEncodesOneAssemblyReadyFragment`,
  `TestFinalJobSegmentPreparerPadsShortBoundaryToExactFrameCount`) restano la
  prova del contratto; un frammento non copiabile cade ancora nel percorso
  encode e si conta. La geometria (width/height/fps/pix_fmt) viaggia nello
  stesso census: il fallback encode non lancia una seconda `ffprobe` per gli
  stessi fatti. Dal 02-11 la chiave è **v9** e distingue il percorso
  `copy-enabled` da quello `encode-only`: il bump invalida per costruzione il
  corpus precedente, quindi r95/r96 partono a cache fredda (`cached=0`). La
  cache v8 invalida i frammenti scritti prima di
  `-avoid_negative_ts make_zero`, perché la copia può produrre byte diversi a
  parità di lavoro.
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
una concatenazione di pacchetti (`video.assemble.copy.v1`). Perché quella copia sia
possibile, **noi** ri-encodiamo ogni finestra in locale: 85 processi `ffmpeg` +
85 `ffprobe`, 3,2 s per frammento, per un film che il nostro stesso renderer ha
già prodotto in formato corretto.

È lo stesso difetto dei due lati del confine, in due forme:

1. il **produttore** non parlava il contratto del consumatore sull'artefatto di
   r49 (bf=2, un keyframe, `1/12288`, `frame_count=-1`, `profile_id` assente) →
   il consumatore non poteva fidarsi. Sull'artefatto corrente i fatti di copia
   sono conformi, `frame_count` è derivato e `profile_id` è dichiarato;
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

| | r49 | r53 | r57 | **r64** | **r65** | **r78** | obiettivo |
|---|---:|---:|---:|---:|---:|---:|---:|
| `handoff_ms` | 353,9 s | 43,4 s | 25,8 s | **13,2 s** | **13,7 s** | **1,6 s** | **<20 s** ✅ |
| ├ `handoff_resolve_ms` | — | 30,3 s | 11,2 s | **10,3 s** | **10,6 s** | **0,427 s** | <12 s ✅ |
| prep frammenti | 264,0 s | ~13 s | ~0 s (67/85 cache) | **~0 s (87/89 cache)** | ~0 s | ≈0 (85/89) | **<20 s** ✅ |
| run totale | 495,6 s | 184,0 s | 151,1 s | **73,4 s** | **69,7 s** | **49,8 s** | **<260 s** ✅ |
| mediana delle run VERDI sul codice finale (n=3, 02-11) | — | — | — | — | — | — | **63,2 s**, <260 s ✅ |
| output | — | master `fb275c38…` | `fb3d5e08…` | master certificato | master certificato | master certificato | MP4 **byte-identico** o parità frame certificata |

### Verifica live 2026-11-02 — gate chiuso (r79–r83)

Stesso payload (`garlasco-editorial-r78-parallel-fragments.generate.json`),
worker e Master ricostruiti dai sorgenti di questo commit. I quattro criteri
che erano aperti:

| criterio | esito misurato |
|---|---|
| run totale < 260 s, **n≥3** | r80 **67,2 s**, r81 **60,6 s**, r82 **63,2 s** → mediana **63,2 s** (varianza ±3,3 s, contro i ±90 s storici) |
| `handoff_resolve_ms` assorbito e non aggiunto (freddo) | r83 a cache fredda: handoff **88,1 s**, `resolve` **0,645 s** = 0,7% del payload; nessun tempo aggiunto |
| copy-window a freddo | r83: `segment_prep.copied = 11` su 86 segmenti, `derived = 1`, `cached = 0`; le finestre copiate non passano dall'encoder per costruzione (ramo `copied`), ma il receipt aggrega `encode_ms` e **non** lo attribuisce per finestra |
| profilo live | `profile_id=VELOX_OVERLAY_VIDEO_ONLY_V1` su **tutti e 9 i batch** di r80/r81/r82/r83 (64 occorrenze per job); zero `output profile … stripped` nel log del worker |

Artefatto verificato con `ffprobe` sul batch `9c5f0de9…` (3,000 s, 72 frame):
h264 **High 4.0**, 1920×1080, yuv420p, SAR 1:1, tv/bt709/`iec61966-2-1`/bt709,
keyframe a 0 e 2,0 s (GOP 48), **zero stream audio** — esattamente i fatti che il
profilo dichiara.

**Difetto trovato dalla verifica live (e corretto).** Il primo run col profilo
nuovo (r79) ha fallito la certificazione di un batch con `closed GOP not
certified`, facendo cadere il confine di render sul percorso single-timeline
(r79: 113,3 s, `remote_final_job` 38%). La causa non era il bitstream: laregola di cadenza (`closedGOPPositionsCadence`) richiede almeno tre keyframe (due
intervalli GOP completi), mentre i batche fusi di questo payload vanno da **72 a
306 frame** (3,0–12,75 s, misurati sugli artefatti di r80/r82) e le due finestre
più corte (**72 e 84 frame**) contengono un solo intervallo: la cadenza **non è
dimostrabile** lì per costruzione. Pinnare `RequireClosedGOP` rendeva il
certificato insoddisfacibile e peggiorava la latenza. Il profilo ora certifica
solo ciò che ogni finestra può provare — primo frame IDR, start PTS 0, geometria,
codice, zero audio e, **quando la cadenza è osservabile**, GOP 48 — mentre il
closed GOP resta al censimento del consumatore, che lo deriva dalla tabella dei
pacchetti dell'artefatto intero ed è il vero gate della copia. Regressione pinnata
da `TestOverlayVideoOnlyProfileCertifiesShortBatchWindows` (finestra da 19 frame:
certificata; primo frame non IDR o cadenza drift: rifiutati) e dal fatto che
`TestOverlayVideoOnlyProfileCertifiesTheMeasuredArtifact` pretende
`RequireClosedGOP == false`.

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

6. **Il produttore dichiara frame count e profilo.** L'artefatto r64/r65
   `b1486eecefc5…` è conforme sui fatti che il copiatore usa (bf=0, GOP 48,
   1607 frame, primo IDR); il `frame_count` non è più `-1` perché RenderingGen
   lo deriva con un probe unico quando il receipt non lo dichiara, e i batch
   manifest dichiarano `profile_id=VELOX_OVERLAY_VIDEO_ONLY_V1`. Il profilo è
   dedicato, non V1: l'artefatto è video-only e `VELOX_ASSEMBLY_READY_V1`
   richiede 1 stream audio, quindi V1 sarebbe un certificato falso. Il
   container usa ancora `1/12288`; il consumatore lo accetta e rimuxa a
   `1/90000`. Il riuso della sorgente intera resta l'unico percorso che si fida
   ancora della dichiarazione, e la pubblicazione del `profile_id` va verificata
   su una run live con `native_output_profiles=true`.

Il gate "MP4 byte-identico" non è applicabile alla cache calda per costruzione:
le finestre riusate sono **gli stessi byte** già certificati (lo SHA è nel
sidecar e viene ri-verificato a ogni hit), non un ri-encode «equivalente».

## Aggiornamento 2026-11-02 (sera) — layer frammenti misurato su r96

Run `r96` (`job_1793616110611675492_5c784cea`), stesso payload di r95,
binario ricostruito e servizio riavviato, esito **SUCCEEDED** in **140,9 s**.
È una run a cache frammenti **fredda** (`cached=0` dopo il bump a v9), quindi il
confronto omogeneo è con r95 (fredda, binario precedente) e con r83 (freddo,
164,2 s):

| Voce | r95 | **r96** |
|---|---:|---:|
| run totale | 146,1 s | **140,9 s** |
| `handoff_ms` | 90,6 s | **71,2 s** |
| `payload_ms` | 89,8 s | **70,3 s** |
| `encode_ms` accumulato | 301,3 s | **241,4 s** |
| `verify_ms` | 33,5 s | 23,5 s |
| `publish_ms` | 2,68 s | 0,23 s |
| frammenti encode/copy/derived | 74/11/1 | 74/11/1 |

Il guadagno di lavoro (−59,9 s di encode) vale −19,5 s di wall perché la prep
gira su 4 lane già all'**85,8%** di utilizzo (241,4 s / (70,3 s × 4), floor
60,3 s). Il resto del run è fuori dal layer: overlay 28,9 s,
generate 11,2 s, prepare_join 10,9 s, scene_analysis 9,0 s, coda Master 9,0 s,
render remoto 7,7 s.

Correzioni atterrate con la stessa misura:

- le metriche dell'handoff sono ora **per scope** (un accumulatore per handoff,
  `Timing(scope)`); prima gli scalari erano la differenza di due snapshot
  globali e due run concorrenti si contaminavano (le finestre erano già
  filtrate);
- la geometria di delivery è **una sola dichiarazione** derivata dal contratto
  kernel (`finalJobDelivery`, `finalJobDeliveryMatches`,
  `finalJobDeliveryLevelCode`): filtro encoder, argomenti, census, probe e gate
  di riuso non ripetono più 1920×1080/24/yuv420p/GOP 48;
- la chiave del frammento ha una sola composizione (`fragmentWorkKey`), e
  `fragmentPath` ri-hasha i byte invece di fidarsi della memo (size, mtime): una
  riscrittura in place di pari dimensione nello stesso tick non è dimostrabile
  da una stat.

**Profilo del frammento — correzione al confine r90.** Il certificato del
frammento è tornato `VELOX_ASSEMBLY_READY_V1`: r90 (02-11 09:32) è fallita con
`segment "segment-0000" manifest binding is not exact` quando il frammento
portava `VELOX_OVERLAY_VIDEO_ONLY_V1`. Il profilo video-only resta corretto per
`plan.Output` e per i batch manifest (r80–r83); non è accettato dal gate di
copia remoto per il frammento.

**Ottimizzazioni residue — misurate (02-11, terza tornata).** Lane 4→6 (r99, cache fredda): 127,2 s contro 140,9 s
di r96, utilizzo lane 83,8% vs 85,8%, CPU media 84,7% e picco 99%; **misurato,
default 4 invariato** su n=1. Normalizzazione per sorgente: i tre outlier
provengono da tre sorgenti distinte usate da una finestra ciascuna; a freddo
solo `segment-0084` è `normalized=true` (10,9 s), gli altri due costano 4,5 s
senza normalizzazione → **rigettata**. Coda Master 9,0 s resta esterna. La
proiezione `timing` in `/full` è **corretta**: il reader preferisce il run
`script.generate` ai figli `finalize.*`; r97/r98 mostrano `wall_ms`
111.706/61.835 e 16 stage nel `critical_path`. Evidenza:
`ops/jobs/garlasco_editorial_e2e_20261030/REPORT-2026-11-02-r96-fragment-layer.md`
§8–§9.

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

## P5 — Compatibilità SPS/PPS tra segmenti copy-only (RIAPERTO, 2026-11-03)

**Vincolo prodotto:** l'assemblaggio finale resta packet-copy. Il gate di copia
deve confrontare i parametri reali del bitstream di ogni segmento, non fidarsi
di `profile_id`, risoluzione, fps o nome del profilo.

**Evidenza sul job P4 r101-like:** gli 85 frammenti sorgente sono integri
(SHA-256 verificato e decodifica senza errori), ma `ffmpeg/ffprobe` distinguono
tre famiglie H.264. Tutte dichiarano High, level 4.0, 1920×1080, yuv420p e
24 fps; i parametri codificati sono diversi. Dei tre gruppi, **due sono
famiglie overlay** (9 render Chronon nella famiglia B, 2 mappe importate nella
famiglia C); la terza è stock/clip:

| Famiglia | Provenienza | SPS/PPS osservati | Conseguenza |
|---|---|---|---|
| A | stock e clip | `pic_order_cnt_type=2`, `max_num_ref_frames=1`, PPS `weighted_pred=1`, `weighted_bipred_idc=0`, liste ref L0/L1 default 0; `has_b_frames=0` | Bitstream di riferimento condiviso da stock e clip. |
| B | overlay generati da Chronon (`VELOX_OVERLAY_VIDEO_ONLY_V1`) | POC type 2 come A, ma `max_num_ref_frames=3`; PPS L0 default 2 e `weighted_pred=0` | Profilo nominale uguale, SPS/PPS diversi. |
| C | mappe importate (`overlay_import`) | `pic_order_cnt_type=0`, `log2_max_pic_order_cnt_lsb_minus4=2`, max ref 4; PPS `weighted_bipred_idc=2`; `has_b_frames=2`, reorder 2, decoder buffer 4 | Differenza strutturale, non correggibile dichiarando un altro profilo. |

Dimensioni, crop, crominanza e livello sono uguali nei campioni esaminati; la
causa specifica è la diversa configurazione di reference/POC e PPS, con B-frame
nelle mappe. Il decoder del file concatenato conserva il parametro del primo
segmento e incontra i cambi ai confini: questo spiega i flash verdi/arcobaleno
osservati. Le sorgenti non risultano corrotte.

**Come adattare gli overlay senza ricodificare il video finale:**

1. Fissare la famiglia A come bitstream canonico, estratto da una sorgente
   stock/clip certificata, e registrarne i fingerprint SPS e PPS.
2. Per Chronon, configurare encoder e muxer affinché emettano gli stessi campi
   della famiglia A: POC type 2, un reference frame, nessun B-frame, PPS con
   gli stessi reference default e weighted prediction, VUI/SAR/colour/crop
   equivalenti. Rigenerare il solo overlay dalla scena grafica con quelle
   impostazioni. Il solo `profile_id` non adatta un bitstream esistente.
3. Le mappe importate non sono copy-eligible finché restano codificate nella
   famiglia C. Fornirle come sorgenti non codificate e renderizzarle con la
   configurazione canonica, oppure rifiutarle prima dell'assemblaggio. Il
   secondo ramo non è valido se il final job dovrebbe convertirle.
4. Non riscrivere SPS/PPS a livello di byte negli MP4 esistenti: le slice sono
   state codificate assumendo quei parametri (POC, reference list e B-frame);
   una sostituzione può invalidare il flusso o produrre immagini errate.

**Gate fail-closed richiesto prima del final job:** estrarre SPS e PPS da ogni
segmento copy-eligible; confrontarne i fingerprint con la famiglia A; al primo
mismatch fermare il job e indicare asset, hash e campi divergenti. Poi
decodificare l'output completo e verificare i frame ai confini. Questo gate
non ricodifica il video finale. La rigenerazione conforme opera soltanto sugli
asset overlay a monte.

**Implementazione (2026-11-03, gate chiuso):** la famiglia A è il bitstream
canonico, con fingerprint avcC/SPS/PPS
`7201ce80a2b2b32a0c1a89501851099f267c1767d59ff799db744f72032aae5a`. Il producer
dei frammenti usa esclusivamente libx264/veryfast con `refs=1`, B-frame 0, GOP
chiuso 48 e colore bt709; NVENC e ogni altro encoder sono rifiutati in
configurazione, perché emettono parameter set diversi sotto lo stesso profilo
nominale. Ogni frammento copiato o encodato viene misurato: una copia con avcC
diverso viene scartata e ricodificata, un encode che non produce l'hash canonico
fallisce. RenderingGen pinna lo stesso hash sul profilo assembly-ready e propaga
`codec_config_sha256` nei fatti certificati; il profilo overlay V2 è una nuova
lane di cache per la SORGENTE, non una promessa sull'avcC, perché il producer
normalizza la sorgente prima dell'assemblaggio. Il gate Go e il gate Rust
richiedono e confrontano l'hash su ogni input; il mux finale forza timescale
90000 e decodifica l'intero file. Test eseguiti: gate Go/Rust, producer dei
frammenti, parser avcC e concat packet-copy di due frammenti canonici con
decodifica completa pulita e hash preservato. `[NOT RUN]` run end-to-end del job
P4 sul worker e confronto dei confini del filmato finale. Le mappe importate
(`overlay_import`) restano un gate di lavoro separato: le sorgenti non canoniche
vengono ricodificate dal producer, non dichiarate risolte.

### Copy opportunistico con encode certificato (2026-11-03)

Il requisito zero-encode è stato ritirato dopo il run live r13: una sorgente
stock non aveva closed GOP e B-frame zero, quindi la modalità copy-only
obbligatoria interrompeva il job senza produrre video. Le finestre allineate e
certificate restano packet-copy; quelle off-GOP, non certificate o con copia
non verificabile passano a libx264/veryfast con `refs=1`. Ogni output, copiato
o encodato, deve avere l'avcC canonico prima della pubblicazione. Ogni percorso
encode registra `copy_failure` con la ragione e mantiene `encode_ms` separato
da `copy_ms`. Test: `TestFinalJobCopiedWindowTimingReceipt`,
`TestFinalJobSegmentPreparerCopiesCertifiedWindows`,
`TestFinalJobSegmentPreparerEncodesOneAssemblyReadyFragment`.

**Producer C++.** `NativeAvSink` ora pinna `profile=High`, `level=4.0`,
`refs=1` e `AV_CODEC_FLAG_CLOSED_GOP` per **tutti** i producer H.264, non solo
NVENC; il ramo libx264 non è più quello che può divergere. `[VERIFICATO
2026-11-03]` compilazione: la TU `src/media/video/encode/native_av_sink.cpp`
compila con la command line reale di `build/chronon/linux-video-release`
(`CHRONON3D_ENABLE_NATIVE_FFMPEG=ON`, `ENABLE_VIDEO=ON`) e `-fsyntax-only`
esce 0. La prima compilazione ha trovato un errore reale: questa generazione
FFmpeg dichiara `FF_PROFILE_H264_HIGH`, non `AV_PROFILE_H264_HIGH`; il profilo
ora usa la stessa guardia di
`apps/chronon3d_cli/utils/video/ffmpeg_profile_constants.hpp`. `[NOT RUN]` link
della build completa e run su GPU: il gate Go continua a verificare l'avcC
reale, quindi un producer non conforme viene rifiutato a valle e non è mai
dichiarato conforme per configurazione.

## Secondo audit 2026-11-08 — disposizione delle voci

Audit: PipelineGen `38443128` + RenderingGen `8141b1df`. Una voce è CHIUSA solo
se il codice la implementa e un test la pinna; le voci non raggiungibili in
questa tornata restano APERTE con il motivo, non vengono mascherate.

| # | Voce | Stato | Evidenza / motivo |
|---|---|---|---|
| 1 | Asset già presenti sul Producer ri-registrati dal renderer | APERTA | Serve un percorso CAS locale esplicito e sicuro, visibile al renderer su host condiviso. Non introdotto: un fallback a path arbitrari sarebbe peggio del trasferimento. `Settle()` resta locator-first. |
| 2 | `RenderingGen → Producer → worker finale` scarica l'MP4 localizzato | PARZIALE | Il contratto ora trasporta `SizeBytes`, `StorageKey`, `ArtifactURL` su `RenderFacts` → `LocalizedClipArtifact` → `LocalizedRenderResult`. `renderedClipAssetRef` firma un clip remote-only certificato senza download, `os.Stat` o HEAD. Resta il `Materialize()` lato Producer perché la localizzazione pubblica ancora la clip su Drive: una modalità locator-only richiede che la richiesta provi che la clip non ha una destinazione Drive autonoma. |
| 3 | Cache audio finale: copia fisica e SHA integrali a ogni hit | CHIUSA sul I/O | `stageCachedFinalAudio` preferisce un reflink copy-on-write (Linux) e, in fallback, copia e calcola lo SHA nello stesso passaggio. Il file staged resta indipendente. Test: `TestFinalAudioCacheHitStagesIndependentBytes`. Il riferimento diretto al blob immutabile non è adottato: il consumatore riceve ancora byte run-owned. |
| 4 | Preparazione ripetuta per ogni chunk | APERTA | Richiede un prepared render package condiviso e versionato in RenderingGen (`package_id + frame_range`), non un'ottimizzazione locale al Producer. |
| 5 | `overlay.prepare` preventivo non giustificato | APERTA | La decisione va presa su `warm_bytes_reused`, worker di esecuzione e tempo sottratto al percorso critico; senza quella misura si rischia di sostituire un beneficio reale con una coda più lunga. |
| 6 | Doppia codifica degli stock estratti via mirror | APERTA | `mirror_sections` consegna file per sezione; eliminare l'encode intermedio richiede che il cutter accetti sorgente + finestra attraverso il contratto di staging. `-c copy` da solo non conserva l'esattezza del taglio. Nessun cambio non misurato. |
| 7 | Mappe: scaling/encode condizionale e verifica completa | APERTA | Va misurato se rendere direttamente alla geometria finale costa meno; la certificazione del renderer può sostituire la verifica indipendente solo se è equivalente. |
| 8 | HEAD duplicati per lo stesso oggetto final-job | CHIUSA | Clip locale e audio usano il riferimento restituito da `PublishFinalJobLocalObject` (una sola verifica); gli artefatti remoti già certificati usano `signCertifiedFinalJobObject`, che non fa HEAD. Test: `TestRenderedClipLocalPublicationUsesOneHead`, `TestFinalJobAudioPublicationUsesOneHead`, `TestCertifiedRemoteClipSignsWithoutLocalBytesOrHead`. |
| 9 | Clip riletta integralmente su cache hit | CHIUSA sul consumatore | `certifiedOutputDigest` già riusa lo SHA certificato dal confine; il nuovo `SizeBytes` evita anche lo `Stat` nel handoff final-job. `reusableBytes()` continua a ri-hashare la cache locale, che è mutabile per contratto: un blob remoto content-addressed non viene riletto. |
| 10 | Metadati stock già certificati interrogati di nuovo su Drive | CHIUSA | `stockDriveReference` usa `Asset.SizeBytes()` della riga di registro e, se assente, `MediaSearcher.DriveLocationSize` su `asset_locations.file_size_bytes`; `GetFileMeta` resta solo quando entrambe le fonti locali non hanno una dimensione. |
| 11 | Render di clip coperte al 100% da overlay replace | APERTA | Richiede finestre overlay congelate prima del fan-out e la prova che la clip non venga pubblicata autonomamente; non è sicuro approssimarla. |

Test eseguiti per questa tornata: `go build ./internal/...`; package
`internal/capabilities/scripts`, `internal/capabilities/localization/...`,
`internal/app/wiring`, `internal/kernel/asset` (vedi comando di verifica nel
commit). Le voci 1, 4, 5, 6, 7 e 11 restano lavoro aperto, non regressioni.

## Terzo audit 2026-11-08 — tagli I/O eseguiti e limiti provati

Questa tornata elimina i passaggi che non aggiungevano una verifica. Ogni taglio
ha un test che pinna il confine di sicurezza; i tagli che non potevano restare
fail-closed sono stati **rifiutati**, non mascherati.

| # | Taglio | Stato | Evidenza |
|---|---|---|---|
| 1 | Clip locali final-job ri-hashate prima del PUT | CHIUSO | `renderedClipAssetRef` usa `PublishFinalJobLocalObjectVerified`: lo SHA/size arrivano dal certificato del render per quegli stessi byte. Il probe di disponibilità resta; un miss non viene ripetuto prima del PUT. I client HEAD (20 s) e upload (10 min) sono package-level con keep-alive. Test: `TestFinalJobLocalPublicationUsesCertifiedIdentity` (il PUT avviene senza ri-hash locale), `TestFinalJobLocalObjectProbeMissIsNotRepeated` (1 HEAD, 0 PUT su byte non certificati), `TestFinalJobObjectPublicationChecksAvailabilityOnce`. |
| 2 | Parent finalization: hash + upload + warm L2 | PARZIALE / MISURATO | `storage.PutStagedFile` calcola lo SHA durante lo stream e il server lo verifica; `artifactFromFile + PutFile` sono sostituiti dal percorso staged con fallback. Le tre letture diventano **due**: upload+hash in un passaggio, poi la copia L2 necessaria a `publishAndVerify`. La copia L2 non è stata rimossa perché il publisher risolve l'artefatto dalla cache locale; saltarla sposterebbe lo stesso costo su un fetch L3. Test: `TestParentFinalizerPublishesThroughTheStagedLane`, `TestPutStagedFileStreamsHashesAndWarmsL2`. |
| 3 | Overlay import: hash + upload duplicati | CHIUSO | `ImportOverlayArtifact` usa `PutStagedFile`, poi confronta SHA e size restituiti con `source_sha256`/`source_size_bytes` della richiesta prima di accettare l'artefatto. Probe, decode completo e provenienza restano invariati; il fallback classico resta per i backend senza staging. Test: `TestImportOverlayArtifactIsByteIdenticalAndDoesNotInvokeChronon` (verifica anche una sola upload staged). |
| 4 | Prefetch local hint: verifica + upload | CHIUSO | Il percorso locale usa la sessione di staging pendente: `PUT /objects/staging` hasha il file mentre lo legge, il client confronta il digest prima del `finalize` e un mismatch viene cancellato con `DELETE` senza mai rendere l'oggetto visibile. Il file locale è letto una volta; nessun PUT diretto riceve un hint errato. I backend senza staging (404/405) mantengono la verifica preventiva memoizzata e il PUT diretto classico. Test: `TestHTTPAssetPrefetcherStagesLocalAssetThroughPendingSession` (1 staging PUT, 1 finalize, 0 direct PUT), `TestPrefetch_FailsClosedWhenNoVerifiedSourceExists`, `TestPrefetch_RejectsLocalHintWhoseBytesDoNotMatchTheAddress`. |
| 5 | Re-hash di mappe/plate/BGM invariati | RIFIUTATO / NESSUNA MODIFICA | Il tentativo con `digest.NewVerifier(nil)` è stato rimosso: la memo è valida solo per `(size, mtime)` e `TestDynamicMapVideoCacheRoundTripAndRejectsCorruption` ha servito un file manomesso di pari dimensione. Per file mutabili (cache mappe, plate generate, BGM locale) il re-hash completo resta l'unica prova; non è stato indebolito. |
| 6 | Size stock in batch | APERTO / DEFERITO | `DriveLocationSize` è una query locale per asset, non più un round-trip Drive. Un `ANY($1)` batch ridurrebbe N query a una, ma richiede cambiare l'interfaccia del registry e i suoi test di integrazione; il guadagno residuo è piccolo rispetto al rischio. Le righe con size già registrata non fanno alcuna query. |
| 7 | Hash overlay duplicato dopo il probe | GIÀ CHIUSO / NO-OP | `MediaContractProber.ProbeOverlay` restituisce `SHA256` e `SizeBytes` nello stesso risultato; il chiamante usa quei valori. Non esiste un secondo `SHA256File` nel percorso. |

Verifica eseguita: build e vet di `refactored/internal/...` e dei package
RenderingGen `internal/storage` + `internal/processor`; test completi di
`internal/storage`, test mirati `TestParent*`, `TestImport*`, `TestTailUpload*`
e `TestPutStagedFile*`; test completi di `internal/capabilities/scripts` e
`internal/app/wiring`; test mirati prefetch di
`internal/platform/renderinggen` (`TestHTTPAssetPrefetcher*`, `TestPrefetch*`,
`TestVerifiedLocalPath*`, incluso
`TestHTTPAssetPrefetcherStagesLocalAssetThroughPendingSession`). La suite
**completa** di
`internal/platform/renderinggen` non è verde su questa macchina: i test
`clip_executor` partono senza `RENDERINGGEN_STORE_URL` e usano il default
`127.0.0.1:9000`, che risponde **401** già durante il prefetch, prima dei
percorsi modificati; non è una regressione di questi tagli. Restano aperti, come
da tabella precedente, i tagli che richiedono un nuovo contratto (prepared
package, finestre overlay congelate, cutter source+window) e il `Materialize()`
lato Producer della voce 2.
