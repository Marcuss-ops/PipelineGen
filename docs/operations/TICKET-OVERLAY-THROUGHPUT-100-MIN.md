# Ticket: portare la generazione degli overlay a 100/minuto

Stato: **APERTO — baseline 27,2 overlay/min (mediana 3 run) → 37,8/min dopo la 2ª lane di render (2° daemon, v11, +39%); target 100/min non raggiunto. 23/10: profilazione per-item (servizio seriale 2,75 s, di cui ~0,93 s di receipt dentro la reservation) e fix list; poi implementata la variante **Go-only** di fix 1+3 (senza toccare Chronon3d): il decode del receipt è spostato fuori dalla lane nel post pool di RenderingGen e Chronon è richiesto al tier `fast` sotto policy `normal`. Proiezione: servizio lane ~2,0 s/item → ~57–60/min a 2 lane, ~85–90/min a 3 lane. Per chiudere il gate a 2 lane restano le fix Chronon (2/4/5).**
Priorità: **P1**  
Ambito: generazione, refactoring e rendering degli overlay nei job video YouTube di PipelineGen / RenderingGen.

## Obiettivo

Passare dall'attuale velocità riferita di circa **6–7 overlay/minuto** a una capacità sostenibile di **100 overlay/minuto** per i job Cronon, variabile in funzione del numero di overlay richiesti. La metrica deve contare gli overlay effettivamente generati e validati, non quelli soltanto richiesti dal copione o pianificati.

Il ticket deve identificare e ridurre il tempo speso nelle fasi di generazione, materializzazione, rendering, retry e attesa in coda, mantenendo qualità e correttezza degli overlay.

## Evidenza storica disponibile

- La batch Garlasco da 10 video usa payload `generate` con un obiettivo di **40 overlay** per copione e consegna sequenziale. La manifest registra un conteggio osservato tra **13 e 36 overlay**: il target dichiarato non coincide sempre con quelli materializzati.
- Nella stessa batch, alcuni job hanno richiesto correzioni mirate: un video ha limitato `max_overlays_per_scene=1`; un altro ha disabilitato le mappe dopo errori deterministici. Sono stati registrati anche overlay entity-card in conflitto con date-card, oltre a tentativi cancellati e risottomissioni.
- Perciò la prima opportunità misurabile è ridurre overlay mancanti, conflitti e retry, oltre ad accelerare il percorso riuscito. La documentazione attuale non contiene una misura omogenea overlay/minuto né una scomposizione temporale di questi job.
- La velocità di 6–7/minuto è la baseline operativa riferita dall'utente e va confermata con timestamp e conteggio validato. Non è ancora una misura ricavata dai log.

### Prima ricostruzione misurata

I dieci risultati originali della batch conservano `timing.stages.overlay_render` e il numero di elementi del piano salvato. Il rapporto `elementi del piano / wall time della fase overlay_render` va letto come stima preliminare: i risultati storici non attestano in ogni caso che ogni elemento abbia un artefatto valido.

| Video | Elementi nel piano | Fase overlay (s) | Stima (overlay/min) |
| --- | ---: | ---: | ---: |
| 01 cronologia | 36 | 114,6 | 18,9 |
| 02 procedimento | 20 | 148,1 | 8,1 |
| 03 sentenza Stasi | 19 | 80,7 | 14,1 |
| 04 nuova indagine | 21 | 437,3 | 2,9 |
| 05 prove | 13 | 73,4 | 10,6 |
| 06 pedali | 18 | 163,4 | 6,6 |
| 07 presunzione | 17 | 75,1 | 13,6 |
| 08 fonti e media | 13 | 454,2 | 1,7 |
| 09 Chiara Poggi | 20 | 369,2 | 3,3 |
| 10 sintesi | 16 | 94,4 | 10,2 |

La media preliminare è **9,0/minuto**, la mediana **9,2/minuto**. Due job richiedono oltre sette minuti nella sola fase overlay pur avendo appena 13 elementi. È il segnale più forte per profilare mappe, image/entity-card e loro passaggi di materializzazione/pubblicazione, invece di aumentare alla cieca il parallelismo. La finestra non include l'attesa in coda.

La batch finale contiene anche tre retry con cause chiaramente non risolvibili ripetendo lo stesso checkpoint: file audio locale temporaneo assente, asset stock assente dal media SSOT e clip intro non risolvibile. Un precedente conflitto entity-card/date-card era garantito dal contratto `mode=replace`; il codice corrente contiene già uno scheduler di frame che prova a spostare l'overlay nella prima finestra libera della stessa scena e una validazione prima dell'invio al worker. Se nessuna finestra valida esiste, il payload fallisce localmente prima di consumare un tentativo del worker remoto.

### Collo di bottiglia overlay individuato

Nel job `08_fonti_media` la fase overlay dura **454,2 s**, ma le metriche registrano solo **41,0 s** di render Chronon e **43,2 s** di attesa terminale del queue per 13 elementi. La differenza di circa **370 s** non aveva un'operazione Drive propria nei dati storici; l'attribuzione alla pubblicazione seriale era una forte inferenza dal percorso del codice e dal tempo non spiegato.

**Verifica eseguita il 22 ottobre 2026 (post-fix, metrica `overlay_upload`): l'inferenza è risultata NON sostanzialmente corretta.** L'analisi per-item della finestra (`run_operation_observations.created_at`) mostra che il residuo è **concentrato in uno-due item pesanti, non distribuito per item**: in `08_fonti_media` gli item 1–11 chiudono in 2,0–5,5 s l'uno con ~0 s non misurati ciascuno, poi **l'item 12 blocca 383,5 s (373,3 s non misurati)** e l'item 13 altri 40,2 s; in `05_le_prove` (stessi 13 item, wall 73,4 s) l'unico residuo è 37,9 s sull'item 13. Una pubblicazione seriale per-item produrrebbe N piccoli gap, non un blocco unico da 373 s. La controprova v1 (stesso percorso seriale pre-fix, 39 item): l'overhead non-render è **~2,6 s/item** (226,2 s di finestra − 123 s di attese), che include già il publish Drive seriale. Le prime misure `overlay_upload` danno ~2,3 ms/item sul percorso async (enqueue outbox).

La causa dominante del residuo va quindi attribuita alle **fasi per-item non proiettate nella telemetria delle operazioni** degli item pesanti (mappe/immagini): le mappe misurate direttamente sul golden viaggiano a 42–90 s/item con prefetch tile/prepare/encode che non compaiono in `run_operation_observations` (`renderinggen.encode` storico medio 0,13 s contro i 64,8 s di encode mappa osservati nei log del worker). La pubblicazione Drive seriale resta un costo reale ma minore (ordine di 2–5 s/item), già rimosso dal fix e ora misurato.

Correzione implementata (commit `5538ed8d6`) e ora **attiva sul binario in esecuzione** (`8423ea114`, build 22/10 17:51 UTC): il child delega la pubblicazione al pool Drive limitato già esistente; il ramo overlay si ricongiunge al pool una volta completati tutti i child e prima che il final job costruisca il payload. La garanzia resta: nessun final render parte prima che gli overlay siano pubblicati e abbiano i Drive ID certificati. La metrica `overlay_upload` (stage `overlay_render`, component `drive`) registra il costo della pubblicazione: prime misure **22/10 18:01–18:03, 3 item, 2,3 ms/item medi** sul percorso async (enqueue outbox). L'upload reale avviene nel worker outbox (`overlay_drive_outbox.go` handler → `direct.PublishOverlay`), dove **non è ancora misurato**: chiudere il gap misurando nel handler. Il percorso sincrono residuo (compositi `:final-composite:`) registrerà il costo completo alla prima run final-job riuscita.

Modifica in `internal/capabilities/scripts/runner_lifecycle.go`: il run pianifica retry solo per errori marcati transient dal contratto tipizzato condiviso, oltre alle deferral esplicite per un final render remoto ancora in esecuzione e al caso recuperabile dell'audio locale mancante. Quel caso ora riprende da `audio_compile`, azzera il solo riferimento al file scomparso e conserva gli overlay già renderizzati; non ripete più la stessa pubblicazione sullo stesso path inesistente. Gli altri errori deterministici e non classificati cessano di ripetere automaticamente il lavoro invariato. I confini che possono recuperare un errore transitorio devono propagare `retry.TransientInfrastructureError`.

Riferimenti: `ops/jobs/garlasco_batch_10_20261019_sequential_v7/manifest.json`, i payload `*.generate*.json` della stessa cartella e `TODO-milton-finaljob-performance.md` per precedenti misure di fasi overlay e tempi di job (non direttamente comparabili finché non vengono normalizzate).

### Golden case v2 — run live overlay-only (22 ottobre 2026)

Payload immutabile: `ops/jobs/overlay_throughput_golden_100_20261022.v2.generate.json` (SHA-256 `60fb14ec1e6ace95489f38e2b84f91dd7a28d45d89edbcf1e5c060a52d63080d`). Deriva dal JSON reale `08_fonti_media.generate.v11.json`: mantiene i 20 segmenti, le impostazioni editoriali e le tre clip intro già selezionate (`yt_CHn2h59EptE_2235_2250_v1`, `yt_CHn2h59EptE_2235_2280_v1`, `yt_CHn2h59EptE_2520_2565_v1`), con progetto e correlazione dedicati. Il mix richiesto è 25 frasi brevi, 25 lunghe, 30 immagini (10 schede entità comprese), 10 date e 10 mappe; `max_overlays_per_scene=10`. Il riuso della cache è abilitato, il refresh forzato degli asset è disabilitato e `final_job=false`: la misura termina al percorso overlay senza generare il video finale.

La prima prova live v1 con `final_job=true` è stata interrotta dopo che la fase overlay era terminata. Il job `job_1792685428589251856_a9f967b8` ha renderizzato e pubblicato 39 overlay tra le 16:15:58 e le 16:19:16 UTC; il final job remoto è poi fallito per `timeline segment 33 has zero frames`, con due overlay numerici mancanti in prefetch. Il binario attivo `53c200925` (avvio 14:38 UTC, precedente alle correzioni) ha pianificato un retry sullo stesso errore terminale; il job è stato annullato alle 16:23 UTC per non spendere altra capacità sul video finale. Il conteggio 39 è inferiore ai 100 richiesti: questa prova non certifica il golden.

La run v2 overlay-only **non ha completato** e non costituisce baseline di throughput: job `job_1792686265115244060_107c6817`, inviato alle 16:24:25 UTC sul binario `53c200925` (processo avviato alle 14:38 UTC, quindi precedente alle correzioni). È stato annullato alle 16:39:06 UTC dopo due fallimenti identici. `final_job=false`; nessun video finale è stato richiesto o prodotto.

### Run v3 e v4 — asset image catalog e verifica della bonifica (22 ottobre 2026)

La v3 (`job_1792692486237544004_02e3b631`, correlation `overlay-throughput-golden-v3-postfix-20261022T180804Z`) è partita sul binario `805b1f931` alle 18:08:06 UTC e si è chiusa `FAILED` alle 18:13:29 UTC: **323,0 s (5:23)**, `retry_count=0`. Ha pianificato 38 overlay/20 scene e fallito al prefetch del riferimento SHA-256 `fda374e1…fbc2` (HTTP 422), dopo 3 submit di render. Non è stato prodotto un video finale (`final_job=false`). Il residuo era nel catalogo immagini: candidato DuckDuckGo `36893`, `person:chiara-poggi`, URL Vanity Fair, aveva `legacy_file_md5=fda374…` ma `file_hash` vuoto e una materializzazione Drive non verificabile. I byte scaricati dall'URL al controllo avevano SHA-256 `50a2e672…afc229`, quindi non corrispondevano alla chiave storica. La v3 aveva anche 2 mappe costate 20,66 s e 37,37 s (`process_wall_s`).

La correzione di stato aggiunta in PipelineGen fa preservare da `UpsertCandidate` entrambi gli stati terminali operativi `broken` e `retired`: una nuova osservazione dello stesso URL non prova che una materializzazione già registrata sia valida. Soltanto acquisizione, verifica e materializzazione riuscite possono rimettere il candidato in `fresh`. La regola è coperta da un test adapter e documentata in `docs/entity-image-catalog-state-policy.md`. Test eseguiti: `go test ./internal/platform/sqlite/assets/entitycatalog ./internal/capabilities/scripts/adapters -count=1` (pass). Binario installato tramite deploy ufficiale, build `805b1f931`, SHA-256 `c4cd9effd591e158c6e7da51b564153019155570864f2471b55f1d6a50879ac0`; health e hash di `/proc/<MainPID>/exe` corrispondono.

La v4 (`job_1792693000903602853_dd8a6a84`, correlation `overlay-throughput-golden-v4-broken-state-fix-20261022T1817Z`) è terminata `FAILED` dopo **374,1 s (6:14)**, sempre sul medesimo SHA fda con HTTP 422 e `retry_count=0`. La lettura del database ha chiarito perché non era una verifica valida della nuova regola: la v3 aveva lasciato candidato `36893` nuovamente `fresh`; v4 è partita da quello stato e quindi non ha esercitato la transizione `broken → upsert provider`. Corretto il setup in modo esplicito: prima di v5 il candidato è stato rimesso `broken` tramite `SQLiteEntityImageCatalogAdapter.SetCandidateStatus`, verificando che l'asset materializzato resti quello fda. Il job v5 è ora il test live effettivo; registrare qui sotto l'esito terminale e i timing prima di dichiarare risolto il riferimento.

**Profilo v4 da `GET /api/jobs/<id>/full`:** 374.089 ms end-to-end; attribuiti 373.925 ms, residuo non attribuito 164 ms (0,044%), overlap 110.233 ms. Piano: **39 elementi/20 scene**, non 100; 4 submit overlay prima del fallimento; nessun throughput valido, perché il job non ha completato. La fase overlay ha consumato 110,484 s, con 4 wait queue (15,775 s sommati), 4 render Chronon (15,033 s sommati), 208 ms object-store upload; il tempo restante include lavoro mappe non interamente rappresentato nelle operazioni render. Due mappe Chronon hanno chiuso rispettivamente in 43,77 s e 39,11 s (`process_wall_s`, cache tile: 4.475 hit, zero miss/download per ciascuna). La terza non è attribuibile come completata prima del fallimento.

| Fase v4 | Wall ms | Note |
| --- | ---: | --- |
| `generate` | 126.880 | 20 chiamate Ollama; 264.595 ms di work cumulativo, 79.114 ms queue wait, 26.272 ms model load |
| `scene_analysis` | 107.265 | 20 risoluzioni Artlist; 296.899 ms work cumulativo, parallelizzato |
| `prepare_join` | 68.210 | preparazione media/join |
| `voiceover` | 57.603 | TTS 20 chiamate; 211.883 ms work cumulativo |
| `publish` | 123.064 | fase concorrente con le altre, non sommare al wall totale |
| `audio_compile` | 71.293 | AAC encode 51.338 ms work; render audio Rust 60.604 ms work |
| `overlay_render` | 110.484 | 39 pianificati, 4 submit prima del 422; renderinggen render work 15.033 ms |

Sequenza completa del job v4: submit/idempotency → lease worker → generazione script (20 chiamate Ollama) → traduzione (32 ms) → analisi scene/Artlist e preparazione join → TTS e pubblicazione voiceover con timing per 20 scene → compilazione audio → generazione piano overlay (39 elementi, 20 scene) → render di due mappe Chronon (43,77 s + 39,11 s) e ulteriori item → quarto submit fallito sul vecchio hash immagine → job `FAILED`, nessun replay automatico. L'outbox ha differito gli eventi finché il parent era `RUNNING` con delay 60 s e senza bruciare tentativi; al fallimento l'evento ha seguito la normale chiusura del parent.

### Run v5 — prova live con candidato marcato broken prima del refresh

Job `job_1792693410789283024_bed1c33a`, build `805b1f931`/SHA `c4cd9eff…`, idempotency `overlay-throughput-golden-v5-broken-state-fix-20261022T1824Z`. Candidato `36893` marcato `broken` prima del submit e rimasto tale dopo discovery/refresh; le scene Chiara 01 e 11 selezionano l'alternativa valida `e039c138…`. La run fallisce dopo **378,133 ms (6:18)** al 32° submit: un diverso asset fda `38e48a50…` in scena 14 risponde HTTP 422. Il mapping DB identifica il secondo riferimento come candidato `36469`, `person:andrea-sempio`, materializzazione con `asset_id=7fb55580…` ma `legacy_file_md5=38e48a50…`; anche `file_hash` è vuoto. Candidato 36469 marcato `broken` con lo stesso adapter canonico prima della v6. Il problema dunque comprende più materializzazioni persistenti non coerenti, non solo il ritratto di Chiara.

**Timing v5 (`GET /api/jobs/<id>/full`):** wall/execution 378.133 s; attribuiti 377.998 s (99,964%), 135 ms non attribuiti; overlap 136.378 s. Piano 40 elementi/20 scene; 31 submit/render Chronon completati prima del 422 sul 32°; `retry_count=0`; non è un throughput accettabile certificato perché il job è `FAILED` e 9 overlay pianificati non sono stati processati. Le metriche hanno 31 render/encode/materialize/hash/upload di output, con 105.475 s cumulativi di wait del queue e 96.052 s cumulativi di rendering; 1.208 ms objectstore upload. Il primo map item registrato costa 27,35 s `process_wall_s` (63 frame, cache hit 4.475, zero tile download; fetch 61,72 s cumulativi internamente, prepare 6,63 s, encode 15,80 s).

| Fase v5 | Wall ms | Work/lettura |
| --- | ---: | --- |
| `generate` | 138.707 | bottleneck dichiarato dall'API; 20 generate work 249.499 ms, queue wait 74.319 ms, warm model 19.043 ms |
| `scene_analysis` | 82.055 | 20 Artlist resolve, work cumulativo 253.927 ms |
| `prepare_join` | 57.563 | fase preparatoria |
| `voiceover` | 50.150 | TTS 20 chiamate, work cumulativo 188.450 ms |
| `publish` | 87.427 | fanout concorrente, non additivo al wall totale |
| `audio_compile` | 115.574 | AAC encode 95.319 ms e Rust audio render 104.526 ms di work |
| `overlay_render` | 120.433 | 31 render completati, errore deterministico sull'item successivo; wait totale 105.475 s |

### Correzione cache e run v6

La v5 ha isolato un secondo percorso: le risposte cache/provider conservano i candidati già materializzati; il catalogo li registra come `broken`, ma il risultato cached poteva ancora essere restituito al selettore prima della materializzazione. In PipelineGen è stato aggiunto `excludeUnavailableEntityImageCatalogCandidates`, applicato dopo l'upsert sia ai risultati L1/L2 cached sia ai risultati freschi di provider nel media resolver e nel fanout VidRush. I candidati corrispondenti a URL `broken` o `retired` vengono esclusi dal set consegnato al materializzatore; i candidati non presenti nel catalogo e i candidati `fresh` restano disponibili. Test mirato `TestCachedEntityImageResultsCannotBypassBrokenCatalogState` e pacchetti `internal/capabilities/scripts/adapters`, `internal/platform/sqlite/assets/entitycatalog` passati.

Build installata e identità verificata: SHA-256 `ad6b98debf0d1082590f4b4b6282c9b7060fb7cc614b25e977152c2873f044c2`, health `git_commit=805b1f931`, build time `18:30:49Z`. Prima del submit v6, candidati `36893` (Chiara) e `36469` (Andrea) entrambi risultavano `broken` nel DB. Job `job_1792693936932863369_c1fd5858`, idempotency `overlay-throughput-golden-v6-cache-state-filter-20261022T1832Z`, `final_job=false`.

**Ulteriore causa radice trovata durante v6:** subito dopo il riavvio, il processo di startup `entity image catalog recertification` ha selezionato 3 URL; 2 richieste HTTP riuscite hanno riportato `fresh` proprio i candidati 36893 e 36469, aggiornati alle 18:33:08, nonostante entrambi avessero ancora materializzazioni ObjectStore non valide. Il servizio (`RecertificationService`) verifica solo che l'URL remoto risponda e non legge/valida il digest Drive/ObjectStore, ma `RecordCandidateValidation` trasformava comunque `broken -> fresh`. I due candidati sono stati rimarcati `broken` dopo la recertificazione e prima del passaggio v6 a discovery/materializzazione; le scene Chiara hanno selezionato `e039…`, Andrea `95d6…`, nessuno dei due vecchi riferimenti fda. Correzione sorgente aggiuntiva, testata ma ancora da installare dopo v6: una sonda remota positiva lascia `broken` in `broken` e consuma il budget URL-only; solo `SetCandidateStatus(fresh)` dopo un nuovo Acquire→Verify→Finalize azzera il budget. I test dei tre pacchetti catalog/entitycatalog/scripts-adapters passano. La build v6 in esecuzione include invece già il filtro di cache contro URL broken/retired.

**Esito v6 (run golden riuscita, 22 ottobre 2026):** `SUCCEEDED` alle 18:39:19 UTC, un solo attempt, wall `421.858 ms` (submit 18:32:16, worker start 18:32:17, terminale 18:39:19), queue wait 1.000 ms, `final_job=false`. Sono stati pianificati, sottoposti, materializzati e verificati **37 overlay su 37**; nessun video finale e nessun 422. Il filtro cache ha scartato le vecchie materializzazioni e riutilizzato le alternative `e039…` per Chiara e `95d6…` per Andrea. Throughput end-to-end del lavoro riuscito: **37 / 7,031 min = 5,26 overlay/min**. Throughput limitato alla fase overlay: **37 / 127,904 s = 17,36 overlay/min**. Il target configurato di 100 resta solo un cap/mix richiesto: il planner v6 crea 37 item, non 100; questa run non certifica il target 100/min.

| Fase v6 | Wall ms | Dettaglio misurato |
| --- | ---: | --- |
| Generazione script (`generate`) | 135.988 | bottleneck API; 20 chiamate Ollama, work 272.941 ms, queue wait 80.904 ms, model load 31.832 ms |
| Analisi scene (`scene_analysis`) | 91.278 | 20 Artlist resolve, work cumulativo 281.355 ms; max chiamata 91.147 s |
| Preparazione media/join (`prepare_join`) | 54.153 | percorso pre-audio/render |
| TTS/voiceover | `voiceover` 56.968; `tts` 95.092 | 20 TTS, work cumulativo 213.597 ms; si sovrappone ad altre fasi |
| Compilazione audio (`audio_compile`) | 128.355 | Rust audio render 115.626 s work, AAC encode 99.312 s work; serve ai timing overlay anche con `final_job=false` |
| Rendering overlay (`overlay_render`) | 127.904 | 37 submit, wait cumulativo 131.611 s, rendering 116.328 s cumulativi, 37 probe/materialize, 1.575 s objectstore upload; le chiamate sono concorrenti e i work non si sommano al wall |
| Pubblicazione (`publish`) | 117.298 | operation breakdown attribuito al finalizer e sovrapposto; non sommare al wall end-to-end |
| Chiusura writer (`post_writer_finalize`) | 16.865 | 3 chiamate Drive publish, work Drive cumulativo 27.519 s |
| Pubblicazione audio (`audio_publish`) | 15.495 | 1 upload Drive, work 15.382 s |

Verifica deploy successiva alla correzione di ricertificazione: build `805b1f931`, SHA-256 binario `fe4415246f77b4077b5abfdbbdb5820f707a6c2e52d491be2f4d6601bbed5c85`, health `healthy`, processo PID `1047695`. Dopo il ciclo di startup del 22/10 18:42:12 UTC, candidati `36893` e `36469` sono rimasti `broken`, entrambi a `validation_attempts=5`: il probe URL non ha più riabilitato una materializzazione con hash storico non verificato.

### Verifica puntuale attese di polling — v6

Interrogata direttamente `render_attempt_analytics` per la run `run_bfda3153-f152-4dbe-b866-6032a7d85563` (37 item). Il totale `completion_wait_ms` è **131.611 ms**, ma `polling_sleep_ms=0` e `poll_count=0` per tutti i 37 item; `polling_interval_ms=250` è solo la configurazione fallback. Il percorso usato è il long-poll event-driven (`RenderQueueWaiter`), perciò i 131,6 s cumulativi sono latenza di completamento osservata mentre i render lavorano/concorrono, **non tempo perso dormendo tra poll**. Sui timestamp disponibili: `queue_queued_at → queue_started_at` somma ~0,505 s; `queue_completed_at → artifact_available_at` ~1,771 s; worker `render_ms` somma 116,328 s e `encode_ms` 5,137 s. Queste sono somme per item e non wall time additivo; il residuo di `completion_wait_ms` va attribuito con attenzione ai timestamp mancanti/overlap, non chiamato polling. Correggere la diagnosi: non ridurre l'intervallo di polling per spiegare la v6. Il prossimo passo è confrontare per-item `submit_accepted_at`, `queue_started_at`, `queue_completed_at`, `artifact_available_at`, render/encode e tempi materializzazione per isolare la latenza sul critical path. Il polling rimane già misurato e separato per i fallback che lo usano.

I fix di stato catalogo e filtro cache/provider sono presenti anche nel checkout sorgente. Verifica mirata sul checkout: `go test ./internal/platform/sqlite/assets/entitycatalog ./internal/capabilities/images/entitycatalog ./internal/capabilities/scripts/adapters -count=1` — pass. Nessun commit o push eseguito. La run v6 rimane la baseline live più recente; non è stata rilanciata dopo il solo fix di ricertificazione perché quel fix è coperto dal test e la verifica startup ha confermato lo stato DB. Non attribuire alla v6 una misura per-scena di TTS/Rust: la nuova sezione «Audit end-to-end del tempo perso» esplicita i punti da strumentare e le ipotesi che il dato aggregato non risolve.

I map item sono tre e hanno chiuso con `process_wall_s` **65,65 s**, **26,53 s**, **17,58 s**: totale 109,76 s, circa l'86% dei 127,90 s della fase overlay. Tile cache: 4.475–4.567 hit e zero miss/download; il costo resta frame pipeline/encode (rispettivamente 48,23/48,64 s per la prima mappa) e plate prepare (11,32 s). Nelle mappe successive la durata scende con output più corto (67 e 56 frame). Questa è la causa concreta dominante della fase overlay nella run riuscita; il costo non sta nell'upload Drive degli overlay (68 ms misurati nel caller asincrono).

Timing totale è 421,858 s, attribuiti 420,919 s (99,78%), non attribuiti 939 ms; overlap rilevato 149,913 s. Le fasi elencate non vanno sommate: il parallelismo produce work cumulativo superiore al wall. Rispetto alla v5 (378,133 s, fallita al 32° submit), v6 richiede 43,7 s in più ma completa tutti i 37 item, perciò è il primo throughput osservato con output validati. Resta il gap tra i 37 item prodotti e i 100 item attesi dal golden, da risolvere nel planner/contratto del mix prima di dichiarare misurato l'obiettivo.

La sequenza osservata nei log (UTC) è:

| Fase | Timestamp/durata osservata | Evidenza |
| --- | --- | --- |
| Submit/worker start | 16:24:25 | L'API aggregata sul job annullato riporta `wall_ms=0`, `execution_wall_ms=0`, `queue_wait_ms=874000`: questi campi sono incoerenti con i timestamp e non vanno usati come misura. |
| Generazione testo + traduzione | completate 16:27:34 (~189 s dal submit) | Script generation completa alle 16:27:33.955; translation fanout chiusa alle 16:27:34.135. |
| Voiceover/TTS | completato 16:28:26 (~52 s dopo translation) | Prefetch audio iniziato alle 16:27:34; la composizione audio è necessaria anche con `final_job=false`. |
| Preparazione input overlay/piano | input 16:29:56; piano 16:30:27 | Il piano è ricompilato al resume; non abbiamo una misura affidabile e separata del wall time per tutte le sottofasi. |
| Rendering mappe al primo tentativo | tre item, 16:32:01–16:34:45; 242,4 s cumulati | item 030: 89,6 s (prefetch 15,7; prepare 18,1; encode 64,8); 031: 75,2 s (12,3; 13,9; 56,5); 032: 77,6 s (37,8; 41,5; 30,4). |
| Primo errore | 16:34:48 (~10m23s dal submit) | Durante `COMPILING_AUDIO`/overlay materialize: chiave `b5b91ac5…c501c`, contenuto scaricato SHA `13da47d2…a41b7`. |
| Retry legacy e rendering ripetuto | 16:34:54–16:38:48 | Resume da checkpoint `COMPILING_AUDIO`, attempt 2; stesso item e mismatch. Mappe ripetute per altri 153,9 s: item 030 60,3 s, 031 51,3 s, 032 42,3 s, anche a cache calda. Nuova selezione immagine (`e039…`) non ha rimosso il riferimento obsoleto `b5…` dal piano/checkpoint. |
| Secondo errore e stop | 16:38:48–16:39:06 | Stesso mismatch; il runtime vecchio ha schedulato attempt 3/3. Job cancellato per non pagare un terzo replay invariato. |
| Pubblicazione Drive | zero artefatti pubblicati per v2 | Gli eventi outbox ordinari venivano elaborati mentre il parent era `RUNNING`; `OverlayDriveLinkIsCurrent` rispondeva “job is not terminal” e il pool ritentava. Non conteggiare questi overlay come consegnati/validati. |

**Risultato netto v2:** zero overlay validati attestabili dal risultato terminale; 100 era il target richiesto, ma il numero pianificato, materializzato e validato va tenuto distinto. Nessun throughput/minuto valido è calcolabile. La fase mappe ha consumato almeno 396,3 secondi cumulati in due tentativi per soli tre item. La CPU era occupata anche dal compile audio/FFmpeg e dai renderer Python; non è stato prodotto un video finale.

**Difetto alla radice verificato e mitigazione operativa applicata:** il container ObjectStore attivo era stato creato il 18 agosto, prima dell'enforcement SHA-256 presente nel codice RenderingGen. `/health` non esponeva le capability content-address e `/objects/{key}/verify` restituiva 404. Il volume persistente contiene l'oggetto `b5b91ac5…c501c`: verifica in sola lettura HTTP 409, byte effettivi SHA-256 `13da47d2…a41b7`, diverso dalla chiave. Il rebuild del solo servizio ObjectStore, senza ricreare il volume, ora dichiara `sha256-v1`, `verify_on_write=true`, `verify_on_read=true`; una PUT negativa con chiave errata viene rifiutata con HTTP 422. Il blob errato non è stato cancellato o sovrascritto: non è stato trovato un asset canonico corrispondente nell'admin/media SSOT. Serve risolvere la provenienza/ripubblicare l'asset corretto e rigenerare il piano; non riusare il checkpoint contaminato.

Il codice PipelineGen ora classifica i retry tramite `retry.IsTransient`, più le due deferral/recuperabilità esplicite. Il runtime che ha eseguito questa prova era precedente a tale modifica e ha quindi ripetuto un mismatch deterministico. La nuova build non deve ritentare automaticamente un content-address mismatch: va verificato che il job termini con errore diagnostico senza replay. Rimane da correggere il percorso outbox: gli eventi di pubblicazione devono essere differiti fino alla terminalità del parent senza bruciare tentativi ravvicinati o essere dead-letterati prematuramente.

### Bonifica cache immagini, idratazione L3 e strumentazione (22 ottobre, 19:00–19:45 UTC)

Dopo v6 sono state completate quattro azioni, tutte verificate:

1. **Bonifica dei blob corrotti L3.** Scansione completa: 5 hash con blob corrotto (`verify` 409) più i 2 già noti con binding stale (`fda374e1…`, `38e48a50…`). I 5 blob sono stati prima **rihomeati sotto il vero hash dei byte** (es. `b5b91ac5…` → `13da47d2…`, confermando la provenienza già ipotizzata) e poi rimossi; le 7 materializzazioni e 13 entry cache corrispondenti eliminate, 7 candidati marcati `broken`. Verifica: vecchie chiavi 404, nuovi oggetti `verify` 200, zero residui DB.
2. **Audit completo delle materializzazioni (783 righe).** Download delle copie Drive certificate e confronto hash: **0 mismatch su 575 verificabili** — i hash registrati erano corretti; i fallimenti 422 nascono perché il render può riacquisire dalla **sorgente volatile** invece di risolvere dall'ObjectStore. 13 copie Drive risultano cancellate (HTTP 404) e 11 non raggiungibili (risposta HTML): 24 binding **in quarantena** (materializzazione + cache eliminate, candidato `broken`).
3. **Idratazione L3 completa.** Tutte le copie Drive certificate sono state riversate nell'ObjectStore sotto la rispettiva chiave (`verify_on_write` accetta solo byte coerenti): 574 oggetti idratati. Stato finale: **758/758 materializzazioni con oggetto L3 verificato, 0 binding senza bytes certificati**. Il prefetch al render diventa cache-hit: nessun 422 possibile da sorgente volatile. Misura di performance collaterale: niente più riacquisizioni rete imprevedibili nel critical path.
4. **Strumentazione nuova (build `65d0a823`, attiva dal 19:36 UTC).** (a) Le fasi del map renderer (`prefetch`/`prepare`/`encode` dal summary `chronon.dynamic-map-telemetry.v1`) sono ora proiettate in `run_operation_observations` (component `chronon`, stage `overlay_render`) via `projectMapRendererPhases`; (b) l'**upload Drive reale** nel worker outbox è misurato con `MeasureOperation` (`drive_publish`, component `drive`) su una delivery run per evento, joinabile al job via `run_observability.job_id`. Test: `go test ./internal/kernel/observability ./internal/capabilities/scripts ./internal/app/wiring` (pass). Voci aggiunte a `architecture/observability-measurement-matrix.yaml`.

Durante la run v8 la **macchina è stata riavviata** (~19:34 UTC): il tentativo 1 (16/27 item) è stato abbandonato per lease scaduto e il retry ha ripreso da zero — non un bug del lease, un'interruzione esterna. Da tenere presente come rischio di confrontabilità delle run.

### Run v7–v10 e baseline misurata

| Run | Esito | Item (piano/render/links) | Finestra render | Item/min | Note |
| --- | --- | --- | ---: | ---: | --- |
| v7 `job_1792695874959201370_6c8ecf6f` | FAILED, `retry_count=0` | 27 submit prima del 422 | — | — | Prima run strumentata: fasi mappa e upload Drive reali visibili per la prima volta. Asset stale `efca1c3a…` (422 al prefetch, scena 12): binding coerente sulla copia Drive, risolto con l'idratazione. |
| v8 `job_1792697082069586662_6ee46def` | **SUCCEEDED** | 41 / 41 / 44 | 83,4 s | **29,5** | Tentativo 1 interrotto da reboot macchina (v. sopra); tentativo 2 pulito. 1 mappa: prefetch 3,6 s + prepare 4,7 s + encode 11,8 s. |
| v9 `job_1792698395739307802_57186936` | **SUCCEEDED** | 41 / 41 / 41 | 90,5 s | **27,2** | 1 mappa: 3,3 / 4,6 / 11,0 s. 41 upload Drive reali: media 3,0 s. |
| v10 `job_1792698822460956156_e0003190` | **SUCCEEDED** | 36 / 36 / 36 | 85,0 s | **25,4** | 2 mappe: prefetch 3,7 s + prepare 4,7 s + encode 12,5 s medi. 36 upload Drive reali: media 2,7 s. |

**Baseline (3 run confrontabili, build strumentata, L3 idratato): mediana 27,2 overlay/min sulla finestra render, range 25,4–29,5, finestra mediana 85 s.** Il target 100/min non è raggiunto: manca un fattore ~3,7. I record completi sono in `ops/jobs/overlay_throughput_golden_100_20261022/results/` (comando di ripetizione nel README della stessa cartella).

### Baseline misurata e dove si perde tempo

Wall per fase (mediana delle 3 run; `audio_compile` e `overlay_render` sono sorelle concorrenti, non sommare al totale):

| Fase | Mediana | Range | Natura del costo |
| --- | ---: | ---: | --- |
| `generate` | 132,8 s | 129,0–136,6 | 20 chiamate Ollama (work cumulativo ~250 s, model load ~30 s, cold start) — **il collo più grosso end-to-end** |
| `audio_compile` | 89,6 s | 62,9–96,1 | AAC encode + render audio Rust (work cumulativo 83–167 s), in overlap con l'overlay |
| `overlay_render` | 97,0 s | 90,2–97,0 | **finestra dei ~37 item** → 25–29 item/min |
| `scene_analysis` | 66,9 s | 54,4–67,9 | 20 resolve Artlist (work cumulativo 180–281 s, esterno) |
| `voiceover` | 56,3 s | 55,6–57,0 | TTS 20 chiamate (work ~216 s) + publish Drive **seriali** (~5,5 s/item) |
| `prepare_join` | 28,5 s | 25,9–68,0 | preparazione media/join (varia con gli asset da materializzare) |
| `post_writer_finalize` | 16,3 s | 15,7–17,2 | hash + publish artefatti (drive_publish finalize ~43 s work su 4 chiamate) |
| `audio_publish` | 15,0 s | 14,1–15,3 | upload Drive audio finale |

Dentro la finestra render (la metrica del ticket):

- **Mappe**: ~20 s/item ciascuna (prefetch ~3,6 + prepare ~4,7 + encode ~11–12,5), ora interamente attribuite. Sono gli item più pesanti: con 1–3 mappe nel piano incidono per 20–60 s degli 85–97 s di finestra.
- **Upload Drive reali (outbox)**: media **2,4–3,0 s/item** (range osservato 2,0–6,8 s), somma 98–139 s per run — ma **asincroni** (delivery dopo la terminalità del parent, in parallelo col resto): non entrano nella finestra render; il loro costo è ora visibile e prima era l'unico pezzo invisibile. Il `drive_publish` del percorso sincrono (compositi `:final-composite:`) resta da misurare alla prima run final-job.
- **Item non-mappa** (phrase/number/image/entity): 2–5 s/item con pool a 4 worker (`wait_completion` work ~99 s su 41 item).

**Leve concrete verso 100/min nella finestra render** — *aggiornate dopo la misura
per-item v9, che corregge l'attribuzione: la finestra è il servizio render
seriale (`sum(render_ms)` ≈ finestra; catena post-render 1,5 s), quindi le leve
"pool width" e "anticipazione mappe" erano secondarie. Le mappe girano già su
una classe parallela alla larghezza 1:*

1. **Più lane di render** — LEVA VERA e già esercitata: 2° daemon Chronon +
   `gpu_lanes=2` + `overlayItemDeepWidth=2` ha portato 27,2 → 37,8–41,7 item/min
   (overlap 1,89×). La strada è replicare il pattern (N daemon = N lane) fino
   alla saturazione NVENC; oltre 2 istanze serve però una nuova certificazione
   VRAM (l'evidenza esistente copre 2 daemon).
2. **Ridurre il servizio render per-item** (~3,0 s/item, ~26 ms/frame): è lavoro
   sul frame pipeline/encode del renderer Chronon (Chronon3d), non su
   PipelineGen. È l'unica leva residua per arrivare a ~100/min con poche lane.
3. **Voiceover publish seriale** (~5,5 s/item × 20) e `drive_publish` finale:
   restano sul wall end-to-end, non sulla finestra — parallelizzarli come fatto
   per gli overlay se l'obiettivo diventa il wall del job intero.
4. **End-to-end**: `generate` (132,8 s) e `scene_analysis` (66,9 s) dominano il
   wall del job (~7–8 min) ma non la finestra overlay: se l'obiettivo è il
   throughput del job intero, sono loro il primo target (prompt cache per
   Ollama, parallelismo resolve Artlist).

Riferimenti record: `results/overlay-only-v6` … `results/overlay-only-v10`, `results/README.md` (ripetibilità), baseline JSON nei summary di ciascuna run.

### Ottimizzazione: seconda lane di render (2° daemon Chronon) — report prima/dopo (22/10, 20:30–21:00 UTC)

**Correzione dell'attribuzione (misura per-item v9, `render_attempt_analytics`):** la finestra render non è limitata dal pool né dalla catena pre/post, ma interamente dal **servizio render seriale**: 41 item, `sum(render_ms)=91,7 s` contro una finestra di 90,5 s; tutta la catena post-render (materialize+probe+hash+upload) somma **1,5 s**. Con `gpu_lanes=1` e un solo daemon (`kSerializedRenderJobSlots = 1`) il throughput era quindi per costruzione `1 / servizio medio per item` ≈ 25–29 item/min. Le leve "pool width" e "mappe fuori finestra" erano secondarie: le mappe girano già su una classe parallela (la loro fase pesante non entra nella finestra).

**Modifica applicata** (topologia certificata dal repo, evidenza VRAM `RenderingGen/multilingual_overlays_v1/evidence/vram_two_runtime_pool_2026-09-16.json` — due istanze warm, artefatti byte-identical, ~1,8 GiB per due render concorrenti):

1. **Secondo daemon Chronon**: `chronon3d-b.service` installato e attivo, socket dedicato `/run/chronon3d-b/chronon.sock`, stesso binario `linux-video-release` e stessa env del daemon A (`CHRONON_RENDER_JOB_SLOTS=1`) — una domain di esecuzione per daemon = 2 lane render.
2. **Worker RenderingGen**: `chronon.socket_paths = [chronon.sock, chronon3d-b/chronon.sock]`, `gpu_lanes: 1 → 2` (log di avvio: `lane/daemon parity: gpu_lanes=2 transport=ipc sockets=2 lanes_per_socket=1`).
3. **Pool item overlay**: `overlayItemDeepWidth 1 → 2` (`render_queue_items.go` + test): con due lane il clamp a 1 affamava la seconda lane; due child render in flight = una per lane, senza allargare la coda. Test: `go test ./internal/capabilities/scripts/...` (18 pacchetti ok).
4. **Contratto di ammissione**: `RENDERINGGEN_GPU_SLOTS=2` (`gpu-slots.conf`, allineato a `worker.gpu_lanes=2`).

**Report prima/dopo (stesso payload immutabile, stessa macchina, L3 idratato):**

| Configurazione | Run | Item | Finestra render | Item/min | Overlap render |
| --- | --- | ---: | ---: | ---: | ---: |
| PRIMA (1 lane, pool clamp 1) | v8 | 41 | 83,4 s | 29,5 | 1,0× |
| PRIMA | v9 | 41 | 90,5 s | 27,2 | 1,0× |
| PRIMA | v10 | 36 | 85,0 s | 25,4 | 1,0× |
| **DOPO (2 lane, pool 2)** | **v11** `job_1792701052952713436_239a27c8` | **38** | **60,4 s** | **37,8** | **1,89×** (`sum(render)=114,1 s`, peak 3 render concorrenti) |
| **DOPO (2 lane, pool 2)** | **v12** `job_1792701608735359540_610dea1d` | **38** | **54,6 s** | **41,7** | 2 lane confermate |

**+39…+53% di throughput** (mediana baseline 27,2 → 37,8 e 41,7 item/min sulle due run post-intervento), nessuna regressione: 38/38 item COMPLETED e 38 link Drive certificati per run, `retry_count=0`. Il scaling è coerente con l'evidenza NVENC (1 motore su RTX A4000: ~1,65–1,9× da due sessioni sovrapposte). Record: `results/overlay-only-v11/`, `results/overlay-only-v12/`.

**Proiezione al target 100/min:** con servizio render medio ~3,0 s/item (v11) e un motore NVENC condiviso, la strada è (a) più lane via altri daemon — 2 lane danno 1,89×, ma oltre 2 istanze non c'è evidenza VRAM certificata (serve `vramprobe` dedicato) — e/o (b) ridurre il servizio per-item (~26 ms/frame oggi, frame pipeline/encode del renderer Chronon: è lavoro in Chronon3d, non in PipelineGen). Con 4 lane efficaci o servizio ~1,5 s/item si raggiungono ~75–100 item/min su questo mix. Il piano materializzato resta comunque 36–41 item (non 100): il gate del ticket va misurato come capacità della finestra, e il gap pianificatore (100 richiesti vs ~38 materializzati) resta un tema separato già dichiarato.

### Profilazione per-item del servizio render — 23 ottobre 2026

Fonte: le 38 righe `render_attempt_analytics` della run v12 più i 38 sidecar
di timing (`chronon_timing_storage_key`, schema
`chronon3d.render-telemetry-summary.v1`) recuperati dall'objectstore. La
scomposizione media per item (ms):

| Voce (media su 38 item) | ms | Natura del costo |
| --- | ---: | --- |
| **`render_ms` worker (servizio seriale per lane)** | **2.748** | è la finestra del ticket |
| — job wall daemon (`chronon_job_job_wall_ms`) | 1.567 | |
| ——— `render_loop` | 956 | lavoro reale (87 frame medi ≈ 11 ms/frame) |
| ——— `prepare` | 311 | `font_preflight` 245 (max 944) + `pool_warmup` 65 |
| ——— `process_startup` | 186 | `encoder_open_nvenc` 104 + `plan_decode_validate` 65 |
| ——— `encode_close` | 155 | drain/finalize encoder |
| — receipt verification | 933 | probe 296 + **decode 634** (policy `normal`) |
| — residuo (IPC/marshal/materialize/hash/upload) | ~250 | |

**Scoperte:**

1. **Il receipt di verifica tiene occupata la lane.** `write_render_job_receipt`
   (probe `ffprobe` + `sha256` + full decode `ffmpeg -f null -`) è chiamata dentro
   `execute_render_job`, prima che la reservation del device venga rilasciata:
   933 ms medi di verifica pura bloccano la lane render. È il pezzo grosso di
   quella che prima era la "catena post-render 1,5 s".
2. **Verifica doppia sullo stesso file.** `verify_output_contract`
   (`pipe_export_finalize_encoder.cpp`) sonda e fa già l'hash dell'output appena
   muxato (ffprobe ~15 ms + sha256 ~6 ms); `build_render_receipt` ripete poi
   `ffprobe -show_streams -show_format` (~296 ms, dominato dallo spawn del
   processo) e ricalcola lo sha256 sullo stesso file. ~300 ms/item di lavoro
   identico.
3. **Policy di verifica deployata ≠ default documentato.**
   `/etc/renderinggen/renderinggen.yaml` usa `receipt_verify: normal` (full
   decode, 634 ms/item); il codice (`receipt_verify.go`) documenta `fast` come
   default di produzione e `certify` per CI/golden. La discrepanza va decisa e
   documentata: o si passa a `fast` (meno garanzie di decode) o il decode resta
   ma fuori dalla lane (fix 1).
4. **`font_preflight_ms` misura in realtà tutta `runtime::prepare_render`**
   (etichetta fuorviante): 245 ms medi, fino a 944 ms sugli item frase con molti
   text-run. Nel daemon caldo, con font/MTSDF/pool già persistenti, questo
   lavoro viene ripetuto per ogni item invece di essere riutilizzato.
5. **Trappola di telemetria:** `vulkan_device_ms` (~300 ms in ogni sidecar) è
   l'inizializzazione di processo riportata per job, non un costo per-item (lo
   startup realmente attribuito ai job è 186 ms). Non va "ottimizzato" come se
   lo fosse; va solo etichettato meglio.
6. **Upload immagini GPU ~300 MB/item medi** (8 full-upload, 242 MB di immagini
   per item): le immagini vengono caricate a risoluzione sorgente, non ridotte
   al canvas.

**Fix list (ordine per guadagno atteso, nessun cambio di correttezza):**

| # | Fix | Guadagno atteso | Dove |
| --- | --- | --- | --- |
| 1 | Rilasciare la reservation **prima** di `build_render_receipt` (o receipt async): il reply al worker resta dopo il receipt, `RequiresStructuredReply` rispettato | −933 ms/item dal servizio seriale | Chronon3d `daemon_service_ipc_render_job.cpp` + `render_job_execute.cpp` — **variante Go-only FATTA (23/10), vedi «Fix 1+3 in variante Go-only»**: il decode (−634 ms) è fuori dalla lane senza toccare Chronon; resta in-lane il probe+sha256 del receipt (~300 ms, solo fix 2 lo toglie) |
| 2 | Deduplicare la verifica: passare `MediaContract` + sha256 già calcolati da `verify_output_contract` a `build_render_receipt` | −~300 ms/item | Chronon3d `render_receipt.cpp` / `pipe_export_finalize_encoder.cpp` |
| 3 | Decisione policy decode gate (`fast` vs `normal` vs `certify`) documentata per golden/produzione | fino a −634 ms se `fast` | **DECISA (23/10)**: `normal` resta la policy di produzione con la stessa garanzia (un decode completo per artefatto), ma il decode è eseguito dal worker nel post pool — vedi «Fix 1+3 in variante Go-only» |
| 4 | Cache prepare/font tra item nel daemon caldo + pool warmup persistente | −~310 ms/item | Chronon3d `render_preparation` / `pipe_export_pipeline_setup.cpp` |
| 5 | Overhead fissi: `encoder_open_nvenc` 104, `encode_close` 155, `plan_decode_validate` 65 | −~150–230 ms/item | Chronon3d |
| 6 | Downscale immagini a canvas prima dell'upload GPU (300 MB/item → ~30 MB) | meno upload e pressione VMA | Chronon3d resource preparation |

**Proiezione:** servizio per-item 2.748 → ~1.820 ms (fix 1) → ~1.510 ms
(fix 4) → ~1.280 ms (fix 5): con le sole 2 lane già certificate **≈ 94
item/min**; con i ritocchi al render loop (956 ms/item, 11 ms/frame) si chiude
il gate 100/min **senza** superare i 2 daemon (evidenza VRAM certificata).
Ogni fix va misurata con le regole già descritte: stesso payload immutabile,
report prima/dopo, nessun conteggio di item non validati.

### Fix 1+3 in variante Go-only — 23 ottobre 2026 (RenderingGen & PipelineGen, no Chronon3d)

La fix 1 era iniziata lato Chronon3d (`daemon_service_ipc_render_job.cpp`,
receipt deffered dopo il rilascio della reservation). Scelta operativa del
23/10: **non procedere nel C++** e ottenere lo stesso effetto dai due servizi
Go, spostando il decode del receipt fuori dalla lane senza cambiare la
garanzia. Implementato e testato:

- **RenderingGen `internal/processor/receipt_verify.go`**: la policy resta
  l'unica autorità (`pipeline.receipt_verify`), ma è spezzata in due metà
  esplicite — enforcement (cosa il worker esige dall'artefatto) e request (il
  tier wire chiesto a Chronon, `chrononReceiptRequest()`). Sotto `normal` la
  request è `fast`: Chronon non fa il decode in-daemon e la lane non paga più
  i 634 ms/item di `ffmpeg -f null -`.
- **Gate decode post-lane** (`enforceDecodableOutput`, chiamato da
  `verifyArtifactReceipt` nel post pool di `FinalizeJob`): sotto
  `normal`/`certify` è garantito **esattamente un decode completo per
  artefatto** — quello di Chronon quando il receipt lo prova
  (`resolved_policy` normal/certify o check `decode: pass`), altrimenti quello
  del worker, prima che i byte siano pubblicati. Un decode che fallisce
  rifiuta l'artefatto come un check del receipt fallito. `fast`/`strict` non
  promettono decode e non ne pagano. `certify` continua a chiedere il tier
  `certify` (decode in-daemon come evidenza indipendente per CI/golden);
  `strict` ora chiede `fast` invece di leakare lo spelling non-wire.
- **Metriche**: `worker_decode_us`/`worker_decode_ms` +
  `worker_decode_verified` (chi ha decodificato l'output è rispondibile dal
  solo ledger, insieme a `chronon_receipt_verification_policy`).
- **RenderingGen `renderinggen/config.yaml`**: `receipt_verify: normal` resta
  la policy di produzione — stessa garanzia di prima (un decode completo per
  MP4 pubblicato), costo lane zero. La discrepanza deploy vs default
  documentato dalla fix 3 è chiusa: `normal` è la scelta, non un accidente.
- **PipelineGen `render_queue_items.go` / `config/scripts.go`**: pool per-item
  ritoccato 4 (cap 6), senza clamp di profondità — la larghezza di
  alimentazione serve a tenere le lane nutrite attraverso il tail post-lane
  (~0,9 s/item), non alza il carico GPU (`worker.gpu_lanes` resta l'unica
  autorità).

**Proiezione Go-only (da misurare con il golden):** servizio lane per item
2,75 s → **~2,05 s** (decode 634 ms fuori lane; restano in-lane render loop
956 + prepare 311 + startup 186 + encode_close 155 + probe/receipt ~300).
Con 2 lane ≈ **57–60 item/min** (da 37,8–41,7); con 3 lane ≈ **85–90 item/min**.
Il gate 100/min a 2 lane resta fuori portata Go-only: servono fix 2/4/5 lato
Chronon (→1,28 s/item ≈ 94/min) oppure la 3ª lane più il ritocco del render
loop.

**Contraddizione da sciogliere sulle lane:** `RenderingGen/renderinggen/config.yaml`
dichiara `gpu_lanes: 3` (commento «certified three-lane admission contract») e
il pool commenta «3 lane certificate», ma questo ticket registra l'evidenza
VRAM certificata per **2** daemon (oltre serve `vramprobe`). Prima di
pianificare la 3ª lane verificare quanti socket sono effettivamente montati
(`chronon.socket_paths`) e la certificazione VRAM del 3° daemon.

**Test:** `renderinggen` `go test ./...` verde (inclusi i nuovi
`TestChrononReceiptRequestDefersTheDecodeUnderNormal`,
`TestWorkerDecodeGateRunsWhenTheReceiptProvesNoDecode`,
`TestWorkerDecodeGateSkipsADecodeTheReceiptProves`,
`TestWorkerDecodeGateIsNotPromisedUnderFastOrStrict`,
`TestWorkerDecodeFailureRejectsTheArtifact`,
`TestWorkerDecodeGateDecodesARealArtifact` con ffmpeg reale); PipelineGen
`go test ./internal/capabilities/scripts ./internal/platform/config` verde col
pool ritoccato. **Resta da misurare:** rerun golden (3 run, mediana) per
registrare il prima/dopo secondo il contratto di misura — non eseguibile da
questo ambiente di sviluppo.

### Run v13–v14 e stato corrente della misura — 23 ottobre 2026

Le run v13 e v14 misurano rispettivamente la fix 1 (receipt di verifica fuori
dalla lane) e la prima configurazione a 3 lane. Finestre lette da
`job_steps.overlay_render` (lo stesso istante dei summary già registrati, il
metodo coincide su v15–v18):

| Run | Esito | Config | Item (piano/render/links) | Finestra render | Item/min | Note |
| --- | --- | --- | ---: | ---: | ---: | --- |
| v13 `job_1792749340415123677_53737c48` | **SUCCEEDED** | 2 lane + receipt off-lane (fix 1) | 40 / 40 / 40 | 69,2 s | **34,7** | `retry_count=0`. Prima run col decode del receipt fuori dalla lane. Mix: 20 frasi + 8 immagini + 9 entity card + 2 numeri + 1 mappa; `phrase_overlay_budget` 20/20, shortfall 0. |
| v14 `job_1792750754970977625_9a4b7cec` | **SUCCEEDED** | 3 lane (prima run trilane) | 39 / 39 / 39 | 59,4 s | **39,4** | `retry_count=0`. Mix: 19 frasi + 8 immagini + 8 entity card + 2 numeri + 2 mappe; `phrase_overlay_budget` 19/20, shortfall 1. |

**Stato corrente della misura (v15–v18, stessa giornata):**

| Run | Esito | Config | Item (piano/render/links) | Finestra render | Item/min | Note |
| --- | --- | --- | ---: | ---: | ---: | --- |
| v15 `job_1792753818929537987_188b9312` | SUCCEEDED | 2 lane, build `efc944f6`, pool 4 | 40 / 40 / 40 | 49,9 s | **48,1** | Migliore misura 2 lane: proiezione Go-only (57–60/min) quasi centrata. |
| v16 `job_1792755309238983955_f81ba9c8` | SUCCEEDED | 2 lane, build `efc944f6` | 39 / 39 / 15 | 105,2 s | 22,2 | Finestra degradata (lunga coda); links Drive parziali allo snapshot. |
| v17 `job_1792755903866828770_faee264e` | SUCCEEDED | 2 lane, build `efc944f6` | 40 / 40 / 12 | 51,1 s | **47,0** | Replicazione di v15; links Drive asincroni non tutti conclusi allo snapshot. |
| v18 `job_1792757495483683434_df8e374e` | SUCCEEDED | 3 lane (certificazione `vramprobe` 23/10) | 36 / 36 / 13 | 105,1 s | 20,6 | **Non confrontabile** con la baseline 2 lane; finestra degradata. |

Riga di stato dei due contatori del ticket:

- **Throughput finestra render:** 27,2/min (baseline 1 lane) → 37,8–41,7/min
  (v11/v12, 2 lane) → **34,7–39,4/min (v13/v14)** → **47,0–48,1/min (v15/v17,
  2 lane + pool 4)**. Le run v16/v18 (20,6–22,2/min) sono outlier da finestra
  degradata e non vanno usate come mediana. Gate 100/min: **non raggiunto**;
  la proiezione resta ~1,3 s/item di servizio seriale con fix 2/4/5 lato
  Chronon, oppure la 3ª lane certificata (v18 iniziale ma non confrontabile).
- **Item pianificati (il gap 100):** tutte le run v6–v18 materializzano
  **36–41 item**, non 100. Causa radice individuata 23/10: erano i clamp del
  pianificatore a tagliare il mix richiesto dal payload (che chiede esplicitamente
  `max_phrase_overlays=50`, `max_image_overlays=30`, `max_map_overlays=10`,
  `overlay_group_limits` 25+25 frasi / 30 immagini / 10 mappe):
  `MaxPhraseOverlaysHardLimit` clampano le 50 frasi a 20;
  `MaxMapOverlaysPerRun=3` e `MaxNumberOverlaysPerRun=5` tagliavano le lane data
  e mappe; `effectiveMapOverlayCeiling` ignorava le richieste esplicite;
  l'image limit richiesto non raggiungeva il primo pass di budget del planner.
  **Fix applicato nel codice** (PipelineGen, `internal/capabilities/{overlays,scripts}`):
  `MaxPhraseOverlaysHardLimit` 20→50, `MaxMapOverlaysPerRun` 3→10,
  `MaxNumberOverlaysPerRun` 5→10, threading dell'image limit richiesto nel
  planner, `effectiveMapOverlayCeiling` onora le richieste esplicite entro il
  cap di run. **Test:** il mix golden è pinnato a **100 item materializzati**
  (25 frasi brevi + 25 lunghe + 20 immagini + 10 entity card + 10 date +
  10 mappe) sia al budget (`TestGoldenThroughputMixBudgetAdmitsOneHundredItems`)
  sia end-to-end su `CompileOverlayPlan`
  (`TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items`), con la
  fornitura di candidati completa. **Non ancora misurato live**: la prossima
  run golden con questa build è quella che chiude la voce «overlay effettivi»
  del contratto di misura; il mix reale resterà comunque vincolato alla
  fornitura di candidati groundati dal contenuto.

### Roadmap operativa del job

### Audit end-to-end del tempo perso — golden v6

Il target non è soltanto accelerare `overlay_render`: per scendere da 7:02 a 5:00 bisogna togliere almeno **121,9 s (28,9%)** dal percorso completo v6 mantenendo gli stessi 37 output validi. La seguente è la lettura operativa della run; le durate di fase sono wall attribuiti e in parte sovrapposti, quindi non vanno sommate.

| Punto del percorso | Dato osservato v6 | Waste/costo da verificare o rimuovere |
| --- | --- | --- |
| Accodamento → worker | `queue_wait_ms=1000`; singolo tentativo | Basso in questa run. Tenere distinto dal tempo attivo negli altri job e misurare coda/lease separatamente. |
| Generazione script | `generate` 135,988 s; 20 chiamate Ollama; queue wait cumulativo 80,904 s, model load 31,832 s | È il maggior wall fase misurato. Verificare se le 20 scene vengono generate in sequenza o a batch e se attese di slot/modello si sommano prima di poter proseguire. Registrare start/end per chiamata e momento in cui il testo di ciascuna scena diventa disponibile. |
| Traduzione | presente nel percorso ma senza fase di peso significativo isolata in v6 | Non sappiamo se blocca il voiceover per tutto il testo. Aggiungere timestamp per singola scena e barriera di join: traduzione completa vs testo pronto per scena. |
| Analisi scene / Artlist | `scene_analysis` 91,278 s; 20 resolve; lavoro cumulativo 281,355 s | Work ~3,1× il wall indica parallelismo, ma il massimo resolve arriva a ~91 s. Identificare la coda critica e se ricerca/analisi per scena parte appena il relativo testo è pronto oppure attende l'intero script. |
| Preparazione media e join | `prepare_join` 54,153 s | La fase aggrega più operazioni; serve tracciare quali input aspettano tutti gli altri e se voiceover, media, scene analysis e overlays hanno una barriera globale evitabile. |
| Voiceover/TTS | fase `voiceover` 56,968 s, 20 TTS con 213,597 s cumulativi; `tts` separato 95,092 s nella proiezione | **Domanda critica da risolvere nel codice:** il TTS parte per scena appena il relativo testo è stabile o solo dopo la generazione/traduzione dell'intero script? Una partenza anticipata può sovrapporre TTS alle scene successive. Registrare per-scena `text_ready`, `tts_enqueue/start/end`, `audio_ready` e dipendenze reali. Il work cumulativo non è elapsed. |
| Compilazione audio | `audio_compile` 128,355 s; Rust render 115,626 s cumulativi, AAC encode 99,312 s cumulativi | Il job overlay-only paga comunque la composizione necessaria al timing. **Verificare se Rust audio viene invocato dopo che tutto il testo è pronto o già sui segmenti pronti**, e se una compilazione globale duplica lavoro durante resume. Separare attesa risorse, Rust, FFmpeg/AAC e join. Se il piano overlay può usare timing parziali o incrementali, valutare streaming; non cambiare semantica senza prova. |
| Piano overlay | piano effettivo 37 elementi/20 scene, pur con richiesta/cap 100 | Il benchmark non esercita 100 overlay: configurare un input che garantisca 100 item effettivamente pianificati oppure documentare quali regole di eleggibilità ne scartano 63. Timing piano non è isolato abbastanza: misurare pianificazione, validazione e serializzazione. |
| Overlay / mappe | 127,904 s; 37 item; 3 mappe `process_wall_s` sommano 109,76 s | Collo dominante dell'overlay: frame pipeline/encode mappa, plate preparation e sincronizzazione dei 3 item. Tile cache ha ~4,5k hit e zero download, quindi il costo non è rete tile. Rendere visibile il tempo mappe nella telemetria fase/item e sovrapporre mappe agli altri overlay se limiti CPU/memoria lo consentono. |
| Wait queue rendering | 131,611 s cumulativi su 37 wait | Work cumulativo supera la fase wall per concorrenza. Per item registrare tempo da submit a render-start e da render-end a probe; distinguere coda reale da worker poll/sleep. Il poll non deve aggiungere secondi se esiste completamento event-driven. |
| Probe, materializzazione, hash/ObjectStore | 37 successi; upload ObjectStore 1,575 s totale | Non dominante in v6; preservare verifica. Misurare separatamente download/read-back, hash e retry; gli errori precedenti 422 erano asset stantii, corretti evitando la selezione, non velocizzando i retry. |
| Drive/outbox e finalize | `post_writer_finalize` 16,865 s; `audio_publish` 15,495 s; 3 publish Drive nel writer | Upload asincrono overlay è fuori dal caller; `overlay_upload` misura enqueue (~68 ms), non consegna Drive. Misurare durata end-to-end dell'outbox, wait per parent terminale e backoff. Le deferral RUNNING usano 60 s ma non consumano tentativi; non sono nel wall v6 se il job finisce prima. Evitare polling/backoff sul critical path overlay-only. |
| Publish/chiusura | `publish` 117,298 s, con overlap; residuo non attribuito totale 939 ms | Non sommare la fase al wall: può essere un aggregato concorrente. Attribuire i 117 s alle sotto-operazioni e chiarire quali publish sono obbligatori con `final_job=false`; niente upload/artifact di final video deve essere richiesto dal golden. |
| Retry e resume | v6 `retry_count=0`, 37/37 riusciti | I run v2–v5 hanno perso minuti ripetendo rendering o arrivando a errori 422. La regressione è stata rimossa lato retry deterministici e catalogo; ogni resume deve provare che riusa artefatti validi e non ricostruisce mappe/audio già certificati. |

**La domanda pipeline richiesta dal team resta aperta finché non è provata con trace per scena:** voiceover/TTS deve partire non appena testo e traduzione di quella scena sono stabili; la compilazione Rust può partire solo quando ha i segmenti audio necessari al suo contratto, ma va verificato se il codice attende inutilmente il completamento di tutto il testo o di tutti i TTS. La run v6 espone durate aggregate e non consente di affermare quale barriera avvenga oggi. Il ticket non deve trasformare questa ipotesi in fatto: aggiungere eventi temporali per scena e parent/child, poi confermare la dipendenza dal codice.

Per ogni barriera annotare `ready_at`, `enqueued_at`, `started_at`, `completed_at`, predecessori richiesti e work effettivo. Calcolare critical path dalla traccia (non sommando le durate) e classificare ogni secondo come lavoro utile, coda, polling/sleep, serializzazione, upload o lavoro ripetuto. Il budget di miglioramento immediato per arrivare a 5 minuti è 121,9 s sul wall v6; i primi esperimenti vanno condotti sulle mappe (109,8 s osservati), generazione (136,0 s di fase) e attese/barriere TTS-audio, mantenendo confrontabile il numero di output.

1. Ricezione payload e idempotency key → validazione/configurazione (`final_job=false` per questo benchmark).
2. Enqueue e attesa worker → lease, checkpoint e correlazione parent/child.
3. Normalizzazione dell'input → segmenti, scene e clip iniziali dal payload.
4. Generazione script/testo → output scene e annotazioni.
5. Traduzione/fanout → testo localizzato e riallineamento scene.
6. Selezione e binding media → immagini/entity-card e riferimenti con identità content-addressed. È qui che il riferimento `b5…` deve essere escluso se non verificabile; il candidato alternativo non deve lasciare vivo il binding congelato precedente.
7. Voiceover/TTS → asset voce e timing parlato.
8. Compilazione audio canonico → FFmpeg prepara l'audio necessario per il timing/render overlay; non equivale al final video.
9. Congelamento piano overlay → timing, asset refs, mappe, fonts e contratti; checkpoint immutabile per il render.
10. Render dei singoli item → map tile/prefetch, plate preparation, encode (attualmente la parte misurata più costosa sulle mappe).
11. Materializzazione e validazione artefatti → workspace/ObjectStore, controllo hash/size/media contract. Mismatch è deterministico e deve fallire senza retry.
12. Outbox Drive → evento durevole, pubblicazione idempotente solo quando il risultato del parent consente il projection; telemetria separata dal render.
13. Chiusura job e report → conteggi richiesti/pianificati/materializzati/validati, timings di fase, retry e output hash.
14. Final video → **fuori ambito del golden overlay-only**, `final_job=true` non viene usato.

## Lavoro richiesto

- [~] Ricostruire i job recenti e storici con timestamp di inizio/fine, script e revisione, overlay richiesti/pianificati/materializzati/validati, esito e numero di retry. Prima ricostruzione dei dieci job e delle run golden completata; restano telemetria per tipo di item, worker e conteggio end-to-end attendibile.
- [x] Separare il wall time per fase: generazione del piano/contenuto, risoluzione immagini e asset, enqueue/wait, rendering, validazione e retry. Fatto: `run_stage_observations` per le fasi di job, `run_operation_observations` per le sotto-operazioni (ora incluse le fasi mappa `prefetch/prepare/encode`), `render_attempt_analytics` per il per-item. Scomposizione mediana delle 3 run baseline in «Baseline misurata e dove si perde tempo».
- [x] Identificare i colli di bottiglia principali con dati; distinguere tempo CPU/GPU attivo, attese esterne e lavoro ripetuto. Fatto: generate/Ollama (132,8 s) e scene_analysis/Artlist (66,9 s) dominano il wall end-to-end; nella finestra render dominano le mappe (~20 s/item) e il per-item su 4 lane; il lavoro ripetuto (retry su asset stantii) è stato rimosso con la bonifica+idratazione L3.
- [x] Creare un **payload golden da 100 overlay** ripetibile, derivato da un job reale rappresentativo e coperto da una composizione documentata di tipologie. Payload fissato e hashato.
- [x] Completare una run golden valida. Fatto: **quattro run SUCCEEDED** — v6 (37/37 overlay validati), v8 (41/41), v9 (41/41), v10 (36/36), tutte `retry_count=0` su tentativo pulito e con link Drive certificati. Entrambi gli asset corrotti (`b5b91ac5…`, `fda374e1…`) più altri 5 blob e 24 binding stale sono stati bonificati/idratati prima di v8 (sezione dedicata): 758/758 materializzazioni con oggetto L3 verificato.
- [x] Eseguire il golden case sul codice corrente e registrare tempo totale, overlay/minuto, tempi di fase, overlay effettivi, errori, retry, parallelismo e risorse. Ripetere almeno tre volte e riportare mediana e dispersione. Fatto: 3 run confrontabili (v8/v9/v10) → mediana **27,2 overlay/min** sulla finestra render, range 25,4–29,5, mediana finestra 85 s; record completi con tutti i dettagli richiesti.
- [x] Salvare input, output atteso, report e comando di ripetizione in una cartella versionata sotto `ops/jobs/`. Fatto: `ops/jobs/overlay_throughput_golden_100_20261022/` (payload immutabile + hash, README con comando di ripetizione) e `results/overlay-only-v6…v10/`.
- [~] Applicare la fix list della profilazione per-item (23/10): receipt fuori dalla reservation, dedup verifica (probe+sha256), decisione policy decode gate, cache prepare/font tra item, overhead fissi (nvenc open / encode close / plan decode-validate), downscale immagini prima dell'upload GPU. Dettaglio in «Profilazione per-item del servizio render — 23 ottobre 2026»; ogni fix va misurata con rerun golden e report prima/dopo. **Stato 23/10:** fix 1 (nella variante Go-only, decode fuori dalla lane) e fix 3 (decisione policy) **fatti** in RenderingGen/PipelineGen, vedi «Fix 1+3 in variante Go-only»; **fix 2 (dedup probe+sha256) fatto lato Chronon3d** (working tree, non committato): `verify_output_contract` pubblica la propria osservazione (probe + sha256) e `build_render_receipt` la riusa invece di riesaminare gli stessi byte — uno probe, un digest per artefatto (nuovi test `render receipt consumes precomputed output facts (fix 2 dedup)` e `render receipt runs its own probe when HDR mastering must match`, più il threading lato piano/RenderJob; suite `chronon3d_media_video_tests` + `chronon3d_cli_tests` verdi sulle aree toccate). **fix 4 (cache prepare/font nel daemon caldo + pool warmup persistente) fatto lato Chronon3d** (working tree, non committato): (a) `preflight_fonts` consulta la prepared-run cache (`find(layout)` sulla fingerprint immutabile del layout, cache di runtime che nel daemon caldo sopravvive tra RenderJob) prima di rigenerare l'MTSDF — un item ripetuto con lo stesso layout è servito dalla cache invece di ricostruire i batch (contatori `gpu_text_prepared_batch_reuse_count`/`_build_count`); (b) il warmup del `FramebufferPool` passa da `preallocate` (creava + riclear/re-touchava le superfici ad ogni item, ~65 ms/item) a `ensure_preallocated` top-up-only: a pool caldo l'item non alloca né tocca nulla; (c) `runtime::prepare_render` registra il memo `prepared_composition` (`SoftwareRenderer::mark_prepared_composition`), così `render_compiled` non esegue una seconda preparazione completa per item sul percorso still/frames del daemon. Pinnato dai nuovi test `tests/runtime/test_render_preparation_warm_cache.cpp` (riuso prepared-run tra item + warmup top-up). Guadagno atteso ~310 ms/item (font_preflight ~245 + pool_warmup ~65) **da verificare con rerun golden**: confrontare `prepare_breakdown.font_preflight_ms` e i contatori warmup dei sidecar prima/dopo. Restano lato Chronon3d fix 5 (overhead fissi), fix 6 (downscale immagini) e la misura golden prima/dopo.
- [x] Selezionare almeno un job storico rappresentativo, conservandone payload e riferimento ai risultati già disponibili. Fatto: il golden deriva da `08_fonti_media.generate.v11.json` (batch Garlasco `ops/jobs/garlasco_batch_10_20261019_sequential_v7/`, manifest e payload conservati); differenze dichiarate nel golden case v2 (stessi 20 segmenti e clip intro, progetto/correlazione dedicati, `final_job=false`).
- [x] Dopo ogni modifica, ripetere il golden case sulla stessa macchina/configurazione e confrontare throughput, tempi di fase e qualità con il baseline. Regola permanente, applicata a tutte le modifiche del 22/10: strumentazione/idratazione → v8/v9/v10; ottimizzazione lane → v11/v12. Ogni confronto tiene conto del numero di overlay validati (38/38 COMPLETED + 38 link in v11/v12, nessun miglioramento da item mancanti).
- [x] Applicare ottimizzazioni solo ai colli di bottiglia confermati, preservando l'ordine, il contenuto, il layout e il rendering richiesti. Applicato alla sola leva confermata dal dato per-item (servizio render seriale): la topologia dual-daemon è certificata artefatti-byte-identici, il pool non renumera gli item (test `TestSeparateOverlayItemsPreservePlanOrderAndIndices`), nessun cambio di contenuto/layout.
- [x] Impedire il retry automatico dei fallimenti non tipizzati, che ripetevano input e checkpoint invariati; preservare il yield previsto per il rendering remoto ancora attivo.
- [x] Togliere la pubblicazione Drive seriale dal critical path dei render final-job, mantenendo il join prima del final render e aggiungendo la misura `overlay_upload`.
- [x] Recuperare il file audio locale scomparso ripartendo da `audio_compile` al retry, conservando i render overlay già certificati.
- [x] Verificare che la nuova build non ritenti automaticamente un content-address mismatch: il job `job_1792691863832924962_59e9fb19` è terminato `FAILED` con `retry_count=0` ed errore diagnostico, senza replay (contratto `retry.IsTransient`).
- [x] Verificare con `overlay_upload` l'attribuzione del residuo di `overlay_render`: la pubblicazione Drive seriale **non** è la causa dominante (vedi paragrafo «Collo di bottiglia overlay individuato»); il residuo è concentrato negli item pesanti non strumentati.
- [x] Misurare il costo reale dell'upload Drive nel worker outbox. Fatto: `MeasureOperation` (`drive_publish`, component `drive`) nell'handler `overlay_drive_outbox.go`, una delivery run per evento joinabile al job. Misurato sulle run v7–v10: **2,4–3,0 s/item medi** (range 2,0–6,8 s), 36–57 upload per run, somma 98–139 s — asincroni, fuori dalla finestra render. Resta da misurare solo il `drive_publish` sincrono dei compositi `:final-composite:` alla prima run final-job.

## Contratto di misura

```text
throughput = overlay validati / minuti di wall time della finestra misurata
```

Pubblicare anche il tempo dall'invio del job al completamento, ma riportare separatamente le attese in coda. Definire una volta nel report se il throughput si riferisce alla sola lavorazione attiva o al tempo end-to-end; non confrontare finestre diverse come se fossero equivalenti. Un overlay conta come valido solo quando è materializzato, renderizzato e supera i controlli di output previsti.

Per ogni run salvare almeno: `run_id`, input hash, revisione, configurazione e worker, orari UTC, richiesti/pianificati/materializzati/validati, durata per fase, wait di coda, retry/failure e output hash. Nessun dato mancante deve essere interpolato come misura.

## Criteri di accettazione

- [~] Payload golden immutabile da 100 overlay, ripetibile e verificabile; hash fissato. La prima run live non ha completato e non prova il throughput.
- [x] Baseline iniziale del golden case registrata con almeno tre run confrontabili e conteggio degli overlay validi. **Mediana 27,2 overlay/min** (v8 29,5, v9 27,2, v10 25,4), 36–41 overlay validati per run, tutti con link Drive certificati.
- [x] Almeno un job storico valutato: confronto documentato con fixture equivalente (golden derivato da `08_fonti_media` con differenze dichiarate; ricostruzione misurata della batch storica in «Evidenza storica»).
- [x] Ogni fase costosa e le cause di retry/mancata materializzazione risultano attribuibili o segnalate come non osservabili. Scomposizione in «Baseline misurata e dove si perde tempo»; residuo dichiarato: `drive_publish` sincrono dei compositi final-job e micro-fasi non ancora proiettate.
- [ ] Gate di throughput concordato e misurato: obiettivo **100 overlay/minuto** sul golden case. Misurato: **27,2/min** (mediana 3 run a 1 lane) → **37,8–41,7/min** (v11/v12, 2 lane) → **47,0–48,1/min** (v15/v17, 2 lane + pool 4, 23/10) — **gate NON raggiunto**, manca un fattore ~2,1. Le run v13/v14 (34,7–39,4/min) misurano la fix 1 e la prima configurazione 3 lane; v16/v18 sono outlier da finestra degradata. La strada preferita resta il taglio del servizio seriale per-item (2,75 s → ~1,3 s con le fix 1-2-4-5) ≈ **94 item/min con le sole 2 lane certificate**; vedi «Profilazione per-item del servizio render — 23 ottobre 2026». Vincolo esterno: l'evidenza VRAM certificata copre 2 daemon, con certificazione 3 lane (`vramprobe`) solo dal 23/10 (v18 non confrontabile). **Gate ridefinito 23/10 (metrica per classe):** vedi «Metrica per classe e ridefinizione del gate» sotto: il numero `item/min` da solo non è più un accettabile report di gate, perché dipende dal mix di classi del piano materializzato.

### Implementazione Go del batching — 24/10

PipelineGen ora può pianificare finestre compatibili per scena e inviare una
composizione multi-item alla coda RenderingGen esistente. Il risultato mantiene
`item_id` per ogni overlay e aggiunge un riferimento batch con artefatto,
finestra, scena e membri. La cache content-addressed include ordine e identità
di tutti gli item; `goldensummary` conta i membri semantici e riporta a parte il
costo osservato del render batch. Mappe e gruppi chain restano fuori dal fused
path. L'handoff finale ricostruisce e valida identità, membership e finestra del
batch, ma rifiuta di inviarlo in `mode=replace` senza un binding per-finestra
al video base canonico. Un batch già consumato interamente come clip di una
scena phrase-only resta ammesso; non genera una seconda sostituzione.

La chiave dell'identità batch include anche il render key di ciascun membro:
un edit che riusa `PlanID` e `item_id` produce un nuovo `BatchID` e non può
collidere con il riferimento editoriale precedente. Quando il cache store e il
prober sono configurati, ogni batch certificato viene registrato prima della
pubblicazione; al resume il planner deterministico ricrea gli stessi gruppi,
riusa i batch ancora validi e sottopone a rendering quelli senza artefatto
certificato. Questo fornisce retry per batch sul percorso della cache esistente,
ma non sostituisce un checkpoint parziale del risultato mentre il parent è
ancora in esecuzione; inoltre un errore di storage/cache degrada al render
normale e può quindi ripetere lavoro già completato.
La prova `TestOverlayRenderCacheRetrySubmitsOnlyTheBatchThatFailed` copre il
caso multi-batch: dopo aver certificato il primo batch e iniettato un errore di
submit sul secondo, il resume riusa il primo artefatto e sottopone soltanto il
batch senza artefatto certificato. Il comportamento dipende dalla disponibilità
dell'indice cache e dal probe di presenza object-store già cablati in wiring.
La stessa verifica `-race` ha trovato che i child phrase modificavano
`MotionParams` condivisi con il piano padre durante la preparazione concorrente
di batch differenti; `separateOverlayItemPlan` ora clona la mappa prima di
impostare `enter_frames`. I due test di retry e handoff batch passano con
`go test -race`.

Il contratto analytics ora persiste anche `cache_lookup_us`, `cache_probe_us`,
`cache_store_us` e `cache_outcome` per ogni tentativo, inclusi i batch. La
migrazione SQLite 280 aggiunge queste colonne; i microsecondi evitano che un
lookup locale rapido appaia come zero millisecondi. `goldensummary` aggrega
lookup/probe/store (count, media, p50, min, max) e gli esiti senza incrementare
il conteggio semantico degli overlay per i tentativi batch. Il cache hit resta
separato dal render worker: i timing del render originale continuano a essere
sanitizzati e non vengono attribuiti al tentativo che riusa l'artefatto.

Nei piani `RequireDriveBeforeReturn` il grouping resta disattivato anche quando
è presente un background video in loop: il campo `Background` non prova che
quell'asset sia il clip canonico selezionato per la finestra. La selezione
canonica dei clip oggi appartiene al payload finale e non viene anticipata nel
piano overlay; inoltre `Source` non porta un source-time offset, quindi
riusarlo in batch riavvierebbe il clip dal frame zero. Attivare il fused path
senza il binding corretto potrebbe sostituire il footage con il canvas o con il
tratto sbagliato. Il supporto di handoff batch è fail-closed anche al confine
finale: una referenza ripristinata o costruita manualmente non può aggirare il
blocco e sostituire il footage con un canvas non legato alla sorgente. Il
percorso di produzione final-job richiede ancora il binding per finestra a
monte. Il gate
verifica ora anche che ogni membro del batch abbia una sola referenza semantica
che punta allo stesso job/batch, senza un artefatto MP4 individuale concorrente;
batch con riferimenti mancanti, duplicati o incoerenti vengono rifiutati prima
di costruire il payload `mode=replace`. Prima di abilitare la fusione
final-job, il piano condiviso deve congelare per finestra almeno `scene_id`,
asset/base SHA-256 e source-in/source-out; renderer e Master devono consumare
quella stessa selezione e lo stesso offset. L'artefatto da sostituire deve
essere opaco e già composto con quel footage, oppure il Master deve accettare
esplicitamente il base binding e comporre la finestra. Finché il contratto non
è implementato e verificato, PipelineGen respinge i batch non consumati come
clip phrase-only invece di inviarli in `mode=replace`.

Le mappe restano singleton anche nel percorso ordinary: la generazione del
video dinamico avviene prima della composizione RenderingGen. È stata aggiunta
una cache filesystem content-addressed opzionale (`VELOX_MAP_VIDEO_CACHE_DIR`)
per riusare proprio quel MP4 tra retry/rerun: chiave sul payload risolto,
SHA-256 dello script, profilo encoder, dimensioni di output ed epoch (`VELOX_MAP_VIDEO_CACHE_EPOCH`);
ogni hit verifica hash e dimensione e attraversa ancora i controlli del
contratto telemetrico prima dell'handoff. Il cache root deve essere persistente
per sopravvivere ai process restart; il pruning rimuove prima i video più
vecchi e mantiene il totale entro 10 GiB per default
(`VELOX_MAP_VIDEO_CACHE_MAX_BYTES`); l'epoch va cambiato quando cambiano
dipendenze Python, ffmpeg o driver che non sono riflessi dallo script e dal
profilo. La cache è best-effort e non ancora certificata su map golden;
dimensionamento del limite e persistenza del volume restano prerequisiti
operativi prima di abilitarla in produzione. Il riuso delle tile/plate resta
distinto dal riuso dell'artefatto video.

Canary GPU cartografici 24/10, scena editoriale offline `ed_saudi_base`, RTX
A4000, OpenCV OpenCL + NVENC, 72 frame H.264 decodificabili, zero download di
rete e zero fallback. Prima baseline a 1280×720: 6,25 s produzione frame
(11,5 fps), 6,48 s render+encode, 12,76 s end-to-end; plate prepare 3,18 s.
Modifiche locali: coda OpenCL 16 frame, memoryview senza seconda copia BGR,
blur delle ombre ristretto al ROI (pixel-identico al full-frame) e upload dei
testi/ombre soltanto nel loro ROI. La versione ottimizzata produce gli stessi
72 frame pixel-per-pixel e scende a 3,43 s (21,0 fps), 3,63 s render+encode,
6,09 s end-to-end. Test: `test_geo_documentary`, `test_geo_places` e
`test_render_dynamic_map_payload`, 39 PASS. Profilo baseline: 55 blur Pillow
full-frame costavano 2,60 s; dopo il ROI questo costo non domina più. Una
prova 1080p/24 con cache tile calda completa 72 frame in 5,17 s (13,9 fps),
render+encode 5,42 s, end-to-end 8,50 s; plate 0,98 s e 1.334 hit disco.
Alzare il batch OpenCL da 16 a 32 è risultato più lento (13,6 fps) ed è stato
scartato. Queste prove non sono confrontabili al golden 3 fps: risoluzione,
scena, durata e carico differiscono. Il percorso resta Python/OpenCL con
readback host; per superare il limite per-frame attuale serve il porting del
sampler/compositor nel render graph C++ di Chronon3d.

Rerun 720p del 24/10 dopo il ripristino del percorso ROI: 72 frame in 3,90 s
(18,5 fps), render+encode 4,09 s, wall del renderer 6,72 s. La variabilità
rispetto ai 3,43 s precedenti è visibile anche con lo stesso input e tile
ricostruite localmente; non la tratto come regressione o guadagno senza più
ripetizioni controllate. È stato provato anche l'accumulo CPU dei mask per
ridurre i composite OpenCL: è sceso a 13,2 fps e la modifica è stata rimossa.
Il RenderPlan Chronon esistente ammette layer immagine con keyframe di
posizione, scala e opacità; la direzione implementativa è campionare plate
persistenti come layer Vulkan animati dalla camera, poi portare pin, forme e
testi nello stesso graph. Non è ancora collegato al render job PipelineGen,
quindi il port nativo non è completato.

Verifica cross-repository del 24/10: in PipelineGen passano i pacchetti
`internal/capabilities/scripts/...`, `internal/platform/renderinggen`,
`internal/app/wiring`, `internal/capabilities/assets/providers/artlist`,
`internal/capabilities/scripts/adapters/processor`,
`internal/capabilities/voiceover/service`, `internal/kernel/observability` e
`internal/platform/audio`. In RenderingGen passano
`renderinggen/internal/processor`, `renderinggen/internal/metricnames` e
`renderinggen/internal/schemaval`; il test Postgres
`TestArtifactPersistedOnComplete` passa. Il runtime Go è stato riallineato ai
cataloghi condivisi: le cinque motion `phrase_highlight_*` usano le ricette
documentarie canoniche; `classic_phrase` risolve il font Playfair regular
incluso nel bundle; i cinque motion Apple V2 sono registrati (108 phrase, 47
Apple V2). I gate di parità restano attivi. Verifiche mirate:
`go test ./internal/motion ./internal/overlay ./internal/processor` PASS. Su
RTX A4000, `TestClassicPhrasePresetExecutesOnStrictGPU` e il render GPU di
`phrase_highlight_evidence_red` sono PASS con Vulkan/NVENC, 48 frame
GPU-native, zero readback CPU e zero fallback software. La suite completa
`go test ./...` è stata interrotta dopo oltre quattro minuti senza output
aggiuntivo, mentre la workstation eseguiva altre suite e processi FFmpeg; non
le attribuisco un esito. Queste verifiche non certificano throughput live a
100/min, parità pixel/audio su golden né l'handoff Master con base-video.

La stima del planner usa un fit sui dati v15 in `render_attempt_analytics`
(parent run `run_da494991-1b8d-47c6-aaac-dd9ec0473a4a`): 39 item non-map,
servizio `render_ms` contro `frame_count`, fit lineare setup **1.036 s** +
**15,369 ms/frame** (R² 0,317; RMSE 558 ms). Il fit è una stima debole tra
classi: il planner richiede almeno il 10% di risparmio modellato e limita
item/window, ma serve ancora una run golden fused per misurare l'errore sul
costo dei batch. Le finestre per item usano la normalizzazione di durata
condivisa dal percorso single-item.
Il flag `VELOX_SCRIPTS_OVERLAY_WINDOW_BATCHING` consente rollback. Non si deve
attribuire a questa implementazione un guadagno di throughput prima di una run
golden confrontabile. RenderingGen già supporta piani con più item nei suoi
stadi `PrepareJob → RunGPU → FinalizeJob` e ora registra il numero degli item
semantici e se il job contiene una composizione multi-item; non è stato
introdotto un wire job parallelo. La certificazione richiede ancora di verificare sul risultato
renderizzato che ogni item del piano sia visibile, insieme a audio, timestamp,
codec, frame count, SHA-256 e comportamento del worker finale. Per gli asset
condivisi, il worker già compila e materializza il piano batch in un solo
`PrepareJob`: `mergeAssets` deduplica i riferimenti compilati per hash/percorso,
il workspace usa il resolver content-addressed e `prefetchWarmAssets` seleziona
gli asset del job con deduplica path e limiti globali (3 immagini + 6 video).
Questo riusa il prefetch esistente per il batch senza aggiungere una seconda
pipeline; i limiti restano quelli del worker attuale.
Il conteggio semantico viene emesso dallo stesso passaggio di compilazione che
abbassa gli item; non si rilegge il JSON del piano solo per telemetria. Gli
import mappa `pipelinegen-map-v2` trasportano inoltre l'`item_id` nel contratto
e nella provenienza persistita da RenderingGen; il worker mantiene il drain dei
job `pipelinegen-map-v1` già in coda. La proiezione dell'artefatto in
PipelineGen conserva la provenienza come JSON raw, senza ridefinirne lo schema.
L'identità e la finestra del batch sono persistite anche in
`render_attempt_analytics` da migration 279 (`batch_id`, membri JSON,
`batch_start_us`, `batch_end_us`); `goldensummary` le usa come fallback se il
risultato del parent non contiene il riferimento batch.
Il gate dei
100/min resta aperto e non misurato.

### Tagli runtime e pipeline mappe — 24/10

Sono stati chiusi anche i tagli Go richiesti per il percorso locale e audio:

- Il rendering video resta locale se non è configurato un publisher Drive; un
  piano che dichiara `RequireDriveBeforeReturn` continua a fallire chiuso se
  il publisher non è disponibile. Il `DriveFolderID` resta escluso dal
  fingerprint del render e dal wire verso RenderingGen, ma resta nel contesto
  applicativo del publisher.
- `PersistGeneratedArtifacts` verifica il digest certificato del master,
  preferisce hard link e usa copia streaming quando necessario; il manifest
  riutilizza quel digest, senza seconda lettura SHA-256 dell'output.
- `publishFinalAudio` è l'unico proprietario della pubblicazione canonica del
  master. Il manifest mantiene `audio_asset_id` e `drive_file_id` come
  riferimento; il worker salta il re-upload solo se l'ID manifest coincide
  con `audio_asset_id`, il `drive_file_id` è presente, SHA-256 è valido e la
  dimensione è positiva. Riferimenti incompleti restano sullo staging/upload
  generico. `publishFinalAudio` riusa un checkpoint solo quando conserva ID
  canonico, Drive link e Drive file ID; altrimenti ripubblica idempotentemente
  e ripristina la referenza completa.
- Il master viene pubblicato appena l'audio è certificato, in parallelo alla
  finestra di rendering; `audio_finalize` resta dopo il join perché proietta
  il timeline con gli artefatti overlay definitivi.
- L'Artlist query non viene costruita quando il provider non ha un consumer
  disponibile. `script.json` resta il solo artefatto di delivery della
  generazione: il final job e il builder Google Doc consumano `script_json`.
  `scenes.json` era una copia di `script.json` → `output.scenes` e non aveva
  lettori Go, Python o RenderingGen; la persistenza e il relativo upload sono
  stati rimossi. Il test del manifest ora pinna un solo `script_json` anche
  quando il risultato include scene e binding voiceover.

La verifica del single-owner audio copre anche manifest corrotti o parziali:
`ReferencesPublishedAudio` richiede ID canonico uguale all'ID manifest, SHA-256
esadecimale valido, byte count positivo e Drive file ID. Il test copre il
resume in cui `final_audio.m4a` è già sorgente e destinazione, per evitare che
il percorso hardlink rimuova il master certificato.

Il percorso GPU delle mappe editoriali in ChrononTemplate ora accoda sedici UMat
prima del readback ordinato, sovrapponendo il setup host delle pose al lavoro
OpenCL. Il frame finito passa ancora dalla memoria host a FFmpeg: questo taglio
riduce le sincronizzazioni, ma non è il passaggio zero-copy Vulkan/CUDA. La
camera fly-to GPU di Chronon/RenderingGen resta il percorso nativo per mappe
con plate e LOD certificati; le mappe dinamiche raster-deferred continuano a
usare il renderer dinamico finché sampling, composizioni editoriali e timing
per pin non hanno un contratto nativo equivalente. Nessun guadagno di FPS o di
throughput è dichiarato senza una misura sul binario e sul carico distribuiti.

Verifica GPU locale del 24/10: `geo_runtime.py --place Roma --shot-preset dive
--strict-native` ha renderizzato 240 frame 1920×1080 H.264 a 30 fps in 13,10 s
totali (18,32 fps end-to-end; 18,69 fps di produzione), con OpenCL NVIDIA,
NVENC, zero frame di fallback e MP4 decodificabile. Prefetch: 5.054 tile in
6,46 s, poi 866 tile mancanti in 1,79 s; campione visivo frame 120 corretto.
È una verifica funzionale con cache fredda e poi calda, non un confronto prima/
dopo né una certificazione del binario di produzione; quindi non chiude il gate
100/min. La prova ha trovato e corretto il contratto mancante `wayback_release`
tra wrapper e `DynamicTilePyramid` (release esplicita e namespace cache isolato).
Il compositing route-glow ora riusa il buffer thread-local e limita clear/blend
alla regione precedente/attuale; i 13 test documentali cartografici passano.
L'integrazione del sampling dinamico nel graph nativo Chronon resta aperta: il
render corrente attraversa ancora la memoria host per consegnare i frame a
FFmpeg, mentre il percorso native FullGraph zero-copy non è ancora cablato a
questo sampler editoriale.
- [x] Report prima/dopo per ogni ottimizzazione, con condizioni di esecuzione comparabili e nessuna regressione nascosta da overlay falliti o non contati. Fatto per la prima ottimizzazione (2° daemon + pool 2): v8/v9/v10 (prima) vs v11 (dopo), 38/38 COMPLETED e 38 link Drive, vedi «Ottimizzazione: seconda lane di render». La baseline «prima» è registrata (v8/v9/v10); il report «dopo» si compila alla prima ottimizzazione applicata.

## Nota sul target

100 overlay/minuto è il target richiesto, non una prestazione già dimostrata né una garanzia per job con dipendenze esterne o composizioni differenti. Il ticket deve riportare sia il risultato del golden case sia il throughput dei job reali per tipologia, così da rendere espliciti i limiti di confronto.

## Metrica per classe e ridefinizione del gate — 23/10 (sera)

Problema misurato: `item/min` è una funzione del **mix di classi** del piano
materializzato, non solo della velocità del renderer. Le mappe costano
17–66 s/item contro 2–5 s di frasi/immagini (run v6: 3 mappe = 110 s dei
127 s della finestra); confrontare run con mix diversi sul solo `item/min`
confronta il pianificatore, non il rendering. Inoltre il golden materializza
36–41 item, non 100: il gate era misurato su un piano che non produce gli
item attesi.

Metrica canonica introdotta (sostituisce gli summary ad-hoc a heredoc):

- Tool: `go run ./cmd/goldensummary --job-full <job.full.json> --db
  data/media/media.db.sqlite` (repo PipelineGen), schema
  `pipelinegen.golden-overlay-summary.v1`. Codice puro testato in
  `internal/platform/sqlite/rendermetrics/golden_summary.go`. La scrittura di
  `--out` passa da `pkg/atomicwrite.WriteFile` (gate enforcement
  `TestNoTruncatingWritesUnderCmd`: nessun `os.WriteFile` diretto in `cmd/`,
  un write interrotto non deve mai troncare l'artefatto generato).
- Definizione del servizio per item: `render_ms` di
  `render_attempt_analytics` (durata render Chronon misurata dal worker), lo
  stesso campo che le summary storiche chiamavano
  `per_item_render_service_ms`. Popolazioni senza misure restano vuote, mai
  stimate a 0.
- Blocchi per classe: per ogni `kind` del piano semantico
  (`text_phrase/entity_image/image/number/map/...`) conteggio item, servizio
  avg/p50/min/max e **quota di servizio** (`service_share_pct`), più la
  capacità della finestra (`overlay_window.items_per_min`) sempre riportata
  accanto al mix (`plan_kinds`).

Ridefinizione del gate (sostituisce la lettura del solo `item/min`):

1. **Metrica primaria: secondi di servizio per classe su piano fisso.** Due
   run sono confrontabili solo a parità di piano (stesso payload immutabile) e
   il report deve sempre portare il blocco `per_class` accanto alla finestra.
2. **Metrica derivata: capacità di finestra (item/min).** Si riporta solo con
   il mix di classi accanto; un `item/min` senza `plan_kinds` non è evidenza.
3. **Condizione del gate 100/min su 2 lane certificate:** piano golden a mix
   dichiarato (riferimento v17: 20 `text_phrase`, 8 `entity_image`, 7 `image`,
   4 `number`, 1 `map`), finestra ≥ 100 item/min ⇒ servizio medio per item ≤
   1,2 s (60 s × 2 lane / 100 item). Stato a v17: servizio medio 2,509 s
   (p50 2,309, max 5,362) ⇒ manca un fattore ~2,1, coerente con la proiezione
   «servizio ~1,3 s/item ≈ 94 item/min» della profilazione per-item.
4. **Gap pianificatore separato e dichiarato:** il piano materializzato porta
   36–41 item contro il mix richiesto da 100; il gate sul piano non è il gate
   sul pianificatore, e la voce resta aperta finché il planner non
   materializza il mix pieno.

## Zone confuse e da bonificare — censimento 23/10 (sera)

1. **Summary golden ad-hoc via heredoc python** (i comandi vivono solo in
   `debuggingchronon.md`): schema non riproducibile, campi ridefiniti a mano
   ogni run. **SOSTITUITI** da `cmd/goldensummary` (schema
   `pipelinegen.golden-overlay-summary.v1`); le summary storiche restano
   evidenza ma non sono rigenerabili. Bonifica residua: rigenerare le summary
   v13–v18 con lo schema v1 quando serviranno confronti.
2. **Schema `render_attempt_analytics` triplicato con drift**: vivo in
   `data/media/media.db.sqlite` (schema completo), copia vecchia in
   `data/cache/cache.db.sqlite` (manca `item_id` e le fasi), 0 righe in
   `data/observability/api_requests.db.sqlite`. Bonifica: un solo owner
   dichiarato, le copie degradate vanno nominate o eliminate.
3. **Debris di DB a 0 byte / 0 tabelle**: `data/jobs.db`,
   `data/jobs.db.sqlite`, `data/jobs/jobs.sqlite`, `data/pipelinegen.db`,
   `data/pipelinegen.db.sqlite`. Ogni analisi perde tempo a cercare «dov'è il
   DB». Bonifica: rimuoverli o registrarli in un README dati.
4. **Join analytics↔item via `job_id LIKE '%<run_id>%'`**: gli id item
   (`<run>:item:<nnn>:<id>`) incorporano il run id; è fragile per costruzione
   e impedisce query per run pulite. Bonifica: colonna `run_id` esplicita
   nella tabella.
5. **Stato fix duplicato su due ticket** (fix table qui + disposition nel
   ticket Chronon3d `TICKET-PERFORMANCE-AUDIT`): allineati oggi, ma resta una
   doppia authority. Bonifica: lo stato fix vive in un solo ticket.
6. **Complessità tagliata in questa sessione**: feature vcpkg fantasma
   `native-ffmpeg` (5 preset, warning a ogni configure; non esiste in
   `vcpkg.json`) rimossa; doppia rappresentazione della finestra nella
   summary (`window_from/window_to` + `overlay_window`) eliminata prima di
   rilasciare lo schema v1.

Verifica della metrica: il tool riproduce esattamente i numeri della summary
storica v17 (`avg 2508,8`, `p50 2310`, 40 item, finestra 51,1 s, 47,0/min) e
aggiunge il blocco per classe (v17: `image` 3,484 s avg = 24,3% del servizio su
7 item; `text_phrase` 2,3 s avg su 20 item; `map` 2,781 s su 1 item con tile
cache calda).

---

## 23/10 (sessione pomeridiana) — Stato fix list lato Chronon3d + idee di paradigma

Sezione aggiunta come registro di una sessione di lavoro sul repo Chronon3d
(working tree, non committato — stessa convenzione della fix 2). Le voci
«Idee di paradigma» e «Sprechi/processi eliminabili» erano **proposte non
approvate** al momento della registrazione; **le idee di paradigma sono state
approvate dall'utente il 24/10/2026** (ticket aperti, vedi la sezione sopra),
mentre la checklist «Sprechi/processi eliminabili» resta materiale di
proposta.

### Stato fix list (repo Chronon3d, working tree)

| Fix | Stato | Dettaglio |
| --- | --- | --- |
| 4 cache prepare/font tra item | **FATTO (parte raggiungibile)** | (a) Eliminata la seconda `runtime::prepare_render` per job: `warmup_pipe_renderer` richiamava l'intera preparation già eseguita dal setup sullo stesso (renderer, compiled) — preflight + resolve + decode + publish ripetuti per item — solo per ottenere `warmup_renderer=true`; ora chiama direttamente `runtime::warmup_renderer` (pool prealloc + touch + dummy frame), e il fail-fast della preparation passa a un gate esplicito (`PipeExportSession::preparation_ok` + diagnostic, consumato in `video_export_native`). La doppia accumulazione dei timings della preparation nel sidecar (prepare_timings contava due volte gli stessi eventi) è eliminata di conseguenza. (b) Attribuzione (finding #3/#6): `prepare_breakdown` espone ora `prepare_render_ms` (nome onesto del vecchio `font_preflight_ms`, che resta come alias legacy) più le sotto-fasi misurate `prepare_asset_preflight_ms`, `prepare_asset_resolve_ms`, `prepare_asset_decode_ms`, `prepare_image_decode_ms`, `prepare_image_upload_ms`, `prepare_font_load_ms`, `prepare_font_resolve_ms` (null = non misurato, mai 0 finto); `vulkan_device_ms` è marcato process-level (`vulkan_device_ms_scope: "process"`). Tool `tools/summarize_timing_sidecars.py` allineato. Test: nuovo caso in `tests/cli/test_timing_sidecar_schema.cpp`; verdi `chronon3d_cli_tests` 253/253 e `chronon3d_media_video_tests` 144/144. |
| 5 overhead fissi per-item | **PARZIALE** | Rimossa una copia profonda ridondante del piano JSON in `prepare_render_plan`: `auto plan_json = root` copiava l'intero documento (due volte per gli envelope `renderinggen.job`) prima che `decode_render_plan` producesse la propria copia di variable-resolution; ora il piano è agganciato per riferimento. `plan_decode_validate` (61–65 ms) risulta decode+validazione tipizzata singola: nessun'altra ridondanza provata nel path. `encoder_open_nvenc` (104 ms) + `encode_close` (155 ms) sono il ciclo di vita della sessione encoder per item: l'eliminazione richiede il riuso sessione cross-item, cioè il paradigma «un piano = una sessione render» (v. sotto), non un micro-fix. |
| 6 downscale immagini prima dell'upload GPU | **VALUTATO — non implementato (richiede misura di correttezza)** | Evidenza raccolta: (a) il bitmap caricato è RGBA32F a risoluzione sorgente (`CachedImage::gpu_rgba`; `SurfaceDesc.bytes = gpu_rgba.size()*sizeof(float)`) — nel golden ~8 upload e ~242 MB/item; (b) `GpuAssetCache::acquire` deduplica già per content key (nessun re-upload dimostrabile); (c) un downscale a canvas cambia i campioni di resampling → output pixel diverso ovunque l'immagine è ridotta, mentre le lane certificate impongono parità byte Vulkan↔software e i golden pixel-oracles andrebbero re-baselineati. **Exit condition**: implementarlo come cambio misurato insieme al re-baseline dei golden pixel e ai gate di parità aggiornati; la misura prima/dopo si legge già dal sidecar (`job.image.upload_ms`/`upload_count`). Non esiste variante byte-preserving del downscale; la stessa radice (decode+promozione immagini a risoluzione sorgente) è anche il costo reale che il profiling attribuiva a `font_preflight` (v. sotto). |

**Ri-attribuzione del «prepare» (evidenza dal sidecar reale
`docs/evidence/camera-authority-migration/T16/cam04-shared-plan-20261022/c3d_render150_timing.json`):**
`font_preflight_ms` 1.862 ms = `asset_decode_ms` 1.823 ms (decode + promozione
GPU immagini) + `asset_resolve_ms` 79 ms + `asset_preflight_ms` 0,2 ms; lavori
font misurati ~0. La proiezione «cache prepare/font −310 ms/item» va quindi
riletta: le cache per-runtime già servono gli asset ripetuti (hit/miss misurati
nel sidecar); il residuo è **lavoro unico-per-item su immagini a risoluzione
sorgente**, cioè la stessa radice del fix 6. Di conseguenza il percorso
realistico a ~1,3 s/item passa per fix 6 + taglio render loop, non per una
«cache prepare» aggiuntiva.

### Idee di paradigma proposte — APPROVATE 24/10/2026, ticket aperti

Dieci cambi di modello (non micro-fix) che la sessione di analisi ha prodotto
verso il gate 100/min. Ordine per (guadagno atteso × dipendenze).

**Aggiornamento 24/10/2026:** l'utente ha approvato la serie di paradigmi e
aperto i ticket operativi in `docs/operations/`:

| Ticket | Paradigma | Gate principale |
| --- | --- | --- |
| `TICKET-STATIC-OVERLAY-DURATION-INVARIANT.md` (P1) | costo overlay statico O(1) nella durata | card 30 s ≤ 1,2× card 3 s, parità pixel golden |
| `TICKET-RESIDENT-RENDER-ENGINE.md` (P2) | motore residente (unifica fix 4/5/6) | overhead fisso per-item ≤ 100 ms su 100 item |
| `TICKET-RENDER-AHEAD-SPECULATIVE.md` (P3) | render-ahead con commit per hash | overlap generate×render ≥ 50% nelle trace per-scena |
| `TICKET-BATCH-SUPERJOB.md` (P4) | un piano = una sessione render | costo batch-10 ≤ 40% del singolo; servizio ≤ 1,1 s/item |
| `TICKET-MANIFEST-IDENTITY.md` (P5) | identità per manifest (fine MP4-per-item) | A/B parità pixel/audio prima della demolizione |
| `Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md` (B2→B1→B3) | executor depth + overlay persistente + MTSDF single-pass | gate sidecar già nello spec |

Ordine esecutivo: **B2 → P1 → P2 → misura golden live (chiude il gate
100/min sulle sole 2 lane) → P3 → P4/P5**.

1. **Un piano = una sessione render** (al posto di un job per item). Ogni item
   paga oggi startup (186 ms) + open/close encoder (259 ms) + decode piano +
   prepare + receipt: ~1,3 s di overhead fisso su 2,75 s di servizio. Batchare
   gli item (per scena o per piano) in una sessione con N segmenti: uno
   startup, un encoder, un receipt. Proiezione: servizio/item → render loop +
   marginali (~1,1 s) ⇒ **~110 item/min a 2 lane già certificate**. Elimina
   alla radice le residue fix 4/5. È il cambiamento che chiude il gate senza
   superare i 2 daemon.
2. **Verifica content-addressed asincrona + certificazione per versione**.
   Fix 1/2/3 vanno in questa direzione (un probe, un digest, decode fuori
   lane): completare con il full-decode campionato per batch e per cambio
   versione del renderer (il golden) invece che per artefatto. Garanzia:
   hash+probe per artefatto + decode statistico con rifiuto idempotente.
3. **Build cache content-addressed end-to-end** (render-once-reuse): estendere
   `overlay_fingerprint` e l'ObjectStore L3 a voce (testo, lingua), artlist
   (frase, lingua), mappe (plate): retry, rerun e secondi video con item
   identici → ~0 s. Oggi il resume ricompila il piano e rirende artefatti già
   certificati: il sistema non ha memoria e ogni fallimento la paga due volte.
4. **Dataflow streaming per scena** al posto delle barriere globali: appena la
   scena N è stabile partono NLP, TTS, resolve media e planning di N mentre
   N+1 viene generata. Wall da somma(fasi) a max(fasi) (~7 min → ~2,5–3 min).
   Richiede gli eventi per-scena già richiesti nell'audit end-to-end sopra
   (`ready_at/enqueued_at/started_at/completed_at`).
5. **LLM pianificatore una tantum + riempimento deterministico**: `generate`
   (132,8 s medi, p90 97 s, max 40 min) è la voce più imprevedibile del wall;
   una/few chiamate strutturate per lo scheletro del piano + derivazione
   deterministica dei segmenti. Il batching N-scene-per-call era escluso per
   contratto editoriale (QA per-segmento): rinegoziare quel contratto È il
   cambio di paradigma.
6. **Pipeline offline-deterministica** (rete solo a ingresso/consegna): TTS
   locale GPU (40–100×), MT locale batch (20–50×), cache artlist, objectstore
   L3 come SSOT degli intermedi con Drive solo delivery. Elimina anche le
   classi di fallimento di rete (155 TTS falliti, rate limit). Voci già in
   `TODO-pipeline-100x-velocita.md` (C1/D1/E3/H1).
7. **Retry del singolo artefatto** al posto del retry del job: unità =
   artefatto content-addressed idempotente (scena, VO, mappa, item), gate
   deterministici pre-enqueue, resume per-checkpoint (I2 aperto). Regola già
   nel ticket: il primo leva è non fare lavoro che fallirà (15 h/finestra).
8. **Servizio di rendering con admission control** al posto di lane manuali e
   consegna sequenziale: code per classe di risorsa (map-heavy/phrase/final),
   anti-head-of-line esteso (la lane interattiva di script.generate: 40× sul
   primo claim), prefetch profondo, N lane fino a saturazione NVENC, capacità
   governata da `vramprobe` come prerequisito (contraddizione `gpu_lanes: 3`
   vs evidenza 2 — ora sanata dalla certificazione 3 lane del 23/10).
9. **Mappe come libreria di plate pre-renderizzati + camera in composizione**:
   le mappe sono funzioni deterministiche di (luogo, stile, path camera) e
   dominano la finestra pesante (v6: 109,8 s/127,9 s; 20–90 s/item; 396 s
   bruciati in retry su 3 item; tile cache 4,5k hit/0 miss = contenuto
   ripetitivo). Pre-renderizzare i plate all'ingestione e animare camera/testi
   in composizione: la classe più pesante esce dalla finestra.
10. **Gate unico misurato + sblocco dei blocker burocratici**: GitHub Actions
    bloccati per billing (TICKET-MAIN-TRUTH P0) congelano certificazioni
    same-SHA e demolizioni e costano sessioni intere di cerimonie locali.
    Sbloccare il billing o derogare formalmente con CI locale firmato; un solo
    gate automatico (golden 100/min + correttezza) al cambio di versione del
    renderer, non a ogni run; chiudere o archiviare i ticket bloccati.

### Sprechi/processi eliminabili — checklist (proposte, anche non approvate)

1. Doppia verifica per artefatto (~300 ms/item) → fix 2 (fatto).
2. Full decode del receipt in-lane (634 ms/item) → fix 1 (fatto, Go-only +
   variante nativa in working tree).
3. Re-preparation per item su daemon caldo → fix 4a (fatto, working tree).
4. Upload immagini a risoluzione sorgente (~300 MB/item) → fix 6 (v. exit
   condition sopra).
5. Retry di errori deterministici invariati → parzialmente chiuso
   (`retry.IsTransient`); resta I2 (retry per-checkpoint).
6. Publish Google Docs: `docs.enabled` già default OFF — verificare che nessun
   payload lo riattivi (1,1 h/sett + un giro parent→child→aggregator per
   nulla).
7. Publish Drive seriali del voiceover (~5,5 s/item sul wall end-to-end):
   delegare al pool congiunto come fatto per gli overlay (non tocca la finestra
   del gate; serve solo se l'obiettivo diventa il wall del job).
8. Recertificazione URL-only all'avvio (`broken→fresh` senza validare digest):
   corretto 22/10; tenere sotto controllo (era la causa dei 422).
9. Telemetria ingannevole: `font_preflight_ms` (rinominato, fix 4b),
   `vulkan_device_ms` (marcato process-level, fix 4b), `wall_ms=0` incoerenti
   nell'API aggregata (ancora da correggere).
10. Lavoro ripetuto cross-run (re-download, re-probe, re-translate, re-TTS):
    mirror F1 e probe cache F3 attivi; mancano voice cache (C3), artlist cache
    (E3), MT locale (D1) — v. paradigma 6.
11. Non forzare la pulizia del codice: l'audit 23/10 (`AUDIT_PRUNING_CENSUS`)
    ha verificato che quasi nulla è rimovibile oggi; le uniche candidate a zero
    caller sono le superfici telemetriche orfane (`NodeStatsReporter`,
    `stats_json_file`), di proprietà di ticket aperti.

### Misura mancante (invariata)

Le fix 4/5 del working tree vanno misurate con rerun golden (3 run, mediana,
stesso payload immutabile) secondo il contratto di misura — non eseguibili da
questo checkout di sviluppo. La fix 2 è già stata misurata dal workstream che
l'ha portata sul binario (v13-v18).

### Candidato architetturale aggiuntivo — overlay senza MP4 per item

La review Freebuff del 23/10 propone un cambio più ampio rispetto alla sessione
render persistente: valutare se gli overlay possano essere layer nella timeline
finale, invece di produrre un MP4 autonomo per ogni item. Oggi ogni artefatto
paga encoder, mux, receipt, pubblicazione Drive e successivo stitch; una
composizione diretta o un batch per scena potrebbe rimuovere parte di questi
passaggi. È una proposta da valutare, non una decisione né un guadagno misurato.
Dipende dal contratto del final worker (oggi remoto e packet-copy) e
dall'identità/link Drive richiesta per ogni overlay.

Prima di sceglierla, confrontare due opzioni con lo stesso payload: (A) sessione
persistente che conserva MP4 separati; (B) overlay compositati nel render finale
o in artefatti per-scena. Accettazione: parità pixel e audio, link e receipt
tracciabili, retry idempotenti, output per-classe invariati e wall misurato su tre
run. Pubblicare throughput per classe (frase, immagine/entity, numero, mappa),
oltre al totale: una mappa da decine di secondi non deve essere mediata con una
frase breve per dichiarare raggiunto il gate.

---

## Verifica 24/10/2026 — criteri raggiungibili in questo checkout

Questa sezione separa esplicitamente ciò che è stato **verificato qui** da ciò
che richiede una run live, GPU o un altro repository. Il working tree non è
committato: il binario in esecuzione (`fd489e5c8`, avviato 09:09 UTC) contiene
planner 100 e render cache, ma **non** le modifiche successive (batching,
local-only voiceover, taglio della copia audio finale). Per portarle in
produzione serve ricostruire e riavviare; nessun numero live in questa sezione
le attribuisce un guadagno non misurato.

### Gate repository eseguiti (tutti verdi)

| Gate | Comando | Esito |
| --- | --- | --- |
| Build completa | `go build ./...` | PASS |
| Formattazione | `gofmt -l .` | nessun file |
| Vet | `go vet ./...` | PASS |
| Architettura strict | `go run ./cmd/archcheck --strict` | `passed=true`, `has_hard_gate_hits=false`, 0 violazioni, 75 warning (budget 77) |
| Foundation | `make verify-foundation` | PASS (gitleaks: no leaks; submodule integrity; `go mod tidy` invariato) |
| Static | `make verify-static` | PASS (alias overlay rigenerato, vet, build) |
| Unit fast | `make verify-unit-fast` | 216 pacchetti `ok`, 0 `FAIL` |
| Componenti cambiati | `make verify-changed-components` | PASS su 21 componenti |
| Architettura aggregata | `make verify-architecture` | PASS (C2-C=0, C2-E=0) |
| Cache/analytics dopo migration 280 | `go test -race ./internal/capabilities/scripts -run 'Test(OverlayRenderCache|PlanOverlayBatchWork|FinalJobRejectsBatchReplaceWithoutCanonicalBase|FinalAudioPublishesBeforeOverlayJoinButFinalizesAfterJoin|AudioOverlayFailureAndCancellationDrainBothBranches)$' -count=1`; package analytics/wiring test; SQLite `TestMigrations_280_RenderAttemptCacheTimingColumns` | PASS |
| Pacchetti Go toccati nei due repository | PipelineGen scripts/jobs/worker/SQLite/goldensummary/wiring; RenderingGen processor/metricnames/schemaval/overlay; `RenderingGen/queue go test ./...` | PASS |

### Criteri di accettazione: raggiungibile / non raggiungibile

| Criterio | Raggiungibile ora | Evidenza |
| --- | --- | --- |
| Il planner materializza il mix da 100 item | **Sì** | `TestGoldenThroughputMixBudgetAdmitsOneHundredItems` PASS: 50 frasi, 30 immagini, 10 date, 10 mappe |
| Render cache content-addressed cablata | **Sì** | 11 test `TestOverlayRenderCache*` PASS; `/health` del binario live 09:09: `enabled=true`, `wired=true`, tabella `overlay_render_cache_entries` |
| Timing durevoli lookup/probe/store cache | **Sì (contratto Go/SQLite)** | Migrazione 280, test adapter/upsert, test timed cache lookup e aggregazione `goldensummary`; i tempi live richiedono una nuova run del binario migrato |
| Batching finestre deterministico e identità per item | **Sì (unit)** | 5 test `TestPlanOverlayBatchWork*`, `TestFinalJobRejectsBatchReplaceWithoutCanonicalBase`, `TestSeparateOverlayItemsPreservePlanOrderAndIndices` PASS; nessuna certificazione live |
| Copia streaming dell'audio finale certificato | **Sì (unit)** | `TestCopyCertifiedAudioOverwritesDestinationAndPreservesBytes` PASS; hash sorgente già verificato a monte |
| Nessuna query Artlist quando il provider è disabilitato | **Sì (unit)** | `TestSceneIRSegmentEnricherExtractsFromNarrationNotEditorialBrief` asserisce `ArtlistQueries` vuoto |
| Voiceover per-item local-only (niente upload Drive nel critical path) | **Sì (unit)** | test voiceover/service e jobs PASS; `timing.json` locale è la SSOT, nessun cleanup Drive senza file remoto |
| Gate 100 overlay/min misurato su golden live, 3 run, stessa macchina | **No** | Richiede Chronon/RenderingGen, GPU e payload golden; non eseguibile in questo checkout. I numeri restano quelli v15–v18 (47–48/min) |
| Parità pixel/audio su golden decodificato | **No** | Richiede decode dei golden e re-baseline dichiarato; i test unit non sostituiscono il confronto pixel |
| P1 durata statica O(1) | **No** | Lavoro Chronon3d (C++), fuori dal working tree Go |
| P2 motore residente, P4 batch-superjob, P5 manifest identity | **No** | C++ Chronon3d e contratto cross-team col final worker; ticket dedicati, nessuna implementazione in questo checkout |

**Conclusione onesta:** i criteri Go raggiungibili sono chiusi e pinned dai
gate sopra. Il gate 100/min resta **aperto e non misurato** in questa sessione:
nessuna modifica di questo working tree deve essere presentata come un guadagno
di throughput finché non viene ricostruita, riavviata e misurata con il
contratto di misura (§ «Contratto di misura»).
