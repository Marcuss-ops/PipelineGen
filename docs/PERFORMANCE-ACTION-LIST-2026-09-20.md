# Performance action list — script/render pipeline

## Stato implementato in questo pass

- [x] **P2 — translated NLP per scena/lingua**
  - `SceneReadyCoordinator` avvia la NLP appena la traduzione è pronta.
  - `WaitForScene` usa il fence `(scene_index, revision, text_hash)` e non attende più la barriera VidRush globale.
  - La fase finale riusa `LocalizedAnnotations` già calcolata e non rilancia la NER.
  - Concorrenza NLP invariata a `DefaultNLPConcurrency = 4`.

- [x] **Checkpoint TTS/translation fuori dal lock applicativo**
  - Sotto `applyMu` resta solo la mutazione della scena e la creazione dello snapshot immutabile.
  - La scrittura SQLite avviene dopo il rilascio del lock.
  - Debounce a 500 ms più flush finale; la finestra intenzionale di perdita in caso di crash è quindi al massimo il debounce.

- [x] **Publication pool Drive aumentato**
  - `defaultOverlayPublicationWorkers`: `2 → 6`.
  - `SetAsyncPublication(true)` è già cablato nel production wiring.
  - Il drain resta joinato prima del completamento del run: cambia il throughput, non la correttezza.

- [x] **Gate `overlay.prepare`**
  - I piani senza `AssetRefs` saltano enqueue, lease e round-trip RenderingGen.
  - I riferimenti immagine verificati vengono trasportati nel payload dell’intent entity senza creare un overlay immagine duplicato.

- [x] **Streaming e polling verificati**
  - Streaming SceneTextReady è attivo quando il generator espone `SceneTextStreamer` e il piano è eleggibile; i marker `SCENE N:` lo disabilitano per sicurezza.
  - Il completamento RenderingGen è event-driven; il fallback configurato è 250 ms, già sotto il target 500 ms–1 s.

- [x] **Pulizia legacy A1–A7 verificata**
  - A1/A2/A3/A5/A6/A7 risultano già eliminati: nessuna interfaccia phrase-extractor, nessun campo `PhraseExtractor`, nessun adapter Ollama, nessun fallback NLP, nessun commento stale nel codice e nessun `serialMode` production.
  - A4 è stato ricontrollato sui chiamanti reali e non va eliminato: `ProviderResolver` è cablato dal wiring production; `StockResolverPort` è attivo quando è disponibile il catalogo locale; `SemanticAndFanoutResolver` compone stock video e fan-out immagini senza duplicare la ricerca Artlist.
  - Il commento dei port semantici ora descrive la superficie rimossa senza mantenere i vecchi identificatori come falsi riferimenti.

- [x] **Runtime production verificato dopo il fix TTS**
  - Il warm-up persistente TTS ora ritenta correttamente il bind locale (`IsRetryable` esplicito); il fallback spawn-per-call non viene più attivato per il normale race di startup.
  - Il cache wiring separa `cacheDB` da `contentDB`: `artifact_cache_*` resta nel DB cache, `content_objects` resta nel DB primario.
  - `/ready` ha un budget di probe profondo di 15 s; il verifier systemd usa lo stesso ordine di grandezza e non trasforma il canary Drive/TTS in un falso timeout.

## Benchmark production rehearsal — 2026-09-20

- [x] Job di riferimento riuscito: `SUCCEEDED`, 10 lingue richieste, 47 overlay certificati, 47/47 artefatti con SHA-256 valido, receipt Chronon e timing preservati.
- [x] Wall runtime: **225,428 ms (225.4 s)** contro baseline **282.9 s**: **−57.5 s / −20.3%**.
- [x] Critical path: `generate` **105.6 s**, `scene_analysis` **100.8 s**, `overlay_render` **83.1 s**.
- [x] Render: 47 chiamate, **163.9 s** di lavoro cumulativo, **188.8 s** di attesa GPU cumulativa; il serialismo del daemon resta quindi un collo di bottiglia aperto.
- [x] Telemetria: **203.3 s** attribuiti, **22.1 s** non attribuiti (**9.82%**), **102.0 s** sovrapposti.
- [x] Cartelle Drive: 10 cartelle distinte (una per lingua), nessuna collisione; tutti i render risultano `COMPLETED`.
- [x] Verifier aggiornato al payload reale (`result.result` + mappe `localized_overlay_*`): ora espone stage, critical path, conteggio item, cartelle, SHA-256, receipt e timing invece di riportare campi vuoti.

### Nota di correttezza overlay — chiusa nel rerun live

Il contratto tecnico è integro per tutti i 47 render: ogni frase ha `motion_id`, ogni immagine ha `preset_id`, timing valido e asset hashato. Il matching applicativo è stato esteso con suffissi turchi e un fallback NER conservativo per ru/tr/pl: l’identità canonica e il binding immagine vengono ereditati solo quando il NER restituisce esattamente gli stessi PERSON in ordine testuale.

Rerun live `job_1789899217398473074_edcdaa26`: `SUCCEEDED`; tutte le 9 lingue localizzate hanno 5 item, di cui 2 `entity_image` (`ru=2/2`, `tr=2/2`, `pl=2/2`). Il fix aggiuntivo era il fallback collision-safe dell’overlay ID per nomi cirillici: lo slug ASCII vuoto faceva collassare due persone distinte nel resolver.

## Lavori prioritari ancora aperti

### P0 — benchmark production completato

- [x] Eseguito lo stesso job di riferimento e raccolto:
  - `wall_ms` totale e per stage;
  - `checkpoint_wait_ms`, `checkpoint_seconds`, numero e byte delle scritture;
  - `translated_nlp_wall_ms`, chiamate NER e cache-hit;
  - `tts_wall_ms`, `tts_calls`, `tts_concurrency` effettiva;
  - `render_wall_ms`, `render_work_ms`, `gpu_lane_wait_ms`, `gpu_lanes`;
  - `publication_wall_ms`, `publication_workers`, `publish_queue_depth`;
  - `overlay.prepare` skipped/enqueued e motivo;
  - `scene_text_path`, `streamed`, `tts_first_started_ms`, `render_first_started_ms`.
- [x] Confrontata la baseline `282,9 s`: il wall è sceso a `225,4 s`; il guadagno è reale sul critical path, mentre il lavoro GPU cumulativo resta serializzato.

- [x] Risolto il wiring della tabella `content_objects` e verificato `/ready` dopo riavvio: 200 stabile, nessun nuovo `no such table: content_objects` nei log.

### P1 — RenderingGen: rimuovere la serializzazione effettiva del daemon

- [ ] Attivare/verificare il secondo worker RenderingGen (`renderinggen-b.yaml`) sull’host production **solo dopo** aver verificato un daemon Chronon indipendente; il servizio B esiste ma oggi è inattivo e punta allo stesso socket.
- [ ] Verificare che ogni worker abbia un daemon Chronon indipendente e che il mutex `RENDER_JOB` non sia condiviso tra i worker.
- [ ] Misurare VRAM peak per job e OOM; non aumentare `gpu_lanes` alla cieca.
- [ ] Criterio: riduzione della coda `gpu_lane_wait_ms` e del wall render senza OOM o fallback CPU.

### P1 — Chunk producer I1

- [ ] Collegare `chunk_plan.go` al produttore che invia i figli con `parent_job_id`, `chunk_index` e `frame_range`.
- [ ] Implementare l’anchor assembly-only dopo la certificazione di tutti i chunk.
- [ ] Estendere la chiave render cache a `(fingerprint, frame_range)`.
- [ ] Aggiungere test di ordine, gap/overlap frame range, retry di un solo chunk e assembly idempotente.

### P2 — publication tuning configurabile

- [x] Rendere `6` configurabile (`overlay_publication_workers` / `VELOX_SCRIPTS_OVERLAY_PUBLICATION_WORKERS`) con cap 8; wiring production applicato prima dell’avvio del pool.
- [ ] Misurare errori HTTP/rate-limit Drive e latenza p50/p95 prima di passare a 8.
- [ ] Valutare in seguito la separazione dello stato `SUCCEEDED` del job dalla conferma Drive, mantenendo un retry/outbox verificabile.

  Ultimo rerun: 11 risposte Drive HTTP 502 transitorie sono state ritentate; il job è comunque terminato `SUCCEEDED`, ma il wall di `434.993 s` non è confrontabile con la baseline a causa del retry storm. Questo conferma che la separazione `SUCCEEDED`/outbox richiede una semantica durevole prima di essere attivata.

### P2 — parità immagini tradotte

- [x] Aggiungere matching dei suffissi di caso/possessivo turchi.
- [x] Aggiungere fallback ru/tr/pl basato su NER localizzato, conteggio PERSON e ordine delle occorrenze; nessuna identità viene inventata se il conteggio non coincide.
- [x] Rilanciare il rehearsal e certificare i conteggi immagine per lingua prima del gate editoriale: `9/9` lingue localizzate con `2/2` immagini.

### P2 — checkpoint follow-up

- [x] Aggiungere metriche esplicite `checkpoint_debounced_total` e `checkpoint_flush_total`; il gate rilascia `inFlight` con `defer` anche su panic della scrittura.
- [ ] Verificare con un fault injection che il crash in ogni finestra conservi l’ultimo snapshot e che il resume non rilanci unità già certificate.

## Bound da non modificare senza nuova certificazione

- `DefaultTTSConcurrency = 4`.
- `DefaultGenerationConcurrency = 3`.
- `MaxTranslationConcurrency = 4`.
- Fan-out SceneTextReady e pool GPU: il producer non sostituisce l’autorità del worker RenderingGen.
