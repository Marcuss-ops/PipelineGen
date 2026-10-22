# Ticket: portare la generazione degli overlay a 100/minuto

Stato: **APERTO — baseline live fallita per asset ObjectStore corrotto; golden da rilanciare dopo bonifica**
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

Nel job `08_fonti_media` la fase overlay dura **454,2 s**, ma le metriche registrano solo **41,0 s** di render Chronon e **43,2 s** di attesa terminale del queue per 13 elementi. Nel percorso `final_job`, ogni child ereditava `RequireDriveBeforeReturn=true`: il codice pubblicava quindi su Drive in modo sincrono dopo ogni render e il batch profondo era limitato a un worker. La differenza di circa **370 s** non aveva un'operazione Drive propria nei dati storici; l'attribuzione alla pubblicazione seriale è una forte inferenza dal percorso del codice e dal tempo non spiegato, da confermare con la nuova metrica `overlay_upload`.

Correzione implementata nel worktree, da misurare: il child delega la pubblicazione al pool Drive limitato già esistente; il ramo overlay si ricongiunge al pool una volta completati tutti i child e prima che il final job costruisca il payload. La garanzia resta: nessun final render parte prima che gli overlay siano pubblicati e abbiano i Drive ID certificati. La misura per `overlay_upload` registra ora il costo separato. Non è stato aumentato il parallelismo GPU: verrà ritoccato dopo avere misurato la nuova composizione render/upload.

Modifica in `internal/capabilities/scripts/runner_lifecycle.go`: il run pianifica retry solo per errori marcati transient dal contratto tipizzato condiviso, oltre alle deferral esplicite per un final render remoto ancora in esecuzione e al caso recuperabile dell'audio locale mancante. Quel caso ora riprende da `audio_compile`, azzera il solo riferimento al file scomparso e conserva gli overlay già renderizzati; non ripete più la stessa pubblicazione sullo stesso path inesistente. Gli altri errori deterministici e non classificati cessano di ripetere automaticamente il lavoro invariato. I confini che possono recuperare un errore transitorio devono propagare `retry.TransientInfrastructureError`.

Riferimenti: `ops/jobs/garlasco_batch_10_20261019_sequential_v7/manifest.json`, i payload `*.generate*.json` della stessa cartella e `TODO-milton-finaljob-performance.md` per precedenti misure di fasi overlay e tempi di job (non direttamente comparabili finché non vengono normalizzate).

### Golden case v2 — run live overlay-only (22 ottobre 2026)

Payload immutabile: `ops/jobs/overlay_throughput_golden_100_20261022.v2.generate.json` (SHA-256 `60fb14ec1e6ace95489f38e2b84f91dd7a28d45d89edbcf1e5c060a52d63080d`). Deriva dal JSON reale `08_fonti_media.generate.v11.json`: mantiene i 20 segmenti, le impostazioni editoriali e le tre clip intro già selezionate (`yt_CHn2h59EptE_2235_2250_v1`, `yt_CHn2h59EptE_2235_2280_v1`, `yt_CHn2h59EptE_2520_2565_v1`), con progetto e correlazione dedicati. Il mix richiesto è 25 frasi brevi, 25 lunghe, 30 immagini (10 schede entità comprese), 10 date e 10 mappe; `max_overlays_per_scene=10`. Il riuso della cache è abilitato, il refresh forzato degli asset è disabilitato e `final_job=false`: la misura termina al percorso overlay senza generare il video finale.

La prima prova live v1 con `final_job=true` è stata interrotta dopo che la fase overlay era terminata. Il job `job_1792685428589251856_a9f967b8` ha renderizzato e pubblicato 39 overlay tra le 16:15:58 e le 16:19:16 UTC; il final job remoto è poi fallito per `timeline segment 33 has zero frames`, con due overlay numerici mancanti in prefetch. Il binario attivo `53c200925` (avvio 14:38 UTC, precedente alle correzioni) ha pianificato un retry sullo stesso errore terminale; il job è stato annullato alle 16:23 UTC per non spendere altra capacità sul video finale. Il conteggio 39 è inferiore ai 100 richiesti: questa prova non certifica il golden.

La run v2 overlay-only **non ha completato** e non costituisce baseline di throughput: job `job_1792686265115244060_107c6817`, inviato alle 16:24:25 UTC sul binario `53c200925` (processo avviato alle 14:38 UTC, quindi precedente alle correzioni). È stato annullato alle 16:39:06 UTC dopo due fallimenti identici. `final_job=false`; nessun video finale è stato richiesto o prodotto.

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

### Roadmap operativa del job

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
- [ ] Separare il wall time per fase: generazione del piano/contenuto, risoluzione immagini e asset, enqueue/wait, rendering, validazione e retry. Registrare parallelismo e risorse della macchina, quando disponibili.
- [ ] Identificare i colli di bottiglia principali con dati; distinguere tempo CPU/GPU attivo, attese esterne e lavoro ripetuto.
- [x] Creare un **payload golden da 100 overlay** ripetibile, derivato da un job reale rappresentativo e coperto da una composizione documentata di tipologie. Payload fissato e hashato.
- [ ] Completare una run golden valida: la v2 è fallita prima di poter attestare overlay validi; ripubblicare l'asset corretto, eseguire su build aggiornati e rigenerare il piano senza checkpoint contaminato.
- [ ] Eseguire il golden case sul codice corrente e registrare tempo totale, overlay/minuto, tempi di fase, overlay effettivi, errori, retry, parallelismo e risorse. Ripetere almeno tre volte e riportare mediana e dispersione.
- [ ] Salvare input, output atteso, report e comando di ripetizione in una cartella versionata sotto `ops/jobs/` (o nella sede di fixture del componente, se il caso è puramente di test).
- [ ] Selezionare almeno un job storico rappresentativo, conservandone payload e riferimento ai risultati già disponibili. Rifarlo sul golden path solo se gli input e le dipendenze sono riproducibili; altrimenti usare la stessa richiesta con asset fissati e dichiarare la differenza.
- [ ] Dopo ogni modifica, ripetere il golden case sulla stessa macchina/configurazione e confrontare throughput, tempi di fase e qualità con il baseline. Non accettare miglioramenti fondati solo sul wall time complessivo se cambia il numero di overlay validi.
- [ ] Applicare ottimizzazioni solo ai colli di bottiglia confermati, preservando l'ordine, il contenuto, il layout e il rendering richiesti.
- [x] Impedire il retry automatico dei fallimenti non tipizzati, che ripetevano input e checkpoint invariati; preservare il yield previsto per il rendering remoto ancora attivo.
- [x] Togliere la pubblicazione Drive seriale dal critical path dei render final-job, mantenendo il join prima del final render e aggiungendo la misura `overlay_upload`.
- [x] Recuperare il file audio locale scomparso ripartendo da `audio_compile` al retry, conservando i render overlay già certificati.

## Contratto di misura

```text
throughput = overlay validati / minuti di wall time della finestra misurata
```

Pubblicare anche il tempo dall'invio del job al completamento, ma riportare separatamente le attese in coda. Definire una volta nel report se il throughput si riferisce alla sola lavorazione attiva o al tempo end-to-end; non confrontare finestre diverse come se fossero equivalenti. Un overlay conta come valido solo quando è materializzato, renderizzato e supera i controlli di output previsti.

Per ogni run salvare almeno: `run_id`, input hash, revisione, configurazione e worker, orari UTC, richiesti/pianificati/materializzati/validati, durata per fase, wait di coda, retry/failure e output hash. Nessun dato mancante deve essere interpolato come misura.

## Criteri di accettazione

- [~] Payload golden immutabile da 100 overlay, ripetibile e verificabile; hash fissato. La prima run live non ha completato e non prova il throughput.
- [ ] Baseline iniziale del golden case registrata con almeno tre run confrontabili e conteggio degli overlay validi.
- [ ] Almeno un job storico valutato: confronto diretto quando riproducibile, altrimenti confronto documentato con fixture equivalente.
- [ ] Ogni fase costosa e le cause di retry/mancata materializzazione risultano attribuibili o segnalate come non osservabili.
- [ ] Gate di throughput concordato e misurato: obiettivo **100 overlay/minuto** sul golden case, senza regressioni di correttezza, quantità o qualità visiva.
- [ ] Report prima/dopo per ogni ottimizzazione, con condizioni di esecuzione comparabili e nessuna regressione nascosta da overlay falliti o non contati.

## Nota sul target

100 overlay/minuto è il target richiesto, non una prestazione già dimostrata né una garanzia per job con dipendenze esterne o composizioni differenti. Il ticket deve riportare sia il risultato del golden case sia il throughput dei job reali per tipologia, così da rendere espliciti i limiti di confronto.
