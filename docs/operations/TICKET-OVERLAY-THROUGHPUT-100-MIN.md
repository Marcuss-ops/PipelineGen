# Ticket: portare la generazione degli overlay a 100/minuto

Stato: **APERTO — baseline da misurare**  
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

### Golden case v1 — baseline live in corso

Payload immutabile: `ops/jobs/overlay_throughput_golden_100_20261022.generate.json` (SHA-256 `c31f3a6e5a01c6c024dc84d687ec7c66662cff55a9cb279fdb80a54ea6f9ef50`). Deriva dal JSON reale `08_fonti_media.generate.v11.json`: mantiene i 20 segmenti, le impostazioni editoriali e le tre clip intro già selezionate (`yt_CHn2h59EptE_2235_2250_v1`, `yt_CHn2h59EptE_2235_2280_v1`, `yt_CHn2h59EptE_2520_2565_v1`), con progetto e correlazione dedicati. Il mix richiesto è 25 frasi brevi, 25 lunghe, 30 immagini (10 schede entità comprese), 10 date e 10 mappe; `max_overlays_per_scene=10`. Il riuso della cache è abilitato e il refresh forzato degli asset disabilitato per non confondere il benchmark con download duplicati.

Prima baseline live inviata il 2026-10-22: job `job_1792685428589251856_a9f967b8`, endpoint locale `127.0.0.1:8000`, stato iniziale RUNNING. L’API attiva dichiara il build `53c200925` (avviato alle 14:38 UTC), che precede la correzione; questa run misura quindi il percorso precedente. Risultato completo e breakdown delle fasi da allegare dopo il completamento. Il confronto della correzione richiede lo stesso payload e un build che contenga il commit `5538ed8d6`.

## Lavoro richiesto

- [~] Ricostruire i job recenti e storici con timestamp di inizio/fine, script e revisione, overlay richiesti/pianificati/materializzati/validati, esito e numero di retry. Prima ricostruzione dei dieci job completata; resta da classificare la telemetria per tipo di item e per worker.
- [ ] Separare il wall time per fase: generazione del piano/contenuto, risoluzione immagini e asset, enqueue/wait, rendering, validazione e retry. Registrare parallelismo e risorse della macchina, quando disponibili.
- [ ] Identificare i colli di bottiglia principali con dati; distinguere tempo CPU/GPU attivo, attese esterne e lavoro ripetuto.
- [~] Creare un **golden case da 100 overlay** ripetibile, derivato da un job reale rappresentativo e coperto da una composizione documentata di tipologie (frasi, immagini/entity-card, date e mappe). Payload fissato e hashato; prima run live in corso. Restano conteggio degli output validi e rerun sul build corretto.
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

- [~] Golden case immutabile da 100 overlay, ripetibile e verificabile automaticamente o con report verificabile; payload v1 fissato, verifica output e report della run ancora da completare.
- [ ] Baseline iniziale del golden case registrata con almeno tre run confrontabili e conteggio degli overlay validi.
- [ ] Almeno un job storico valutato: confronto diretto quando riproducibile, altrimenti confronto documentato con fixture equivalente.
- [ ] Ogni fase costosa e le cause di retry/mancata materializzazione risultano attribuibili o segnalate come non osservabili.
- [ ] Gate di throughput concordato e misurato: obiettivo **100 overlay/minuto** sul golden case, senza regressioni di correttezza, quantità o qualità visiva.
- [ ] Report prima/dopo per ogni ottimizzazione, con condizioni di esecuzione comparabili e nessuna regressione nascosta da overlay falliti o non contati.

## Nota sul target

100 overlay/minuto è il target richiesto, non una prestazione già dimostrata né una garanzia per job con dipendenze esterne o composizioni differenti. Il ticket deve riportare sia il risultato del golden case sia il throughput dei job reali per tipologia, così da rendere espliciti i limiti di confronto.
