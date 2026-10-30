# TICKET: Architettura per la Massima Velocità di Generazione e Rendering Overlay (VeloxEditing)

- **Data:** 2026-10-26
- **Autori:** Antigravity + Pierone
- **Moduli interessati:** `refactored` (PipelineGen), `RenderingGen`, `Chronon3d`, `BrainRust` (pipelinegen-muscles)
- **Stato:** ATTIVO / IN IMPLEMENTAZIONE
- **Target Prestazionale:** Throughput overlay da ~80–100/min a **>2.500 overlay/min**; tempo di pianificazione da 4m45s a **<30s**; eliminazione del 90% dell'I/O disco.

> **Lettura operativa (29-10-2026):** i target e i fattori delle sezioni storiche
> sono ipotesi, non risultati. L'ordine di lavoro corrente è §10; lo stato
> osservato e le limitazioni sono §9.8. La mediana 62,32 item/min riguarda tre
> run comparabili v2.1 **senza mappe**, 66–69 item: non certifica il mix da
> 100. Le proiezioni 1.800–3.000/min e il confronto 83,98/min non sono
> accettazioni; l'ultimo non è comparabile con la serie §9.8.
> Per Chronon vale il freeze in `Chronon3d/docs/CURRENT_STATUS.md`: niente
> nuova architettura prima di CI e baseline same-SHA verdi. Le sezioni sotto
> descrivono lavoro futuro, non autorizzano deploy o attivazioni live.

---

## 0. TODO — cosa manca (agg. 2026-10-28; revisione 2026-10-29)

Pending "GOAL A" dalla sessione Freebuff 2026-10-25 — sessione encoder persistente
cross-item (−259 ms/item) per chiudere il gap 83,98 → ≥100 overlay/min.
Revisione 2026-10-29 dopo verifica sui codici:

- [x] A1. Mappare il ciclo di vita dell'encoder e il meccanismo encoder-reuse disabilitato (dove/why fail-closed). — `native_av_encoder_lifecycle.cpp`: cache single-entry `CodecSession`, `codec_session_reuse_enabled()` opt-in via `CHRONON_ENCODER_SESSION_REUSE=1`, gate di configurazione supportata (CQP NVENC / CQP software zero-latency), boundary «packet-complete» il close() non invia EOS e il codec resta UNFLUSHED. Fail-closed: EOS-flushed non si parca (AVERROR_EOF verificato empiricamente 26-10), fingerprint matching, CRF/x264 runner esclude il riuso (stato adattivo). Nota: il P4 one-session segmented writer è fuori dal meccanismo e non ne risente.
- [x] A2. Implementare il riuso sessione sicuro + contratto mux/EOF nel percorso render. — Implementato lato Chronon3d (`park_codec_session_for_reuse` + `acquire/release_codec_session`, sessione adottata con fingerprint loggato). Opt-in: il flag NON è impostato nei drop-in systemd live (`/etc/systemd/system/chronon3d*.service.d/`); le unità production non attivano il riuso. Attivazione operativa separata dal codice: richiede restart dei daemon, è disabilitata di default per rispetto del boundary «un encoder per item» solo in fallback non-P4, e invalida la comparabilità con le run storiche se attivata a metà serie di gate.
- [ ] A3. Test di regressione (doctest) + suite mirate verdi. — Test presente: "Cross-item codec session reuse stays fail-closed" in `tests/video/test_native_av_encoder_teardown_stress.cpp` (26-10); rerun suite mirata da eseguire per chiudere A3. **Rerun 29-10: BLOCKED (cross-repo)** — `Chronon3d/` non ha alcun `CMakeCache.txt` né albero di build per i test (`build/` contiene solo `chronon` e `Testing/`): il target non è costruibile senza un configure+build completo fuori dal perimetro `refactored`. Nessun PASS dichiarato.
- [ ] A4. Build `-j14`, commit mirato, deploy release, restart daemon. — **Parziale 29-10 (solo `refactored`)**: `go build -o bin/pipelinegen ./cmd/server` PASS, restart `systemd` PASS, `/ready` HTTP 200 e env verificato da `/proc/<pid>/environ`. Restano non eseguiti: build Chronon3d `-j14` (nessun build tree, come A3) e commit mirato (nessun commit richiesto/effettuato in questa sessione).
- [ ] A5. Golden ×3 di confronto + mediana vs baseline 83,98/min; aggiornare il ticket. — **3 run comparabili eseguite 29-10** (stesso payload v2.1-nomaps SHA `ad5e3468…`, stesso binario, 3 lane): 58,55 / 62,32 / 73,57 item/min → **mediana 62,32 item/min, gate ≥100 FAIL**. Canary 4 lane: 76,16 item/min. Dettaglio e cause in §9.8.
- [ ] Aggiornare i MD di closeout con root cause motion-blur, fix e stato lavori.

## 1. Diagnosi del Collo di Bottiglia Storico e Risoluzioni Effettuate

### 1.1 Root Cause del Blocco Storico (1–2 overlay/minuto)
- **Il problema:** Nelle run storiche, `PipelineGen` tentava di renderizzare l'intero video master continuo da 17–20 minuti (`30.540` frame a 1080p @ 30fps) all'interno dello stesso job via FFmpeg software/wrapper, impiegando oltre 75 minuti e mandando il runner in timeout (`submit_rc=2`).
- **L'effetto collaterale:** Questo faceva apparire il sistema bloccato a ~1–2 overlay/minuto, quando in realtà il render grafico degli overlay non era il collo di bottiglia.

### 1.2 Correzione dell'Invarianza Statica in Chronon3D
- **File:** `Chronon3d/src/render_graph/core/scene_hasher.cpp#L481`
- **Bug risolto:** In `SceneHasher::is_effectively_static_at()`, layer con animazioni o reveal futuri impedivano la marcatura di frame statici antecedenti.
- **Fix:** Inserito `if (!layer.active_at(frame)) continue;`. Questo permette ora di bypassare interamente la rasterizzazione GPU Vulkan sui frame di freeze-hold post-reveal (`DuplicateRunMode::Resubmit` e `Replicate`), riutilizzando direttamente il frame NV12/NVENC.
- **Build verificata:** `chronon3d_cli` compilato con `ninja -j 12` (profilo `linux-video-release`), asserzioni PASS.

### 1.3 Disaccoppiamento Architetturale in PipelineGen
- **Files:**
  - `refactored/internal/kernel/script/output_spec.go`
  - `refactored/internal/capabilities/scripts/runner_phase_overlay_render.go`
  - `refactored/internal/capabilities/scripts/runner_deps.go`
- **Intervento:** Aggiunto il flag esplicito `OverlaysOnly: true` nel contratto `VideoRenderSpec`.
  - La fase di pianificazione e rendering overlay (`runOverlayRenderPhase`) si attiva se `req.Render.Enabled || req.Render.OverlaysOnly`.
  - La fase di ricostruzione delle clip video di scena (`enqueueLocalizedRender`) fa un no-op immediato quando `OverlaysOnly` è true, evitando il render dei 30.540 frame del master.
- **Esito live:** Job da 72 overlay pianificato con successo in 4m45s con `0` frame video master renderizzati in PipelineGen.

### 1.4 Benchmark Live di Chronon3D Isolato
- **Misura reale:** `go test -v -run TestSegmentedSessionMatchesIndependentSingleItemRenders ./internal/processor` in `RenderingGen/renderinggen`.
- **Telemetria GPU:**
  - `render_ms`: 8.12 ms (rasterizzazione Vulkan MTSDF)
  - `encode_ms`: 4.63 ms (NVENC hardware)
  - `execute_ms`: 218.0 ms (tempo totale wall per singolo overlay compreso setup)
- **Throughput reale misurato del render worker:**
  - 1 demone Chronon3D: **~250–270 overlay/minuto**
  - Pool attuale (3 demoni `chronon3d`, `b`, `c`): **~750–800 overlay/minuto**

---

## 2. Limiti Fisici della Macchina e Verità Hardware

### 2.1 Censimento Risorse Host
- **Host:** Linux Ubuntu, 32 GB RAM, GPU NVIDIA RTX A4000 (16.376 MiB VRAM).
- **Occupazione VRAM live:**
  - `llama-server` (Ollama LLM residente, modello `gemma4:e4b`): **5.156 MiB**
  - 3 daemon `chronon3d_cli` attivi: **~2.938 MiB** (~1.450 MiB ciascuno per framebuffer ring, swapchain headless, glyph atlas)
  - Memoria VRAM totale usata: **8.188 MiB**, libera: **~8.188 MiB**.

### 2.2 Vincolo di Scalabilità
> [!WARNING]
> **Impossibilità di avviare 12 daemon isolati:**
> 12 processi monolitici da ~1.450 MiB richiederebbero oltre **17.4 GB di VRAM**. Insieme ai 5.1 GB di Ollama, supererebbero i 16.3 GB della RTX A4000 provocando un crash irreversibile di **CUDA Out-Of-Memory (OOM)**.
>
> La scalabilità a >2.500 overlay/min non deve quindi passare dalla moltiplicazione sconsiderata dei processi di sistema, ma dalle ottimizzazioni architetturali **P0, P2 e P3**.

### 2.3 Vincolo di Build (misurato 2026-10-27)
- La build di Chronon3d con `ninja -j 12` **OOM-kill di `cc1plus`** sulla stessa macchina (32 GB RAM, ~5 GB già residenti per llama-server + i daemon): il parallelismo di compilazione va tenuto a `-j 3` (build completo del target test ≈ 20-25 min da freddo). Il vincolo è di MEMORIA DI COMPILAZIONE, non di GPU: non interviene a runtime.

---

## 3. Le Tre Proposte Architetturali ad Altissimo Rendimento

### Priorità P0: Cross-Scene Mega-Batching (Saturazione NVENC)
- **Meccanismo:**
  Attualmente il batching P4 impacchetta solo overlay all'interno della stessa scena o dello stesso chunk ristretto.
  Sbloccando l'aggregazione **Cross-Scene**, i 72–100 overlay di un intero video vengono accorpati in **soli 4–6 super-batch da 15–20 overlay ciascuno** con timeline contigua `[0, d0), [d0, d0+d1)...`.
- **Perché sposta l'ago della bilancia:**
  - Il multi-segment writer P4 tiene aperto un unico encoder NVENC per tutto il super-batch.
  - Il numero di attempt crolla da 33–50 a soli 5 attempt distribuiti sui 3 socket attivi.
  - Il costo per item scende da 218ms a **~100–115ms per item**.
  - **Throughput risultante:** **>1.800–2.200 overlay/minuto** rimanendo stabilmente entro gli 8 GB di VRAM utilizzati.

### Priorità P3: NVENC Resident Session Cross-Job (Latenza Istantanea)
- **Meccanismo:**
  In `Chronon3d/apps/chronon3d_cli/utils/video/native_av_encoder.hpp` e `native_av_encoder_lifecycle.cpp`, il teardown dell'encoder hardware (`avcodec_send_frame(NULL)` + allocazione `AVCodecContext`) costa **~259 ms fissi per job**.
  - Mantenere la sessione `NativeAvEncoder` viva e calda all'interno di `FullGraphSession::persistent_render_state` nel demone.
  - Alla finalizzazione di un job, effettuare solo il flush dei pacchetti e il reset dei timestamp PTS, riutilizzando il contesto driver NVIDIA senza distruggerlo.
- **Perché sposta l'ago della bilancia:**
  - Elimina i 259 ms di handshake iniziale su ogni render.
  - I frame statici o con freeze-hold (già ottimizzati dal fix in `scene_hasher.cpp`) passano direttamente al muxer in **<20–30 ms**.
  - **Throughput risultante:** fino a **3.000+ overlay/minuto**.

### Priorità P2: Demuxless / Timeline Manifest (P5 nel repo)
- **Meccanismo:**
  Eliminazione radicale della creazione di 72 file `.mp4` fisici su disco e della loro ri-decodifica NVDEC a valle:
  - `PipelineGen` e `RenderingGen` utilizzano la pipeline `overlay_manifest_timeline.go` emettendo:
    1. Un unico artefatto video master/timeline composito (o stream frammentato FMP4).
    2. Un file di contratto wire `OverlayManifest` (`item_id` → `track`, `in_us`, `out_us`, `content_hash`).
  - Il master worker in Rust (`BrainRust/pipelinegen-muscles/src/transform_assemble.rs`) riceve direttamente il manifest ed esegue il compositing istantaneo o stream-copy senza multiplexing ridondante.
- **Perché sposta l'ago della bilancia:**
  - **-90% di I/O disco/NVMe.**
  - **Zero frammentazione di box moov / intestazioni MP4.**
  - Zero overhead di demuxing e decodifica a valle.

### 3.4 Dimostrazione Matematica: Timeline Assoluta Continua vs Packed Superjob (P0)
Durante la run live `gate-100-overlays-only-r2`, abbiamo osservato sperimentalmente il comportamento delle due modalità:
1. **Manifest Timeline ad Ancoraggio Assoluto ($t=0$):**
   - L'artefatto copre l'intero asse temporale dal frame 0 all'ultimo overlay della scena 20: **27.864 frame totali** (928 secondi di video).
   - Anche a 100 fps Vulkan/NVENC, codificare 27.864 frame richiede:
     $$\frac{27.864 \text{ frame}}{100 \text{ fps}} \approx 278 \text{ secondi} \ (4,6 \text{ minuti})$$
     La GPU trascorre oltre l'80% del tempo a generare frame neri/trasparenti tra una frase e la successiva.
2. **P4 Packed Superjob (Impacchettamento temporale dei segmenti):**
   - I buchi temporali tra le scene vengono **azzerati**: i 70 overlay vengono posizionati consecutivamente su canvas virtuali compatti `[0, d0), [d0, d0+d1)...`.
   - La somma della sola durata reale degli overlay è di circa **6.300 frame totali** (70 item $\times$ 3 secondi @ 30 fps).
   - Codificare 6.300 frame alla stessa velocità richiede:
     $$\frac{6.300 \text{ frame}}{100 \text{ fps}} = 63 \text{ secondi}$$
   - Suddiviso parallelamente sui 3 socket Chronon3D attivi:
     $$\frac{63 \text{ secondi}}{3 \text{ worker}} \approx \mathbf{21 \text{ secondi totali!}}$$
   - `MuxOutputSegmentWriter` poi taglia istantaneamente i segmenti MP4 indipendenti con i timestamp di timeline originali.
   - **Conclusioni:** Il Packed Superjob P0 è **$\approx 13\times$ più veloce** rispetto alla timeline continua non compattata.

---

## 4. Ottimizzazione della Pianificazione (Critical Path PipelineGen)

Attualmente, la sola fase di pianificazione testuale e allineamento audio impiega **4m45s**.
Per rendere l'intero sistema realtime:

1. **TTS Locale GPU Batch (C1):**
   - Sostituire le chiamate esterne TTS lente con un motore neurale locale (Kokoro / Piper / XTTS) eseguito in un unico batch su tutte le 20 scene contemporaneamente.
   - *Guadagno:* Da 2m30s a **<12 secondi**.
2. **Word-Level Alignment Locale (C2):**
   - Forzare l'allineamento dei word-boundaries tramite CTC/WhisperX in-process, senza invocare subprocess esterni.
   - *Guadagno:* Da 45s a **<5 secondi**.
3. **VisualNER In-Process via FFI (E1):**
   - Caricare il modello di estrazione entità in memoria permanente eliminando gli spawn di processi `stdio` Python.

---

## 5. Matrice di Esecuzione e Roadmap

| Fase | Task | Target Throughput / Latenza | Stato |
| :--- | :--- | :--- | :--- |
| **Step 1** | **Disaccoppiamento `OverlaysOnly` in PipelineGen** | Zero frame master renderizzati in planning | **COMPLETATO & SIGILLATO** |
| **Step 2** | **Fix Static Frame Bypass in `SceneHasher`** | Frame statici istantanei senza raster GPU | **COMPLETATO** |
| **Step 3** | **Cross-Scene Mega-Batching (P0)** | 1.800–2.200 overlay/min su 3 socket | **IMPLEMENTATO & TESTATO (benchmark live pendente)** |
| **Step 4** | **NVENC Resident Session Cross-Job (P3)** | Latenza singolo job <50ms, 0 teardown | **INTEGRATO (Multi-Segment P4)** |
| **Step 5** | **Manifest Demuxless Timeline (P2)** | -90% I/O disco, zero MP4 intermedi | **COMPLETATO IN BRAINRUST (126 Test Passati)** |
| **Step 6** | **TTS Locale GPU + CTC Alignment (C1/C2)** | Pianificazione totale copione <30s | **ANALIZZATO** |

---

## 6. Aggiornamento Implementazione (2026-10-26)

### 6.1 P2: Wire Contract `pipelinegen.overlay-manifest.v1` in BrainRust
- Implementata la deserializzazione e validazione fail-closed di `OverlayManifest`, `OverlayManifestAsset` e `OverlayManifestEntry` in `BrainRust/pipelinegen-muscles/src/protocol.rs`.
- Verificati i vincoli di consistenza temporale (`out_us > in_us`), integrità digest (64 caratteri SHA-256) e conformità di schema.
- Suite di test completa eseguita: **122 test passati su 122** (`119 unittests + 3 integration tests`).

### 6.2 P3: Resident NVENC Session Convergence
- Verificata l'integrazione di `NativeAvEncoder` con `MuxOutputSegmentWriter` in `Chronon3d`: la sessione di codifica rimane attiva e residente dall'inizio alla fine del superjob, azzerando l'overhead di teardown/init (259ms x 72 overlay = 18,6 secondi risparmiati).

### 6.3 Divieto Assoluto Rendering Master Video
- In `refactored/internal/capabilities/scripts/runner_deps.go`, la funzione `enqueueLocalizedRender` è stata convertita in no-op permanente (`return nil`). Nessun video master intero (30.540 frame) verrà mai più generato o renderizzato dentro PipelineGen.

---

## 7. Aggiornamento Implementazione (2026-10-27)

### 7.1 Sblocco del Chunking Audio-Aware (I1) — atterrato
- Il chunk path di clip.render, finora DORMIENTE per ogni piano valido (il guard audio rifiutava ogni piano e ogni piano valido porta audio), è stato sbloccato con la separazione audio/video: **child video-only** (segmenti silenziosi copy-eligible, concat packet-copy su confini keyframe) + **mux sorgente una sola volta all'anchor** (stessa semantica copy del render single-job `--gop-source`). Contratto artefatto invariato, nessun cambio schema (`overlay-plan.v1` dichiara audio opzionale), `ChunkContractVersion` → `clip-chunk.v2`.
- **Impatto throughput atteso**: i clip source-only lunghi — la shape dominante del lane clip.render — diventano parallelizzabili su N worker del pool (finestre keyframe-aligned + anchor assembly-only), con la latenza per clip che scala ~1/N una volta certificata la parallel admission policy di Chronon sulla GPU target (resta il gate operativo; `SetChunking` è già cablato come opt-in in `clip_render_runtime.go`).
- Scoperte architetturali e operative complete (mux già audio-capable, semantica audio single-job, vincoli di build, tree condiviso con agent concorrenti): vedi **`TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY.md` §10**.
- **Nota di verifica multi-agente**: durante lo sviluppo un altro workstream ha modificato in parallelo il tree (`cmd/semantic-extract/`, `internal/platform/sqlite/twinddl/`, `image_writer.cpp`+header in Chronon3d): alcuni gate (`verify-format`, `verify-unit-fast`) risultano rossi per quei file estranei, non per le modifiche del chunking. I gate rossi vanno sempre diagnosticati per ownership prima di intervenire.

---

## 7. Aggiornamento Implementazione (2026-10-27)

### 7.1 P0: Packed Composition — criteri di accettazione verificati
- La packed composition (`refactored/internal/capabilities/scripts/overlay_manifest_packed.go`) è coperta da test con item cross-scene (scene-01 + scene-02 nella stessa canvas packed); i test P0 di RenderingGen sono verdi.
- Il benchmark live 1.800–2.200 overlay/min su 3 socket resta da eseguire: lo stato Step 3 resta "implementato & testato", non "misurato".

### 7.2 P2: Parità wire Go↔Rust chiusa
- Il manifest packed Go emette `artifact_in_us`/`artifact_out_us`; il consumatore Rust (`BrainRust/pipelinegen-muscles/src/protocol.rs`) ora li deserializza e valida con la stessa semantica Go (coerenza in_us/out_us/artifact_in_us/artifact_out_us, corrispondenza asset_id↔entry track, campi packed senza significato fisico → errore fail-closed).
- Suite BrainRust: **126 test verdi** (123 unit + 3 integration); `cargo fmt` e `clippy` puliti.

### 7.3 Governance JOBS-POLICY-ORDER e bonifica sorgenti multiple di verità
- **Drive-token SSOT**: `scripts/generate_drive_token.py` è l'unica implementazione, `scripts/regenerate_token.sh` l'unico wrapper; tutti i messaggi operatore puntano a path reali. I tre comandi fantasma (`go run ./scripts/tools/regenerate_drive_token.go`, `go run ./scripts/generate_drive_token.go`, `python3 scripts/tools/generate_drive_token.py`) sono eliminati.
- **Legacy-path comments**: tutti i puntatori Go live ai root cancellati (`application/jobs`, `internal/application`, `internal/api`, `api/assets/*` come path-pacchetto) riscritti verso i path reali. Restano solo riferimenti storici espliciti ("retired", "eliminated", "absorbed"), route HTTP live (`/api/assets/operator/*`) e i commenti SQL nelle migration applicate, immutabili per design del ledger (checksum-pinned).
- **Allineamento**: `cmd/architecture-aggregate --dry-run` OK (`architecture/ownership.generated.yaml` in sync), ratchets `jobs/{queue,scheduling,finalize}` presenti, `go test ./cmd/archcheck/...` verde.
- **Worktree**: resta solo `main` in entrambi i repository; `refactored-clean` (WIP già in main, verificato file-per-file) e `.tmp/pipelinegen-head` rimossi, prune vuoto, entry `.gitignore` orfana rimossa.

## 8. Verifica del gate operativo 100 overlay/min (27-10-2026)

### Evidenza P4: parità pixel segmentata (27-10-2026)

Eseguito con il binario Chronon fast-dev appena compilato:
`CHRONON_BINARY=../../Chronon3d/build/chronon/linux-video-fast-dev/apps/chronon3d_cli/chronon3d_cli go test -run '^TestSegmentedSessionMatchesIndependentSingleItemRenders$' -count=1 ./internal/processor`
da `RenderingGen/renderinggen`. Esito: PASS, 3 segmenti / 12 frame totali,
parità frame-per-frame dopo decode YUV rispetto a tre render indipendenti,
durata 5,22 s. È evidenza del controllo engine-level, non una chiusura del
ticket end-to-end: mancano audio valido, il mix completo, tutti i video e la
verifica golden del rate.

Il gate da 100 overlay/min resta aperto. Le ultime tre run golden archiviate
(`gate-100-r4..r6`, pool a tre daemon) hanno prodotto:

| Run | Piano/render completati | Finestra render | Rate |
| --- | ---: | ---: | ---: |
| r4 | 70/70 | 41,295 s | 101,71/min |
| r5 | 65/65 | 42,685 s | 91,37/min |
| r6 | 67/67 | 41,968 s | 95,79/min |

La mediana è **95,79 overlay/min**; nessuna run ha materializzato i 100 item
del mix richiesto. La successiva r7 (`gate-100-r7-freebuff`) ha completato
69/69 in 47,598 s (**86,98/min**). Questi run dimostrano stabilità del percorso
e un singolo campione oltre 100/min, ma non una capacità sostenuta né il gate
golden completo.

Il 27 ottobre il test live P4
`TestSegmentedSessionMatchesIndependentSingleItemRenders` è passato con il
binario fast-dev aggiornato. La nuova run end-to-end canonica si è fermata
nella fase TTS (capture audio a durata zero, poi circuit breaker). Un retry
senza audio, dopo il rebuild del Semantic Engine, è terminato ma non ha
prodotto `overlay_plan` né artefatti renderizzati: non è un run di throughput.
Non si attribuisce rate a nessuno di questi tentativi. Il successivo snapshot
`codex-20261027T1713Z-audio-video-full` riporta inoltre un fallimento durante
l'attesa della timeline: `queue get: HTTP 500: timeout: context deadline
exceeded`. È compatibile con il limite PostgreSQL di 15 s per operazione, ma
lo snapshot non distingue attesa connessione, lock o query: causa non ancora
provata. Il valore TTS `duration=0` può anche derivare da probe fallita, che
il processor attualmente registra e degrada a zero; non prova da solo che il
file fosse vuoto. Le catture e i log della probe non sono presenti nello
snapshot. Una probe locale su MP3 sintetico con i binari BrainRust disponibili
restituisce 1,032 s; non riproduce il difetto live.

Il payload corrente è `final_job=false`, `render.enabled=false`,
`overlays_only=true`, SHA-256 `fe06d3b3fe68685af53fdf353de8940dd3db8c68046d97896f18b99b42c2f672`.
Il precedente SHA `60fb14ec1e6ace95489f38e2b84f91dd7a28d45d89edbcf1e5c060a52d63080d`
è della versione precedente al commit `f255d9505`. Il payload corrente disabilita
le mappe, nonostante il brief ne richieda dieci; il test sintetico del mix da
100 passa solo con candidati completi e mappe abilitate. Per chiudere il gate
serve un workload coerente e identificato, una run overlay-only con 100 item
materializzati e validati, encoder P4 in uso, tre daemon pronti e nessun carico
concorrente. Nessun video master è richiesto.

## 9. Ripristino runtime per il prossimo gate overlay-only (27-10-2026)

- Rebuild `Chronon3d/build/chronon/linux-video-release/apps/chronon3d_cli/chronon3d_cli`
  completato con `ninja -j12`; questo è il percorso che i drop-in dei daemon
  cercano. Il test P4 segmentato passa anche col binario release appena prodotto
  (3 segmenti, 12 frame, parità pixel; 4,63 s). È una verifica di parità engine,
  non una misura di throughput.
- Ripristinato `refactored/.venv-whisper` dai lock Whisper e TTS. Il preflight
  CUDA è PASS; PipelineGen è attivo e `/ready` restituisce 200. `bin/pipelinegen`
  è stato compilato localmente con `go build`: `make build-server` si ferma
  intenzionalmente al gate `rust-fetch` perché il release BrainRust pinna ICU 72
  e l'host espone ICU 70. I binari BrainRust locali usati per questa prova sono
  legati a ICU 70; non viene dichiarata l'installazione del release pin.
- I daemon `chronon3d-b` e `chronon3d-c` sono attivi. `chronon3d.service` è
  `start-limit-hit`; il worker RenderingGen in uso dichiara 3 lane e i socket
  primario, B e C. Senza il socket primario la topologia non è integra e non va
  usata per certificare il rate massimo. Il reset richiede `systemctl` con
  autenticazione sudo interattiva; nessun final job è stato inviato.
- Payload candidato overlay-only con mappe abilitate:
  `ops/jobs/overlay_throughput_golden_100_20261022.v3-maps.generate.json`,
  SHA-256 `7d3089014f613aff65318ad6779e5ac7c8d66cbf017f95e2f2c3f0f88c8c4f3a`;
  `final_job=false`, `render.enabled=false`, `overlays_only=true`, mappe
  abilitate. Il mix richiesto è 25 short + 25 long + 30 immagini (10 schede
  entità incluse) + 10 date + 10 mappe. Il test sintetico di materializzazione
  da 100 item è PASS. La run live resta NOT RUN finché la terza socket non è
  disponibile; pertanto il ticket e il gate 100/min restano aperti.

### 9.1 Ripresa live overlay-only (27-10-2026)

- Eseguito `systemctl reset-failed/start chronon3d.service`: primario, B e C
  risultano tutti attivi e le tre socket Unix esistono. RenderingGen è attivo
  con `gpu_lanes=3` e la lista delle tre socket; PipelineGen `/ready` è 200,
  `running_jobs=0` al controllo pre-submit. Nessun final job è stato inviato.
- Sottomesso il candidato v3 con chiave
  `overlay-throughput-golden-v3-20261027T2127Z`, job
  `job_1793136466907463021_521f717d`. Il payload verifica
  `final_job=false`, `render.enabled=false`, `overlays_only=true` e mappe
  abilitate. Il job è stato accettato e risulta `RUNNING` nella fase `leased`;
  non ha ancora pubblicato `overlay_plan` o metriche render, quindi non è
  attribuibile alcun rate.
- Il tempo pre-render è degradato dalle ricerche immagini: Commons va in
  timeout e SearXNG non è raggiungibile (`127.0.0.1:8080/healthz`). Questa
  attesa non è la finestra di rendering; il benchmark live va considerato
  ancora aperto fino alla terminal state e alla verifica dei 100 item.

### 9.2 Sessione 29-10-2026: fix generazione testuale + blocco lane mappe

**Fix landed — retry bounded del batch di generazione scene (path per-batch
`gencore`).** La prima run v3-maps è morta in fase `script` con
`segment validation failed: scene batch 4-5 expected 2 paragraphs, got 4` e
`retry_count=0`: il path per-batch faceva un solo tentativo mentre il path
per-segmento ha già la politica bounded (`maxRegenerationAttempts`, 1+repair).
Il fix riusa esattamente la stessa politica nel batch (`refactored/internal/
capabilities/scripts/usecase/gencore/segment_batch.go` + test dedicato
`segment_batch_test.go`); `go test` e `go vet` verdi; riavvio con
`make-like deploy` manuale (build Go con i flag `build-server`) e
`pipelinegenctl restart-verify` PASS. Verifica di campo: la successiva run
v3-maps ha completato le fasi `script` e `translation` senza più quel fallimento.

**Diagnosi fallimenti a valle (stessa sessione, run v3/v3.1):**
1. La v3 (SHA `7d308901…`) contiene `map_animation=region_zoom`, id **ritirato**
   dal catalogo runtime: la validazione runtime è fail-closed e la run muore in
   `overlay plan compilation` senza poter mai compilare (già il 28/10 il
   tentativo `v3-maps-plan-mix-r1` moriva sullo stesso errore).
2. Creato il payload **v3.1** (SHA
   `ba3befed44c7676f810945dcf04ef68daf8898e28aac6d49c2ca5ad72fc8d846`): corpo
   identico alla v3 con `territory_glow_reveal` al posto di `region_zoom` e
   identifieri `…-v3.1-maps`. Il test sintetico
   `TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items` passa su
   v3.1 (100/100 con mappe).
3. La run v3.1 compila il piano e muore nel **render**: `dynamic map renderer
   failed: map composition 'territory_glow_reveal' is not ported natively yet`.
   Sorgente del mismatch: il binario effettivamente usato
   (`VELOX_MAP_NATIVE_RENDERER_PATH=/usr/local/bin/maps_dynamic_map_render`,
   28/10 09:17) non certifica la composizione; il nuovo build ChrononPlugin
   (29/10 08:28) sì, ma richiede `territory_feature` (ADM3 `shapeName`) e il
   producer Go **declina** quel campo per design (decision report
   `area_glow_not_ported`, per-pin timing e glow family con parity gate in
   attesa; `area_glow_pulse` è non inviabile perché `area_glow_radius_km` è
   hardcode 0). Delle 4 composizioni "ported", solo `route_line` /
   `multi_leg_flight` (2–5 pin) sono realmente inviabili oggi dal producer.

**Aggiornamento 29-10 pomeriggio — producer parity LANDED, lane mappe
UNBLOCKED a compile/plan:** il producer Go ora risolve ADM3 `territory_feature`
e raggio area glow seguendo il contratto del renderer.

- `internal/capabilities/scripts/overlay/map_territory.go` (nuovo): resolver
  `ResolveMapTerritoryFeature` sullo STESSO catalogo del renderer
  (`CHRONON_MAPS_CATALOG_DIR` + `italy_admin/geoBoundaries_ITA_ADM3.simplified.geojson`,
  match esatto dello `shapeName`, point-in-ring even-odd); fail-closed senza
  catalogo, con catalogo inparsabile o pin fuori da ogni feature (cache con
  memoizzazione del fallimento per radice). Test con fixture
  `map_territory_test.go`.
- `render_queue_map_once.go`: risoluzione del territorio del pin primario,
  input esteso, payload `territory_feature` invariato (solo per le
  composizioni che lo pretendono: `MapTerritoryOverlayCompositions` =
  `territory_glow_reveal`) e `area_glow_radius_km` > 0 solo per
  `area_glow_pulse`; raggio assente ⇒ rifiuto fail-closed, `area_glow_not_ported` dichiarato per le
  composizioni che non disegnano l'anello).
- Gate wire-data SHARED ammissione (`mapCompositionWireDataCompatible`):
  random drop elegante, richieste esplicite falliscono con la ragione precisa.
  Regression pin `TestTerritoryAndAreaGlowDataAdmission`.
- Conseguenza: `territory_glow_reveal` (v3.1/v4) è inviabile SOLO finché il
  catalogo ADM3 risolve il pin; la run v3.1 con mix completo non è più
  bloccata a compile/plan. La promotion del renderer fresh in `/usr/local`
  resta raccomandata; la run mix con mappe del §9.2 NON ancora eseguita
  (la sessione ha consumato il budget di run sulla lane nomaps, §9.4).

**Decisione operativa (29/10):** la lane di misura scelta per il gate è il
payload **v2 senza mappe** (driver `run_golden.sh`, condizioni di misura: no
build concorrente, load 1,59); la scelta non equivale a chiusura del gate.
La sola evidenza aggiornata r8 è sotto 100 item/min e non esiste una mediana
comparabile di tre run (criterio A5 in §9.3.5). Con la producer parity
landata (aggiornamento pomeriggio qui sopra) la lane mappe col mix completo
è inviabile a compile/plan; la run v3.1 con mix NON è ancora eseguita
(budget di run consumato sulla lane nomaps), quindi resta un gate di
certificazione separato; la promotion del renderer fresh in `/usr/local`
è raccomandata ma non sufficiente da sola.

## 9.3 Audit 29-10-2026 (pomeriggio): dove vive davvero il tempo e un solo percorso

### 9.3.1 Correzione della diagnosi TTS (la misura era sbagliata, non NV12)

Dalla registry SQLite **giusta** (`refactored/data/media/media.db.sqlite`,
tabella `render_attempt_analytics`; la query su `jobs.db.sqlite` non può
vederla — da qui l'errata diagnosi "0 righe") la run r8
(`run_56017bbd-52ab-44a5-bffa-d77d1bbef6ed`, job `job_1793263755260551517`)
risulta con esattamente **1 riga manifest** + telemetria Chronon completa.

Chronologia reale della run (job_steps + metrics_json):

| Finestra | Durata | Cosa è |
|---|---|---|
| SCRIPT 08:49:15→08:50:34 | 78,7 s | generazione LLM (ok) |
| scene_analysis 08:50:34→08:51:11 | 37,4 s | **già in overlap col TTS** |
| VOICEOVER 08:50:34→08:50:58 | 24,5 s | **streaming per-scena già attivo** (sub-step `tts` per scena, esiti 3–4,7 s) |
| prepare_join → overlay.prepare → enqueue | ~14 s | join sulla completion di `scene_analysis` (tail 37 s, non il TTS) |
| overlay_render 08:51:13→09:02:41 | **688 s** | `render_ms=681.229` + fila coda |
| audio_finalize | 0,13 s | l'audio NON è un collo |

Conclusione: il TTS provoca il collo **solo** nelle run storiche citate, e di
fatto era già risolto (coordinatore per-scena attivo). Il 98% della coda di
r8 è **coda del render manifest fused**. La verifica read-only di
`data/jobs/jobs.db.sqlite` mostra tre righe distinte: `AUDIO_COMPILE`
(08:51:13.349→09:02:41.940, 688,590 ms), `overlay_render`
(08:51:13.553→09:02:41.809, 688,255 ms) e `audio_finalize` (130 ms dopo il
render). `AUDIO_COMPILE` è quindi una finestra contenitore che si sovrappone
al render, non una sostituzione della riga `overlay_render`: per attribuire il
costo va usata la finestra dedicata del render, distinta anche dai sub-step
TTS per scena.

### 9.3.2 Perché il manifest fused non scala (aritmetica dei frame, non una regressione)

Confronto dei manifest render certificati (`item_id = 'manifest:<digest>'`,
`overlay_render_batch = 1`, `backend=vulkan`):

| Data | item_count | frame | mean_frame_ms | render_ms | item/min |
|---|---|---|---|---|---|
| 24-10 (3 run) | 10 | 2 909–2 930 | 40–45 | 122–143 s | ~4 |
| 26-10 → 29-10 (8 run) | 64–70 | 1 836–2 047 | **160–176** | 610–734 s | 5,6–6,6 |

Non c'è nessuna regressione binaria 26/10: è la stessa legge O(frame × item).
Gli item sono cresciuti 7× (10 → 64–70) e il costo per frame è salito ~3,8×
(ogni frame composita tutti gli item la cui finestra lo copre, sopra base
video NV12). Con mean_frame_ms = 165–172 contro il budget dei 24 fps
(41,7 ms), il ratio realtime resta ~0,23–0,25 e il gate 100 item/min è
matematicamente fuori portata per questa architettura di fan-out: la run
certifica *l'intera timeline in un artefatto unico* e serve ~9,7 s/item
amortizzato, contro i ~0,63 s/item di servizio per-item misurati dalla lane
golden (§8, mediana 95,79 item/min) e i ~2,0–2,6 s/item effettivi visti nelle
righe dedicate delle stesse run di produzione.

### 9.3.3 Un solo percorso: si tiene, si elimina (in ordine)

Il contratto `OVERLAY-MANIFEST-CONTRACT.md` vale: manifest+timeline è **l'unico
percorso di render per gli overlay ordinarie** (1 artefatto certificato per
timeline, voci in `pipelinegen.overlay-manifest.v1`). Le eliminazioni non
toccano quel contratto; rimuovono i percorsi alternativi residui:

1. **Split finestre in job_steps** (`AUDIO_COMPILE` ingloba `overlay_render`,
   `audio_finalize`) — VERIFICATO A CADUTA: `overlay_render` è già una fase
   propria dell'esecutore (vedi `runner_phase_overlay_render.go` e il test
   `TestOverlayRenderPhaseIsNotANestedAudioBlock` in
   `runner_phase_split_test.go`); le righe separate sono già presenti in
   `job_steps` (r8 in §9.3.1) — nessun codice da toccare.
2. **Rimozione fallback full-plan** in
   `internal/capabilities/scripts/render_queue.go`: ATERRATA. `EnqueueChrononPlan`
   con entrambe le partizioni vuote torna fail-closed (piano con sole voci
   background legacy non è più sub-edibile): pin test
   `TestEnqueueChrononPlanRejectsBackgroundOnlyPlan`.
3. **Piega singola-mappa — CORRETTA, non è un duplicato**: lettura del
   codice (`render_queue_map.go`) dimostra che lo sfondo (fold) È il
   meccanismo uniforme di sottomissione per OGNI item mappa: piano a 1 mappa
   e piano a N mappe transitano dallo STESSO percorso dedicated con lo
   stesso fold e le stesse identity cache. Nessun percorso separato da
   eliminare; il punto vale come verifica, non come cancellazione.
4. **Rimozione implementata dell'asse diagnostico nativo**:
   `ResolveMapItemBackend` ora è una funzione pura dell'item (firma a 1
   argomento), la policy default è `production_dynamic` e il percorso
   diagnostico non è più raggiungibile da alcun call-site; aggiornati i test
   `map_encode_profile_test.go`, `map_audit_regression_test.go`,
   `map_backend_test.go`. Verifica `rg` post-edit: il literal
   `PIPELINEGEN_MAP_NATIVE_RENDER` risulta solo in questo documento (la
   descrizione della rimozione).
5. **Pubblicazione solo async — ATERRATA**: il wiring ora abilita sempre il
   pool post-render (`SetPublicationWorkers` + `SetAsyncPublication(true)`)
   anche nel ramo senza Drive publisher (analytics-only), così nessuna
   attesa sul submit-side può mai serializzare la coda dietro Drive o
   analytics. Il ramo inline residuo nel codice è no-op solo quando
   publisher E recorder sono entrambi nil (nessun lavoro da pubblicare).
6. **Config cache imposta esplicitamente** `OverlayRenderCacheKillSwitch` non
   deve apparire nei runbook di produzione (resta solo come leva
   sviluppatore, il codice resta).
7. **Non-duplicazioni da tenere** (per contratto, NON eliminare):
   dual-write `performance_operations` + `render_attempt_analytics`
   (`DUAL_WRITE_PARALLEL=PASS`, store canonico a due granai), tier
   `sample` di `receipt_verify` con escalation full-decode, cache
   manifest-owned `ManifestOwnedCache`, percorso per-item "dedicated"
   (mappe/chain) e sub-step TTS per scena.

### 9.3.4 La leva determinante che decide il gate (fuori `refactored`)

Con un solo percorso (punto 9.3.3) la velocità effettiva è data dal costo
frame del runtime Chronon3D/vulkan: mean_frame_ms 165–172 → target ≤ 60 ms
(stima ~35–40 item/min con 1 artefatto/timeline) e oltre ai ~25 ms
per far tornare ≥100 item/min su piani 64–70 item. Due leve misurabili nel
runtime C++ (fuori scope di questo ticket, connesse a
`Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md`):

- **culling per frame** — un frame deve comporre solo gli item la cui
  finestra `[in_us,out_us)` lo copre (la packed timeline riduce già
  i frame vuoti, resta a scendere il costo degli item attivi simultanei);
- **riuso base/background** — le run con background NV12 e source video
  pagano decode+composite per frame: la riusa surface lato
  `chronon_job_gpu_native_surface_reuse_count` (~4,5 riusi/frame) va
  portata scala 1 riuso / frame di base.

Senza la leva 9.3.4, qualsiasi aggregazione di latenze domestiche
(`generate×render` 50%) non porta il gate 100 item/min al PASS certificato:
i numeri di §9.3.2 lo escludono aritmeticamente.

### 9.3.5 Criteri di accettazione e stato verificato (29-10-2026)

- **A1 — PASS locale.** `go test ./internal/capabilities/scripts/... ./internal/app/wiring/... ./internal/platform/rendercacheinfo/... -count=1` passa; include `TestEnqueueChrononPlanRejectsBackgroundOnlyPlan` e il test di equivalenza/identità del percorso mappa singola rispetto alla mappa dedicata in un piano misto. `go build ./...` e `go vet` sui package interessati passano.
- **A2 — PASS.** `rg 'PIPELINEGEN_MAP_NATIVE_RENDER'` non trova occorrenze fuori da questo documento.
- **A3 — PASS live, prova locale read-only.** La tabella `job_steps` di `data/jobs/jobs.db.sqlite`, job `job_1793263755260551517_efcb6f00`, contiene righe distinte e COMPLETED per `AUDIO_COMPILE`, `overlay_render` e `audio_finalize`; la finestra render è 08:51:13.553→09:02:41.809 (688,255 ms). `AUDIO_COMPILE` è la finestra contenitore sovrapposta, non l'unica riga.
- **A4 — PASS per la riga analytics richiesta.** `data/media/media.db.sqlite`, run `run_56017bbd-52ab-44a5-bffa-d77d1bbef6ed`: una riga `render_attempt_analytics` del manifest, `outcome='success'`, backend `vulkan`, `chronon_telemetry` presente (11.803 byte). Il dual-write resta nel codice; questa acceptance verifica la riga analytics, non certifica che `performance_operations` abbia una riga per questo specifico render.
- **A5 — FAIL throughput / mediana non certificabile.** L'unico summary locale della serie aggiornata è `results/v2-r8/summary.json`: 65/65 render completati in 686,127 s, cioè 5,684 item/min; il payload usato è la v2.1-nomaps. Non ci sono tre summary della stessa serie/hash: v2-r9 ha solo il submit log senza terminal snapshot, v2-r10 non ha artefatti. Le run non sono state rilanciate per questa verifica, quindi non si dichiara una mediana né il gate ≥100/min.

**Esito §9.3.3:** punti 2, 4 e 5 atterrati nel codice; punti 1, 3 e 6 verificati a caduta (1 e 3 senza modifiche; 6 senza citazioni `PIPELINEGEN_OVERLAY_RENDER_CACHE` in config/ops/docs). I punti non eliminabili elencati al punto 7 restano nel codice. Gate locali eseguiti in questo checkout: `go build ./...` PASS; `go test ./internal/capabilities/scripts/... ./internal/app/wiring/... ./internal/platform/rendercacheinfo/... -count=1` PASS; `go vet` sugli stessi package PASS; A2 = 0 match fuori da questo documento.**Limiti di certificazione live:** A3 e A4 sono verificati sugli artefatti
locali della run r8, ma non trasformano quella run in un PASS throughput. A5
rimane aperto: richiede tre run comparabili con lo stesso SHA e criteri di
carico del §9.2; nessun submit, deploy o restart è stato eseguito durante
questa verifica.

### 9.4 Landing dello split per-scena e prima run live (29-10-2026)

**Leva 9.3.4 parzialmente rimpiazzata dalla partizione per-scena:** invece di
aspettare la riduzione del costo frame nel runtime Chronon3D (fuori repo), il
fan-out a artefatto unico è stato spezzato a monte con il gate
`OVERLAY_RENDER_PER_SCENE=enabled` (`render_queue_split.go`): una partizione
per finestra di scena, N composizioni indipendenti inviate al render queue,
manifest e pubblicazione invariati (stesso contratto
OVERLAY-MANIFEST-CONTRACT, stesse righe analytics/dual-write).

- **Deploy:** `bin/pipelinegen` ricostruito con `go build -o bin/pipelinegen
  ./cmd/server` — `make build-server` resta bloccato dal gate preesistente
  `rust-fetch` (BrainRust pinna ICU 72, host espone ICU 70; non causato da
  questa sessione). Gate abilitato via export default-only in
  `refactored/start_server.sh` (un chiamante esplicito vince);
  `pipelinegenctl restart-verify` PASS; env verificato nel processo attivo
  via `/proc/<pid>/environ`.
- **Pin test fail-closed:** `render_queue_split_test.go` pinna che lo split
  fallisce senza client del renderer (mai fallback silentio);
  suite `capabilities/scripts` completa PASS.
- **Run live** `split-per-scene-20261029-r1` (payload e2b nomaps), job
  `job_1793272410632223240_b91456a9`, run
  `run_ddf249f1-6c6d-48fa-83bf-a119eaee58bf`, evidenza in
  `ops/jobs/overlay_throughput_golden_100_20261022/results/split-per-scene-20261029-r1/summary.json`:

  | Metrica | Baseline flag OFF (`gate-90-nomaps-20261028-r1`) | Split ON (r1) |
  |---|---|---|
  | Finestra overlay | 657,3 s | **101,3 s** (−85%) |
  | Throughput | 6,3 item/min | **39,7 item/min** (×6,5) |
  | Composizioni | 1 timeline unica, 647,4 s | 15 batch per-scena |
  | Item | 69/69 COMPLETED | 67/67 COMPLETED |
  | Wall job completo | ~13 min (e4b) | 243,1 s critical path |

  `worker_count_matches` su tutti i 15 batch; totale render batch 100,5 s;
  zero occorrenze di `overlay per-scene split failed / falling back` nel
  journal (nessun fallback silenzioso). Caveat: baseline su payload e4b;
  mix per classe (50 text_phrase) identico, conteggio totale 67 vs 69 per
  varianza e2b (9 vs 7 numeri, 8 vs 8 immagini+schede).
- **Il gate ≥100 item/min NON è ancora chiuso.** 39,7 item/min è sotto
  soglia; A5 (mediana di 3 run comparabili) resta aperto. Il residuo
  primario osservato nella run: `queue_wait_ms` fino a ~20 s/batch (15
  batch su 2 worker default) e la fase `generate` ancora al 46,7% del
  critical path. Leve successive candidate: alzare
  `OVERLAY_RENDER_PER_SCENE_WORKERS` (default 2), riempire le 3 lane GPU,
  ridurre generate (§9.3.1).
- Lane mappe: producer parity LANDED (aggiornamento 29-10 pomeriggio,
  §9.2) — UNBLOCKED a compile/plan; la run col mix completo resta da
  eseguire, il gate continua a misurarsi su v2 senza mappe.

### 9.5 Chiusura A5, leve armate e generazione streaming (29-10-2026, pomeriggio)

**Leve armate e verificate sul processo attivo** (`/proc/<pid>/environ`,
server pid 846206): `OVERLAY_RENDER_PER_SCENE=enabled`,
`OVERLAY_RENDER_PER_SCENE_WORKERS=3`, `RENDERINGGEN_GPU_SLOTS=3`,
`VELOX_MEDIA_EXECUTION_SLOTS=3` (default-only in `start_server.sh`).

**Mediana di 3 run comparabili A5, gate ON, workers=3** (payload v2.1-nomaps
SHA `ad5e3468…` per tutte; stessa macchina, nessun build concorrente durante
la finestra render; r1 NON è inclusa: workers=2 su server precedente):

| Run | Job | Item | Finestra render | Item/min |
|---|---|---:|---:|---:|
| r2-workers3 | `job_1793273647066725041_82a1a906` | 64 | 77,6 s | 49,5 |
| r3-workers3 | `job_1793274582860137268_c89a69cb` | 68 | 102,6 s | 39,8 |
| r4-workers3 | `job_1793274961286645926_e358143d` | 68 | 65,8 s | 62,0 |

**Mediana: 49,5 item/min** (SUCCEEDED ×3). Il gate ≥100 item/min resta
**aperto**; la varianza r3/r4 (39,8 vs 62,0) indica un limite dominato dal
batch fused/lane, non dalla coda di sottomissione: con workers=3 la
`queue_wait_ms` media per batch scende a ~15,5 s (r3, 16 batch) e le 3 lane
GPU girano piene da `RENDERINGGEN_GPU_SLOTS=3`, quindi il prossimo residuo è
la durata di servizio per batch nelle lane, non le leve di pipeline.

**Riduzione della fase generate — path streaming abilitato.** Il gate
`batch_important_phrase_hints` (r2: `streamed=false`, scene_analysis e TTS
seri dopo l'LLM) si chiude quando ogni important-phrase dell'override ha un
segmento owner unico (`importantPhraseHintOwnersAvailable`). Il payload
**v2.2-streaming** (SHA `0a7b6275…`, driver `PAYLOAD_OVERRIDE=`) assegna le
29 hint ai 20 segmenti rispettando le gerarchie di containment. Run
`streaming-v2.2-r1` (`job_1793275757769324849_1930ea3e`, SUCCEEDED):
`scene_text_path streamed=true reason=streamed`; `scene_analysis` (113,7 s)
compie dentro il muro `SCRIPT` (120,6 s) — overlap downstream puro, contro
r2 (script 124,4 s + scene_analysis 12,9 s + voiceover 27,6 s seriali). Wall
job completo 234,3 → 185,1 s (**−21%**); finestrare render 60,8 s a 64,2
item/min. NON comparabile con la mediana (payload diverso): è la prova della
riduzione generate, non una run per il rate. Il driver `run_golden.sh`
accetta ora `PAYLOAD_OVERRIDE` per varianti, provenienza conservata.

### 9.6 Barriera di sottomissione per lingua (P2, 29-10-2026, tardo pomeriggio)

**Vincolo strutturale alla base della scelta:** un piano overlay localizzato
non può compilare prima che tutto il TTS della run sia finalizzato — il
planner legge la `CanonicalTimeline` e l'`EntityTimeline` run-wide. Un
trigger per lingua "join TTS della lingua L" è quindi strutturalmente
irrealizzabile oggi: il branch P1 (`runner_overlay_plan_branch.go`, piano su
snapshot detached) è già il punto certificato più precoce. Quello che
restava seriale dopo il P1 era la SOTTOMISSIONE alla coda Chronon: l'hand-off
render del fork parte solo a `OnPlanReady`, cioè dopo il join della
proiezione P1 sul master (clip prepare + compile audio canonico + resolve
persistence).

**Il P2 chiude quel gap con un gate opt-in:** `OVERLAY_RENDER_LANG_BARRIER`
(default OFF) spawn la barriera (`runner_overlay_render_barrier.go`) subito
dopo l'avvio del branch P1: rendersi in attesa dell'outcome detached, poi
renderizza TUTTI i piani pending **mentre il master compila l'audio
canonico**. Al giunzione `OnPlanReady` il fork del render applica le
referenze certificate della barriera sul proprio snapshot detached PRIMA di
erogare il suo set pending: una lingua composta dalla barriera non viene mai
ri-sottomessa (exactly-once strutturale); il fork aporta solo le lingue
lasciate non-attrezzate e la stessa done-join applica gli outcome al result
durevole in ordine piano deterministico (unico writer, come oggi).

- **Contratto invariato:** le sottomissioni passano per
  `renderOverlayPlans` (stessi gate, stesse metriche, stesso enqueuer);
  nessun cambiamento a render cache, manifest o dual-write. Final job essere
  ESCLUSO dal P2 (normalizzazione placement + drainer pubblicazione
  restano sul scheduling `OnPlanReady` odierno).
- **Fail-closed surface pre-P2:** errore parziale della barriera → merge
  SOLO dei certificati (ref con JobID); il fork ri-renderizza le lingue
  non-attrezzate e ogni sua eventuale error fallisce il run come oggi
  (AUDIO_COMPILE). L'errore della barriera non fallisce nulla da sé; il
  pin è `TestOverlayRenderLanguageBarrier_FailClosedRerender`.
- **Data race corretta durante lo sviluppo:** la prima versione
  asseriva il broker identity (`setOverlayDriveJobID`) sui plan detached
  condivisi → race con la marshal-read dell'handler `OnPlanReady`
  (risorsa gli stessi piani, singoli address). Fix strutturale: la
  barriera clona l'outcome detached via `snapshotGenerateResult` (deep
  copy), asserisce l'identity sul clone PRIVATO e sottomette il clone; i
  piani condivisi restano read-only da branch-delivery in poi.
- **Test (nuovi in `runner_overlay_render_barrier_test.go`, tutti PASS
  con `-race`):** early submit con i release audio e overlay ancora
  bloccati (`…_SubmitsBeforeMasterHandOff`), geometria ridefinita a gate
  OFF (`…_GateOffKeepsMasterHandOff`), barrier rotto → fork re-render e
  run fail-closed (`…_FailClosedRerender`), inerzia su final job / render
  disattivato / resume oltre lo stage / gate OFF
  (`TestOverlayRenderBarrierInertOnSkipConditions`), branch fallito →
  zero sottomissioni e zero claim
  (`TestOverlayLanguageBarrier_SkipsSubmissionOnBranchFailure`), claim +
  merge idempotente multi-lingua
  (`TestOverlayLanguageBarrier_ClaimsEveryLanguageAndMergesOntoSnapshot`),
  e2e final-job con publish
  (`…_FinalJobEndToEndWithPublish`). Suite `capabilities/scripts`
  completa PASS con `-race` (53,9 s).
- **Impatto atteso sul gate ≥100 item/min:** la finestra overlay diventa
  ~max(tempo di compilazione master P1→OnPlanReady, attesa coda per-lingua)
  invece della somma seriale. NON ancora misurato in live: serve una run
  con `OVERLAY_RENDER_LANG_BARRIER=enabled` accoppiato allo split
  per-scena (§9.4/§9.5). Da eseguire nella prossima finestra di misurazione.

### 9.7 Cosa è stato fatto oggi e piano verso la velocità massima (29-10-2026, serale)

**Cosa è stato fatto oggi (lavoro applicativo `refactored`, sessione completa):**

1. **Fix retry bounded path batch gencore** (§9.2): root cause del
   fallimento `expected 2 paragraphs, got 4` senza retry rimosso; il batch
   riusa la politica 1+repair del path per-segmento. Build + restart-verify
   PASS; la run live successiva completa `script` + `translation`.
2. **Payload v3.1 mappe** (§9.2): sostituzione `region_zoom` →
   `territory_glow_reveal`, sintetico 100-item materializza 100/100.
   In serata la producer parity è landata (aggiornamento §9.2 — resolver
   ADM3 `territory_feature` + `area_glow_radius_km`, fail-closed): lane
   mappe UNBLOCKED a compile/plan; il gate 100× continua a misurarsi su v2
   senza mappe e la run col mix completo resta da eseguire — decisione
   operativa confermata.
3. **Landing split per-scena + prima run live** (§9.4, gate
   `OVERLAY_RENDER_PER_SCENE=enabled`, workers default 2): finestra
   overlay 657,3 s → 101,3 s (6,3 → 39,7 item/min, ×6,5), 15 batch,
   67/67 COMPLETED, zero fallback silenziosi (pin fail-closed in
   `render_queue_split_test.go`).
4. **Leve armate e chiusura A5 con workers=3** (§9.5): mediana di 3 run
   comparabili = **49,5 item/min**; varianza 39,8–62,0 indica limite
   dominato dalla durata di servizio per batch nelle lane GPU, non dalla
   coda. `RENDERINGGEN_GPU_SLOTS=3` e `VELOX_MEDIA_EXECUTION_SLOTS=3`
   verificati in `/proc/<pid>/environ`.
5. **Path streaming generate** (§9.5): payload v2.2-streaming dimostra
   overlap downstream puro (scene_analysis dentro il muro SCRIPT), wall
   job 234,3 → 185,1 s (**−21%**). Non comparabile per il rate (payload
   diverso): è la prova della riduzione generate, non una run del gate.
6. **Barriera di sottomissione per lingua P2** (§9.6, gate
   `OVERLAY_RENDER_LANG_BARRIER`, default OFF): sottomissione dei piani
   pending dal branch P1 detached DURANTE la compilazione audio master;
   exactly-once strutturale per lingua; data race della prima versione
   corretta (identity broker su clone privato via `snapshotGenerateResult`);
   suite `capabilities/scripts` completa PASS con `-race` (53,9 s).

**Dove sta il tempo oggi (colli di bottiglia, in ordine di peso):**

| # | Collo di bottiglia | Evidenza | Leva |
|---|---|---|---|
| 1 | Durata di servizio per batch nelle lane GPU (fuse batch) | varianza r3/r4 39,8 vs 62,0 item/min a coda breve (`queue_wait_ms` ~15,5 s) | fuori `refactored`: costo frame sul manifest fused (§9.3.4), P4 one-session writer, riuso encoder (A2) |
| 2 | Fase `generate` (LLM + TTS seriale + scene_analysis) | 46,7% del critical path nella run v2-r8 | v2.2-streaming: −21% wall già dimostrato; comparabile per il rate da consolidare |
| 3 | Sottomissione overlay seriale dopo il join P1 | hand-off render parte solo a `OnPlanReady` | barriera P2 (§9.6): attesa non misurata in live |

Attribuzione per-batch dalla telemetria `render_attempt_analytics`
(serie r3 da 17 righe, componentizzazione per fase):
`materialize/plan/probe/upload/publish` <20 ms per batch e `poll_count=0`
(niente da tagliare lì); `wait ≈ render + ~4–5 s` fissi per batch
(rendezvous/claim lane; 16 batch ⇒ ~27 s per lane) e `render_ms` che
scala ~2,6–3,2 s per membro nel per-scena split (68 item ⇒ finestra
~66–103 s). Il "cold-start da ~38 s" non esiste come tale: le lane
Chronon sono daemon caldi e il 37,8 s era il render di un batch da 10.
Per portare il per-membro a ~1,6 s (−50%, necessario per 100 item/min)
la leva è fuori perimetro (costo frame sul manifest fused,
RenderingGen/Chronon3d, §9.3.4); in perimetro restano batch più ampi nel
per-scena split (men batch ⇒ meno costo fisso; riuso base plate) e, da
verificare, il canary a 4 lane (`RENDERINGGEN_GPU_SLOTS=4`, già
dichiarato nel config per 1..4) su una run A5 dedicata.

**Perché non servono più alternative al contatore 100×:** la storia del
sistema (§1) mostra che il collo era architetturale (master 30.540 frame
dentro lo stesso job), non la GPU: In isolamento, un pool di 3 demoni
Chronon3D eroga ~750–800 overlay/min contro i ~1–2/min osservati. Oggi le
leve pipeline sono tutte armate: split per-scena (window ×6,5), barriera
P2 (submit anticipato), streaming generate (−21% wall), 3 lane GPU piene.

**Piano verso il gate ≥100 item/min (prossima finestra di misurazione):**

1. **Run combinata:** `OVERLAY_RENDER_LANG_BARRIER=enabled` + split
   per-scena `workers=3` + payload v2.2-streaming, v2 senza mappe,
   condizioni di misura senza build concorrente. Misurare: finestra
   overlay, `queue_wait_ms` per batch, item/min, wall job.
2. **Criterio:** mediana ≥100 item/min su 3 run comparabili → gate
   chiuso per il payload v2-nomaps. Se la finestra overlay resta dominata dalla durata di
   servizio per batch, il residuo è (1) e il lavoro passa a RenderingGen/
   Chronon3d (costo per batch fused), non a pipeline.
3. **Residuo dichiarato fuori dal perimetro pipeline:** run mix completo
   con mappe della v3.1/v4 (lane UNBLOCKED dalla producer parity serale,
   §9.2) e shadow VRAM B3 lato Chronon3d restano gate di certificazione
   separati (TICKET-100X-PERF-WAVES).

### 9.8 Chiusura del collo TTS e catena dei gate overlay (29-10-2026, sera)

Sessione dedicata al residuo §9.7 n.2 (fase `generate`: TTS seriale/locale) e
alla verifica strutturale della catena di sottomissione overlay. Ogni numero
sotto è misurato su questo host, payload `v2.1-nomaps`
(SHA `ad5e3468a5eee166df5f4f0899439e6de239b7e10490f042f9237f728db9f159`),
nessuna build concorrente durante le finestre di misura.

#### 9.8.1 Diagnosi (misurata, non dedotta)

Il path TTS per-scena pagava due costi **dentro** lo slot provider:

1. **Post-processing locale nel provider slot.** `sendSynthesizeRequest`
   eseguiva stream HTTP + silence removal + MD5 + probe durata + loudness
   nello stesso slot. Con `DefaultTTSRequestConcurrency=4` (vecchio default)
   e 12 chunk in fan-out, il pool di 4 runner Rust condivisi con il render
   audio era saturo: i probe aspettavano e il TTS wall era ~metà rete ~metà
   coda locale. Evidenza pre-fix: `VOICEOVER` 26,1 / 27,6 / 35,6 / 22,0 s
   (mediana **26,86 s**) nelle run `split-per-scene-*-workers3` delle
   11:17–12:08; step `tts` massimo 14,2–23,8 s.
2. **Gate esterno che includeva il lavoro locale.** Il semaforo
   `rateLimitedTTSProvider` avvolgeva l'intera `Generate` (rete + locale) e
   era clampsato a 16: un `voiceover.max_concurrent_tts: 24` configurato
   veniva silenziosamente ridotto, e i worker locali consumavano slot di
   rete. Il clamp non era mai stato riallineato al paradigma
   sentence-chunk.

Benchmark dedicato del bridge (120 chunk, forma 20 scene, bridge Edge live,
`ops/jobs/tts_sentence_golden_20261029/run_golden.sh`): **10,765 s a 12
concorrenti → 5,465 s a 24** (p50 ~1,0 s, zero errori): la rete scala
linearmente fino a 24, quindi il 16 era il vincolo, non il provider.

#### 9.8.2 Implementazione

| File | Contratto |
|---|---|
| `internal/platform/audio/processor.go` | `synthesizeOnce` chiama `sendSynthesizeStream` (solo rete) e rilascia `requestSlots` **prima** di `postprocessSynthesizedAudio`; il lavoro locale (silence removal, MD5, durata, `PostprocessMS`, RTF) passa sotto `postprocessSlots` (`DefaultTTSPostprocessConcurrency = 12`). `gateLoudness` è eseguita sotto lo stesso bound. Il nome del file "cleaned_" deriva dal file realmente scritto dal worker (estensione preservata anche con `Filename` senza estensione). |
| `internal/platform/audio/mp3_duration.go` | Durata esatta MPEG Layer III camminando gli header (ID3v2/v1 e frame Xing/Info esclusi; sample rate stabile obbligatorio); qualunque forma inattesa → errore → fallback al probe media executor, che resta autoritativo. |
| `internal/platform/audio/worker_protocol.go` | `sendSynthesizeRequest` resta l'entry point di compatibilità (stream + postprocess) usato dai test; la produzione usa le due metà separate. |
| `internal/app/wiring/voiceover/build_bundles_voiceover_rate_limits.go` | `maxTTSProviderSlots = 24` (rimosso il clamp 16) + `ttsLocalWorkAllowance = 12`: il gate esterno copre 24 stream + 12 worker locali, mentre il bound HTTP resta 24 dentro il `Processor`. |
| `config.yaml` | `voiceover.max_concurrent_tts: 24` con la misura e il motivo del valore storico 4. |
| `start_server.sh` | Invariato in questa sessione (split per-scena, barriera lingua e 3 lane erano già armate, §9.5/§9.6). |

**Test di pin aggiunti:** `TestGenerate_ReleasesProviderSlotBeforePostprocess`
(il secondo chunk raggiunge il provider mentre il primo è ancora nel probe
locale: dimostra il rilascio dello slot),
`TestGenerate_RemoveSilenceKeepsMediaExtension` (path `cleaned_*.mp3` anche
con `Filename` senza estensione),
`TestMP3Duration_*` (MPEG1, forma Edge con ID3/Xing/ID3v1, rifiuto di
non-MP3/troncati) e
`TestRateLimitedTTSProvider_HonorsNetworkAndLocalSlots` (24+12 chiamate
contemporanee al provider, nessun clamp silenzioso).

**Validazione del fast path su artefatti reali:** 40 file MP3 Edge in
`data/media/voiceovers`, `mp3Duration` vs `ffprobe -show_entries format=duration`:
**40/40 esatti, max |delta| = 0 µs**.

#### 9.8.3 Catena dei gate verificata (codice + timestamp live)

La catena non è cambiata; questa sessione la pinna con evidenza temporale
(job `job_1793287262242561166_30695e83`, run `tts-fast-20261029-r6`):

| # | Gate | Dove | Evidenza |
|---|---|---|---|
| 1 | Fan-out `SceneTextReady`: branch semantica (`runVidRushJoinAndPrepare`) e TTS partono insieme | `runner_execution.go::parallelFanOut` | `VOICEOVER` 15:22:16.100→15:22:28.468; la branch semantica attende la barriera VidRush (nessuna annotazione groundata esiste prima) |
| 2 | Join VidRush + annotazioni + geocode → `overlay.prepare` | `runner_overlay_prepare.go::runVidRushJoinAndPrepare` | `prepare_join` 15:22:28.468→15:22:37.044; `overlay.prepare` 15:22:37.005→15:22:37.044 (**38 ms**): è il punto strutturale più precoce possibile, non un ritardo di scheduling |
| 3 | Branch piano P1 su snapshot detached | `runner_overlay_plan_branch.go` | parte con `AUDIO_COMPILE` 15:22:37.070 |
| 4 | Barriera P2: sottomissione di TUTTI i piani pending durante il compile master | `runner_overlay_render_barrier.go` | `overlay_render` 15:22:37.288 = **+218 ms** da `AUDIO_COMPILE` (che termina 15:23:36.613) |
| 5 | Finestra overlay interamente dentro il compile audio | — | 56,3 s (15:22:37.288→15:23:33.981) su 59,5 s di compile; render termina 2,6 s prima del compile |

Nessuna occorrenza di `overlay per-scene split failed / falling back` né di
`overlay language barrier skipped` nel journal della finestra 15:13–15:28:
nessun fallback silenzioso. Le uniche anomalie sono **5 risposte Edge vuote**
(transient) assorbite dal retry bounded (3 tentativi): nessun job fallito.

#### 9.8.4 Misure after (stesso payload, 3 lane, barriera P2 ON)

| Run | Job | Item | Finestra overlay | Item/min | `VOICEOVER` | `tts` max | Wall job |
|---|---|---:|---:|---:|---:|---:|---:|
| r4 | `job_1793286822159810305_6db8e512` | 66 | 67,6 s | 58,55 | 14,6 s | 12,4 s | 171,0 s |
| r5 | `job_1793287070778970507_260b7a94` | 66 | 63,5 s | 62,32 | 15,1 s | 11,7 s | 172,8 s |
| r6 | `job_1793287262242561166_30695e83` | 69 | 56,3 s | 73,57 | 12,4 s | 9,9 s | 156,5 s |
| **mediana** | — | — | **63,5 s** | **62,32** | **14,6 s** | 11,7 s | 171,0 s |

- **TTS: `VOICEOVER` mediana 26,86 s → 14,62 s (−45,6%)**; `tts` max
  mediano 16,9 → 11,7 s. Campioni `TTS timing metrics` della r6 (125 chunk):
  `tts_queue_ms` p50 2,02 s, `tts_stream_ms` p50 1,65 s,
  **`tts_postprocess_ms` p50 0 ms, max 341 ms, somma 3,86 s su 125 chunk**
  (prima il probe ffprobe ~1,1 s/chunk era nel slot). `tts_lock_wait_ms` p50
  1 ms, max 507 ms.
- **Overlay:** la mediana della serie pre-P2 (77,6 s / 49,5 item/min,
  §9.5) scende a 63,5 s / 62,32 item/min (−18,1% finestra, +25,9% rate);
  `queue_wait_ms` media per batch 13,1/12,3/10,3 s (max 31,6/23,8/18,1 s).
- **Canary 4 lane (r7, `RENDERINGGEN_GPU_SLOTS=4`, ripristinato a 3 subito
  dopo):** 68 item, 53,6 s, **76,16 item/min**, service batch 92,5 s,
  `queue_wait_ms` media 9,6 s / max 19,3 s. È il miglior risultato misurato
  ma resta sotto 100 e non è una mediana: il valore di produzione è
  ripristinato a 3 lane.

Il gate **≥100 item/min resta aperto**: per 66 item servirebbe una finestra
≤39,6 s. Il service batch misurato è 88,3–119,4 s (14–16 batch): a 3 lane
anche un bilanciamento perfetto darebbe ~29–40 s solo di servizio, e il
makespan reale (56–68 s) è dominato da batch da 9–10 item con 15–28 s di
render (costo frame sul manifest fused, §9.3.4) e dal ramp di coda. La leva
non è più la pipeline: è il costo per membro in RenderingGen/Chronon3d,
oppure batch più ampi (meno costo fisso per batch) — fuori dal perimetro
`refactored`. La baseline 83,98/min citata in A5 non è confrontabile con
questa serie (payload e generazione diversi) e non viene usata come termine
di paragone.

#### 9.8.5 Verifiche eseguite

- `gofmt -l internal/platform/audio internal/app/wiring/voiceover internal/capabilities/scripts` → vuoto; `go vet` sui package audio e wiring → PASS.
- `go test ./internal/platform/audio/... ./internal/app/wiring/voiceover/... -count=1` → PASS; `go test ./internal/capabilities/scripts/... -count=1 -race` → **PASS (53,2 s)**.
- `go test ./... -count=1` con le sole variabili object-store da `.env` (`RENDERINGGEN_STORE_URL`, `RENDERINGGEN_ARTIFACT_STORE_TOKEN`, `OBJECTSTORE_TOKEN`, `OBJECTSTORE_SIGNING_SECRET`) → **298 package ok, 0 FAIL**. Nota di ambiente (preesistente, non causata da questa modifica): senza token lo store reale risponde 401 ai 5 test `renderinggen`; caricando l'intero `.env` falliscono invece i test che assumono i default perché `.env` imposta `OBJECTSTORE_PUBLIC_URL` (modalità direct) e `PIPELINEGEN_YTDLP_*`. Nessuno di questi file è toccato dalla modifica.
- Build + deploy: `go build -o bin/pipelinegen ./cmd/server`, restart `systemd`, `/ready` HTTP 200; env del processo verificato (split per-scena, barriera lingua, workers=3, `RENDERINGGEN_GPU_SLOTS=3`, `VELOX_MEDIA_EXECUTION_SLOTS=3`, `max_concurrent_tts` da config = 24). Canary 4 lane ripristinato a 3 e verificato dopo restart.

#### 9.8.6 Residui e gate bloccati

1. **Gate ≥100 item/min — FAIL (aperto).** Mediana 62,32 (3 run comparabili);
   canary 4 lane 76,16. Richiede il costo per batch in RenderingGen/Chronon3d
   o batch più ampi; non chiudibile in `refactored`.
2. **`prepare_join` — varianza non attribuita.** 0,33 s (r4) / 11,9 s (r5) /
   8,6 s (r6) / 28,5 s (r3): include annotazioni + eventuale geocode +
   pianificazione intenti. È sulla critical path prima del branch P1; il
   candidato è la geocodifica seriale per scena/entità, ma non è provato da
   questa serie e non è stato modificato (nessuna ottimizzazione speculativa).
3. **A3 (Chronon3d, suite encoder-reuse) — BLOCKED:** nessun `CMakeCache.txt`
   né target test costruito nel workspace; richiede configure+build cross-repo.
4. **A4 — parziale:** build/deploy/restart `refactored` fatti; build Chronon3d
   `-j14` e commit mirato non eseguiti.
5. **Run mix completo con mappe (v3.1/v4)** e shadow VRAM B3 restano gate
   separati (`TICKET-100X-PERF-WAVES`), non misurati qui.

## 9.9 Overlay-only: attribuzione e percorso ai 100/min

**Ambito:** nessun final job, nessun video finale, nessuna modifica al
consumer Rust. I risultati sotto non certificano il gate ≥100/min. La mediana
r4/r5/r6 è 62,32 item/min su 66–69 item senza mappe; il mix cambia fra run,
quindi la serie è diagnostica e non equivale a una ripetizione deterministica
con 100 item. I valori storici non vengono sovrascritti.

### 9.9.1 Punto 1 — attribuzione batch

Nel worktree `cmd/goldensummary` riporta per batch `submit_ms`,
`queue_residence_ms` (queued → started), `queue_service_ms` (started →
completed), `completion_observation_ms` (attesa producer), `gpu_lane_wait_ms`,
metriche worker e, quando disponibili, frame NVENC/software, byte GPU upload,
frame time e fps Chronon. Metrica assente = sconosciuta, non zero. Il vecchio
`queue_wait_ms` è l'attesa di completamento osservata dal producer, non la
permanenza in coda. Le misure si sovrappongono: non sommarle.

Riproiezione in sola lettura di snapshot r4/r5/r6 e DB locale, senza render:

| Run | Batch attribuiti | Coda mediana / max | GPU lane mediana / max | Render batch più lungo |
|---|---:|---:|---:|---:|
| r4 | 14/14 | 123,5 / 1240 ms | 0,286 / 5,742 ms | 9 item, 27.952 ms |
| r5 | 15/15 | 9 / 1292 ms | 0,078 / 0,325 ms | 10 item, 19.825 ms |
| r6 | 15/15 | 11 / 1266 ms | 0,082 / 0,335 ms | 7 item, 15.598 ms |

In r5 il batch più lungo riporta 285 frame NVENC, 177.482.968 byte di
upload, 33,47 ms di frame medio e 53,94 ms p95. Il tempo `render_ms` non è
il solo tempo GPU e le metriche non attribuiscono costi ai singoli membri.
Le attese di lane misurate riguardano soltanto il punto strumentato e non
escludono attesa prima della submission, limiti del pool o contesa interna al
render. Per rigenerare il report senza modificare lo storico:

```bash
cd refactored
go run ./cmd/goldensummary \
  --job-full ops/jobs/overlay_throughput_golden_100_20261022/results/tts-fast-20261029-r5/job.full.json \
  --db data/media/media.db.sqlite
```

### 9.9.2 Punto 2 — job dedicati sovrapposti

Il worktree avvia i batch per scena e i job dedicati in parallelo usando i
pool e l'admission esistenti; errore o cancellazione non produce un risultato
parziale. `TestRunSplitOverlayWork*` e `TestAssembleSplitManifestReference`
sono passati con race detector. Benchmark **simulato** (5 iterazioni, due
attese da 10 ms): seriale 20,78 ms/op, sovrapposto 10,90 ms/op. Non misura
GPU né overlay/min, e non dimostra beneficio su workload senza job dedicati.

Per completare la misura reale: usare lo stesso piano congelato comprendente
scene e mappe/chain, stessi asset, cache, topologia e binari, solo variando
la versione seriale/sovrapposta; eseguire almeno 3 run valide per versione,
registrare durata e throughput, piani, errori, fallback, utilizzo e contesa.
Confrontare output e timing dei singoli overlay. Se la contesa aumenta o i
risultati differiscono, non promuovere la modifica. Il driver golden crea
solo overlay, ma è un submit live che può consumare risorse e accedere a
provider/store: eseguirlo esclusivamente sull'ambiente esplicitamente
abilitato, senza deploy o restart impliciti.

### 9.9.3 Punto 3 — costo e dimensione batch

La riproiezione mostra render elevati anche con attese di coda molto basse;
non basta aumentare lane o assumere che batch più grandi siano migliori.
La composizione packed già elimina gli intervalli vuoti, preservando le
finestre sovrapposte. Nel worktree ora è più facile confrontare costo, frame,
frame time e volume di upload per batch; **non è stata cambiata la strategia
di partizione né dimostrata una riduzione del costo GPU**.

**Aggiornamento micro-batch (2026-10-29):** `EnqueueChrononPlanSplit`
(dietro `OVERLAY_RENDER_PER_SCENE`) ora partiziona ogni scena in componenti
di finestre letterali sovrapposte (`groupManifestCandidatesIntoMicroBatches`
in `render_queue_split.go`). Item con finestre che si sovrappongono restano
sempre nello stesso batch; componenti disgiunte diventano batch separati in
ordine di piano. Test: `TestMicroBatchesNeverSplitOverlappingItems` e i test
split/packed/batch passano (`go test ./internal/capabilities/scripts -run
'MicroBatch|GroupManifest|PlanSpansMultipleScenes|EnqueueChrononPlanSplit|Batch|Packed'`).
Limite: è una verifica di correttezza offline, **non** una misura di
throughput; non è stata eseguita una run GPU A/B, quindi non si dichiara
alcun miglioramento. Il percorso resta dietro il feature flag.

Per completare l'ottimizzazione:

1. Congelare piano/materializzazioni, fingerprint di renderer, cache e
   topologia; registrare frame, durata packed, famiglie, overlap, byte e
   servizio per batch, p50/p95 e makespan, inclusi tutti i retry.
2. Confrontare offline partizione attuale e micro-batch candidati su confini
   sicuri, mantenendo insieme finestre e dipendenze sovrapposte. Scene
   attraversate, z-order, background video ed effetti temporali richiedono
   verifica esplicita; un cap numerico arbitrario non garantisce parità.
3. Implementare un'opzione bounded solo dopo prova di sicurezza, usando
   scheduler e admission già esistenti; mantenere il comportamento corrente
   per piani non rappresentabili. Verificare frame/tempi, ordine, artefatti,
   cancellazione e fallback; confrontare output decodificato.
4. Eseguire A/B reale a input e risorse identici, almeno 3 run per variante;
   riportare item completati validati/min, frame, wall, attese e memoria. Non
   promuovere se parità o miglioramento misurato mancano.

### 9.9.4 Punto 4 — golden completo con mappe

Il test sintetico `TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items`
è passato: v3.1 richiede `final_job=false`, `render.enabled=false`,
`overlays_only=true` e produce con fixture 25 frasi brevi + 25 lunghe +
20 immagini + 10 entity image + 10 date + 10 mappe. I test di ammissione e
risoluzione territorio sono passati su fixture. Non provano disponibilità
live dei candidati, del catalogo o del renderer installato.

Per chiudere il gate completo:

1. Verificare la provenienza del payload v3.1 e la disponibilità di 100
   candidati/asset validi, incluse 10 mappe; verificare catalogo ADM3 e pin
   risolvibili, renderer effettivo compatibile e versione worker.
2. Su host autorizzato e idle, con topologia lane dichiarata e configurazione
   identificata, eseguire `ops/jobs/overlay_throughput_golden_100_20261022/run_golden_v3.sh <label>`
   tre volte con label distinte. Non riusare la v3 con `region_zoom` ritirato.
3. Per ogni run conservare payload/hash, versioni, cache, mix materializzato,
   snapshot, summary, errori/fallback e verifica artefatti, frame e timing.
   Verificare a posteriori che ci siano esattamente 100 item validati col mix
   richiesto: il successo del job o del test planner non è sufficiente.
4. Calcolare la mediana delle tre run comparabili: PASS solo con ≥100
   item/min e tutti i controlli precedenti; altrimenti FAIL/BLOCKED con causa.
   Il test no-map e quello completo rimangono separati.

**Stato:** strumentazione e verifiche locali disponibili; A/B GPU, benchmark
reale di partizioni e tre run del golden completo restano NOT RUN. Nessun
claim di accelerazione reale o chiusura del gate. La misurazione live richiede
un ambiente predisposto e accesso al percorso autenticato fuori dal workspace;
in questa sessione non sono stati effettuati submit, restart o deploy.

**Verifiche locali:** `go test ./cmd/goldensummary -count=1` PASS;
`go test ./internal/capabilities/scripts -run 'TestRunSplitOverlayWork|TestAssembleSplitManifestReference|TestCompileOverlayPlanGoldenThroughputMixMaterializes100Items' -count=1 -race`
PASS; test mirati di risoluzione e ammissione mappe PASS
(`TestMicroBatchesNeverSplitOverlappingItems` incluso).

**`make verify-agent` ora PASS (2026-10-29, `verify_exit=0`).** Il gate
falliva su `verify-overlay-model-alias`: il facade generato
`internal/capabilities/overlays/model_alias.go` era disallineato dal modello
(mancava l'alias `ManifestPlacement`). Rigenerato con
`go run ./cmd/overlay-model-alias-gen --generate` (+1 riga, nessuna modifica
manuale) e rivalidato con il modo drift-check. Foundation, static (vet+build)
e `verify-changed-components` passano; 17 componenti da cache, 3 eseguiti.
Limite noto: ~5 minuti del gate sono la scansione gitleaks, non un hang.

## 10. Piano operativo — compilatore incrementale della timeline

**Stato di questa sezione: SPEC / NOT RUN.** Questo ticket coordina le fasi;
non introduce una seconda authority. Il cambio di paradigma è passare da
«un job = un file completo da rifare» a **timeline dichiarativa con unità
indipendenti, identità verificabile e materializzazione su richiesta**. Primo
rilascio: migliorare l'overlay-only e misurare; preview eseguibile, export
incrementale e speculazione sono fasi successive, non prerequisiti del gate
100/min. Non assumere 100×, scalabilità lineare o eliminazione dell'encode.

### 10.1 Confini e risultati che si misurano separatamente

- **Overlay-only:** item validati / tempo tra primo submit e completamento
  dell'ultimo item, più conteggio, durata, frame e mix effettivamente prodotti.
- **Video completo:** wall da submit a consegna certificata, includendo
  generazione, attese, audio, assemble e pubblicazione richiesti.
- **Prima scena utile / preview:** submit → primo output visionabile; non
  confonderlo con la consegna definitiva.
- **Modifica incrementale:** input iniziale → output corretto dopo una
  modifica locale; contare lavoro rifatto, cache hit e wall.
- Per ogni run registrare SHA e stato dei quattro repo e dei binari, GPU,
  codec, risoluzione, fps, frame, lunghezze, payload/hash, mix effettivo,
  versioni modelli, stato cache, numero lane e carico concorrente. Misurare
  p50/p95 dei tempi di coda e servizio e wall critico; le fasi sovrapposte
  **non si sommano**. Salvare riferimenti ai sidecar e agli snapshot esistenti.
- Distinguere baseline overlay-only senza mappe e golden richiesto completo
  con 100 item e mappe. Tre run confrontabili per la mediana del gate;
  cambiare payload, modello, mix, binario o topologia richiede una nuova
  baseline. Nessun deploy, restart, submit o test live senza autorizzazione.

### 10.2 R0 — baseline e attribuzione prima delle modifiche

**Owner:** PipelineGen + RenderingGen; Chronon per sidecar GPU.

1. Leggere il golden `ops/jobs/overlay_throughput_golden_100_20261022/README.md`
   e le evidenze §9.8; verificare payload effettivo, asset e disponibilità
   mappe, non dedurre 100 item dal nome del file. Assicurare build identificata
   e freeze Chronon rispettato; non modificare baseline storiche.
2. Per batch e item attribuire `queue_wait_ms`, attesa lane GPU,
   materializzazione/probe, IPC/setup, prepare, graph/render, encode, mux,
   publish, `prepare_join`, generazione/TTS e tempo alla prima scena.
   Usare metriche e sidecar esistenti; aggiungere solo il punto di
   strumentazione mancante nel suo owner, distinguendo null da zero.
3. Costruire percorso critico a partire da intervalli temporali, non dalla
   somma dei wall annidati. Confrontare per famiglia e numero di frame: un
   overlay immagine, una mappa e un testo non hanno lo stesso costo.
4. Ripetere il golden no-map ×3 a parità di condizioni; separatamente
   eseguire il mix completo con mappe ×3 dopo prerequisiti e autorizzazione.
   Rilevare fallimenti, fallback, lavoro ricostruito e output verificati.
5. **Gate R0:** report riproducibile con provenienza e qualità; il gate
   100/min si chiude solo per il workload specificato che soddisfa anche il
   conteggio/mix richiesto. Se una fase è assente o non attribuita, segnare
   NOT RUN/UNKNOWN; non dedurre la causa dalla sola attesa in coda.

### 10.3 R1 — partizionamento adattivo e batch bounded

**Owner:** `refactored/internal/capabilities/scripts/` per la forma del piano;
`RenderingGen/renderinggen/internal/processor/` e `chronon/` per ammissione e
misure; Chronon conserva executor/encoder. Si riusano `render_queue_split.go`,
`overlay_manifest_timeline.go`, `overlay_manifest_packed.go`, socket spread e
backpressure esistenti: non creare un secondo scheduler.

1. Profilare dimensione, durata/frame, famiglia, overlap, media e costo
   osservato per i batch esistenti. Costruire prima un calcolo deterministico
   di confronto offline fra piano unico, scena e micro-batch; non affermare che
   più gruppi o batch maggiori siano sempre migliori.
2. Definire gruppi solo su confini sicuri: stessa griglia, background e
   dipendenze compatibili, ordine e compositing invariati, limiti memoria,
   frame e duration espliciti. `groupManifestCandidatesByScene` oggi assegna
   un item che attraversa la scena a quella di inizio: provare tale caso,
   sovrapposizioni cross-scena, transizioni, effetti temporali e z-order;
   se non sono indipendenti tenere insieme le dipendenze o rifiutare lo split.
3. Alimentare i gruppi al submitter esistente con concorrenza bounded e
   backpressure. Una stima di costo può guidare raggruppamento e distribuzione
   solo dopo profiling: preservare fairness, aging e limite VRAM/NVENC;
   timeout, retry e cancellazione restano idempotenti. Non aggiungere lane
   per deduzione da `queue_wait` senza verificare servizio e contesa.
4. Confrontare A/B lo stesso input e topologia: numero batch, lavoro per
   batch, coda, tempo GPU, memoria, frame prodotti, output decodificato e wall.
   La fallback da split al percorso singolo oggi esiste: contarla e indicarla
   nell'esito; non dichiarare throughput split quando è avvenuta.
5. **Gate R1:** ordine e pixel/audio equivalenti secondo il contratto,
   nessuna perdita o duplicazione e rate mediano misurato. Se il batching
   peggiora il wall o aumenta troppo la contesa, non promuoverlo.

### 10.4 R2 — multi-artefatto per final job, non solo overlay-only

**Owner:** PipelineGen per manifest/handoff; BrainRust per proiezione;
RenderingGen e Chronon per i fatti certificati. Ticket di dettaglio:
[`TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29.md`](TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29.md).

1. Fotografare i consumer effettivi. Oggi `render_queue_split.go` produce
   `Batches` e `Items`, ma `Manifest=nil`; final job è escluso dallo split.
   `BrainRust/pipelinegen-muscles/src/transform_manifest.rs` implementa la
   proiezione **di un artefatto** e ricodifica: il closeout P5 documenta codice
   e test, non deploy remoto. L'header di `OVERLAY-MANIFEST-CONTRACT.md` è
   anteriore a quel closeout: verificare il binario effettivo, non inferire
   deploy dal solo sorgente.
2. Prima del codice concordare una revisione versionata del contratto: elenco
   di artefatti certificati (hash, durata/frame, fps e formato) e mappatura
   esplicita di ogni entry alla sorgente; conservare `in_us/out_us` assoluti e
   `artifact_*` quando packed. Un solo owner per entry e retry idempotente.
3. Specificare overlap fra batch: il replace full-frame di due artefatti
   indipendenti non è generalmente equivalente alla composizione comune.
   Raggruppare le finestre interdipendenti oppure definire un vero alpha
   composite con z-order; rifiutare casi non rappresentabili, mai scegliere un
   artefatto arbitrario. Preservare scene clip, mappe/chain e audio.
4. Implementare producer e validazione nel proprietario del manifest, poi
   consumer Rust con check griglia, copertura, ordine, hash e riferimenti;
   attivare final-job solo dopo adozione del consumer. I vecchi payload restano
   validi tramite la migrazione definita, senza interpretazione ambigua.
5. Test: single vs multi artefatto frame-by-frame, scene-crossing e overlap,
   fps razionali, boundary esclusivi, missing/tampered asset, retry,
   crash/resume, numero frame, audio e durata. Poi golden reale e deploy
   coordinato esplicitamente autorizzato. **Gate R2:** non dichiarare final
   job supportato finché producer, consumer effettivo e golden non passano.

### 10.5 R3 — identità, invalidazione e rigenerazione incrementale

**Owner:** PipelineGen per dipendenze editoriali/manifest; RenderingGen per
asset; Chronon per programma e pixel. Riutilizzare `OverlayIntent`,
`CanonicalTimeline`, `OverlayManifest.Digest()`/`RetryKey()`,
`overlayManifestCompositionCacheKey` e cache content-addressed già esistenti.

1. Disegnare prima una matrice di dipendenza per testo, traduzione, TTS,
   timing, asset, motion, layer, scena, audio master e output. Distinguere
   identità editoriale stabile, identità del lavoro, digest del contenuto,
   placement e digest dell'artefatto. Non cambiare `RetryKey` o `Digest` senza
   migrazione del contratto; il solo riuso per item non colpisce la cache
   dell'attuale composizione intera.
2. Definire fingerprint canonico versionato che copra **tutti** gli input
   che influenzano il risultato: contenuto/asset e font, testo e shaping
   (lingua, direzione, feature e font fallback), template/motion, tempo relativo,
   effetti, colori/alpha, fps/canvas, configurazione output, semantica e
   versioni di compilatore/renderer. Per output codificati includere parametri
   codec e compatibilità di bitstream. Distinguere renderer e platform quando
   il contratto di determinismo non garantisce cross-backend equivalence.
3. Propagare invalidazione sul grafo già posseduto dalla pipeline: cambio
   frase può modificare TTS, word timing, offset delle scene successive,
   transizioni e mix audio; una modifica che cambia durata non invalida solo
   la finestra visiva locale per definizione. Le dipendenze run-wide o
   temporali non risolte richiedono ricalcolo conservativo.
4. Riusare solo risultati certificati, leggibili e con hash corretto; gestire
   restart, eviction, limiti disco/VRAM, concorrenza e pubblicazione idempotente
   via authority e outbox esistenti. Nuova famiglia/cache Chronon solo con ADR
   e tassonomia: preferire estendere il meccanismo proprietario appropriato.
5. Golden: cambiare una parola con durata invariata, cambiare durata,
   posizione, lingua, font, asset e versione renderer; confrontare il risultato
   incrementale con ricostruzione completa, inclusi audio e boundary. Misurare
   hit/miss, frame e segmenti riutilizzati, byte e wall cold/warm. **Gate R3:**
   nessun cache-hit errato e ricalcolo limitato provato dalle dipendenze.

### 10.6 R4 — pipeline streaming e speculazione controllata

**Owner:** PipelineGen; asset prefetch RenderingGen già in
`processor/pipeline.go`. `OverlayIntent` è già creato prima del timing;
`SceneTextReady` sovrappone TTS e analisi e P2 può già anticipare il render
sul piano pronto. Non duplicare questi meccanismi.

1. Tracciare per scena/frase lo stato delle dipendenze: testo stabilizzato,
   asset selezionati, TTS/timing pronto, offset assoluto noto, entità e piano
   validati. Le `CanonicalTimeline`/`EntityTimeline` run-wide sono oggi un
   vincolo: non avviare render definitivo prima che la porzione necessaria sia
   certificata; se occorre, progettare un contratto incrementale versionato.
2. Spostare anticipatamente solo lavoro indipendente dal timing (prefetch
   asset conosciuti, preparazione font/template); riusare il fan-out esistente.
   Su ipotesi non confermate marcare lavoro speculativo con versione degli
   input, budget e priorità inferiore; limitarne GPU, rete, memoria e coda.
3. Su modifica/cancellazione scartare o annullare il lavoro non più valido;
   promuovere un risultato solo dopo validazione di **tutte** le dipendenze.
   Nessun side effect di consegna da risultato speculativo, outbox e single
   writer restano autoritativi. Retry/restart non duplicano artefatti.
4. Misurare tempo prima scena, wall job, lavoro scartato, hit, contesa e
   qualità; testare cambio testo/timing dopo prefetch, annullamento, errore e
   resume. **Gate R4:** speculazione migliora il percorso critico sullo stesso
   workload senza rallentarlo per saturazione; altrimenti limitarla o ritirarla.

### 10.7 R5 — preview prima, export dopo

**Owner:** Chronon per rendering, PipelineGen per orchestration; niente GUI nel
core. Ticket motore [`TICKET-DRAFT3D-PREVIEW`](../../../Chronon3d/docs/tickets/TICKET-DRAFT3D-PREVIEW.md).

1. Prima sperimentare proxy a risoluzione ridotta dello **stesso piano**,
   stessi timing, font, asset, frame rate e semantica; isolare la cache preview
   da quella full-resolution. Misurare submit → prima immagine/segmento,
   wall preview, export e qualità: ridurre i pixel non implica lo stesso
   fattore di riduzione del tempo.
2. Render definitivo resta autoritativo; le differenze di antialiasing,
   effetti, leggibilità e crop sono esplicitate. Le modifiche approvate devono
   restare semanticamente valide anche a full-resolution.
3. Solo se serve, prototipare un player della timeline per testo rigido/fade:
   profilo dichiarato del contratto esistente, stesse curve e shaping tramite
   interfacce condivise, caricamento/buffering espliciti. Rifiutare effetti non
   supportati; non duplicare evaluator o ownership dei pixel. Export asincrono
   usa lo stesso piano certificato. Non promettere creazione a tempo zero.
4. **Gate R5:** parità sui frame campione con tolleranza esplicita, tempi e
   qualità registrati, full-res invariato; player e GUI non sono prerequisiti
   del gate overlay-only.

### 10.8 R6 — riuso dei segmenti compressi e scala orizzontale

1. Applicare il chunking audio-aware già descritto nel
   [`TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY`](TICKET-CHUNK-AUDIO-AWARE-ASSEMBLY.md)
   solo ai piani supportati, con admission policy certificata e audio-owner
   unico; non confonderlo con il multi-artefatto overlay R2.
2. Per export incrementale classificare segmenti invariati e copy-eligible:
   codec/profile/extradata, dimensioni, griglia, closed GOP, timestamp e
   boundary audio devono essere compatibili. Gli overlay impressi richiedono
   composizione e re-encode delle finestre modificate; il consumer Rust P5
   attuale ricodifica anche la proiezione, quindi non è stream-copy.
3. Testare decode frame, durata, PTS/DTS, sincronizzazione e priming audio,
   concatenazioni, ingressi non compatibili e fallback esplicito. Misurare
   frame realmente codificati, byte, latenza e qualità.
4. Per più GPU/worker usare la queue e admission esistenti; misurare VRAM,
   NVENC, rete, storage, saturazione e sbilanciamento. **Gate R6:** accelerazione
   e parità end-to-end misurate, non N GPU = N× assunto.

### 10.9 Sequenza e cancellazione del vecchio percorso

R0 → R1; R2 è prerequisito del final-job multi-artefatto; R3 richiede identità
stabili e usa R2 se la granularità scelta produce più artefatti. R4 può
procedere sul prefetch già sicuro dopo R0, ma la materializzazione anticipata
richiede i contratti timing; R5/R6 seguono le rispettive prove e non bloccano
il gate overlay-only. Motore: dettagli in
[`Chronon3d TICKET-100X-PERF-WAVES`](../../../Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md)
e [`TICKET-PARADIGM-ALPHA-LAYER-HUD`](TICKET-PARADIGM-ALPHA-LAYER-HUD.md).

Per ciascun eventuale bridge di migrazione registrare nel ticket/ADR owner,
reason, exit condition osservabile, equivalence gate, removal scope e stato;
dopo la parità e il cutover rimuovere il percorso sostituito. Non eliminare
fallback necessari ai piani non supportati e non indebolire i gate per
raggiungere un numero. I claim storici §3–§5 sono ipotesi, non criteri di PASS.
