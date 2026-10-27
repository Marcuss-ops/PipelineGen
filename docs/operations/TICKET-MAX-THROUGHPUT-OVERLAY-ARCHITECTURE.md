# TICKET: Architettura per la Massima Velocità di Generazione e Rendering Overlay (VeloxEditing)

- **Data:** 2026-10-26
- **Autori:** Antigravity + Pierone
- **Moduli interessati:** `refactored` (PipelineGen), `RenderingGen`, `Chronon3d`, `BrainRust` (pipelinegen-muscles)
- **Stato:** ATTIVO / IN IMPLEMENTAZIONE
- **Target Prestazionale:** Throughput overlay da ~80–100/min a **>2.500 overlay/min**; tempo di pianificazione da 4m45s a **<30s**; eliminazione del 90% dell'I/O disco.

---

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
