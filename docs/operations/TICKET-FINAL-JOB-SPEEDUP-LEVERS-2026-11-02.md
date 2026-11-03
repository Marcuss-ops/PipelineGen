# TICKET-FINAL-JOB-SPEEDUP-LEVERS — dove si guadagna tempo nel final job

**Stato:** APERTO. Una leva è misurata (multi-daemon **1,98x** sullo shape
overlay), una è già chiusa sul consumatore (copy/cache), tre richiedono una
misura o un cambio al producer. Il fix di ammissione del 02-11 è nel working
tree di `Chronon3d` ma **non è formalizzato**: test non eseguito nell'export,
ledger §7 ancora «Bloccato», evidence doc assente.
**Creato:** 2026-11-02.
**Owner:** `refactored` (PipelineGen, `internal/app/wiring/`,
`internal/capabilities/scripts/`) + RenderingGen (`internal/chronon/`,
`internal/config/`, `config.yaml`) + Chronon3d (`apps/chronon3d_cli/daemon/`).
**Coordina:** [`TICKET-FINAL-JOB-BITSTREAM-REUSE-2026-11-01`](TICKET-FINAL-JOB-BITSTREAM-REUSE-2026-11-01.md),
[`TICKET-CLOSEOUT-LEDGER-2026-10-29`](TICKET-CLOSEOUT-LEDGER-2026-10-29.md) §7,
[`TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE`](TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md).
**Fonte numeri:** `ops/jobs/garlasco_editorial_e2e_20261030/job-full-r100.json`
e l'export `freebuff-chat-2026-11-02T10-48-25.391Z.md` (misura GPU del 02-11).
**Regola:** un numero è «misurato» solo con comando ed esito; il resto è
«stimato» e non chiude un gate. Non si sommano le percentuali degli stage: la
timeline ha lavoro sovrapposto.

## TODO — cosa manca

- [ ] **L0 — Formalizzare il fix di ammissione** (test + evidence + ledger).
- [ ] **L1 — Portare il copy-window al 100%** delle finestre coperte da una
  sorgente certificata (`encode = 0`).
- [ ] **L2 — Togliere `prepare_join` dal critical path** o ridurlo a cache/reuse.
- [ ] **L3 — Attivare 2–3 socket/daemon** e rimisurare la stessa run.
- [ ] **L4 — Tagliare la tassa fissa per frammento** (`PREPARE_PLAN`,
  `RENDER_BATCH`, sessione encoder).
- [ ] **L5 — Ridurre il numero di frammenti** con un gate di pixel equality.

## 0. Sintesi misurata

### 0.1 Run r100 — dove va il wall

`job-full-r100.json`: `timing.wall_ms = 60085`, `attributed_ms = 59872`,
`overlapped_ms = 8518`, `unattributed_ms = 213`.

| Stage | Tempo | % wall | Nota |
|---|---:|---:|---|
| `remote_final_job` | **23,349 s** | 38,9% | stage più pesante; il JSON **non** la scompone |
| `overlay_render` | 14,633 s | 24,4% | lane overlay locale (RenderingGen → Chronon) |
| `prepare_join` | 10,688 s | 17,8% | join della preparazione frammenti |
| `generate` | 10,217 s | 17,0% | script/LLM, fuori dal perimetro GPU |
| `scene_analysis` | 8,547 s | 14,2% | fuori dal perimetro GPU |

Le percentuali si sovrappongono (`overlapped_ms` 8,5 s): non sono additive.
**Prima di ottimizzare `remote_final_job` serve il breakdown** con
`ops/tools/final_job_breakdown.py`; il ticket non attribuisce quei 23,3 s a
Chronon senza la scomposizione.

### 0.2 GPU — l'unica leva già misurata

Due job overlay indipendenti su **due daemon** (RTX A4000, 48 frame):

- seriale **1434 ms**, concorrente **933 ms**, **1,98x**, **501 ms** risparmiati;
- VRAM: delta **1528 MiB** per la coppia, **~764 MiB/job**;
- artefatti identici.

Sullo **stesso** daemon, due job FullGraph concorrenti **non sono sicuri**:
stesso piano → hang; piani diversi → il daemon muore (`ipc read reply: EOF`).
La causa è la classificazione: un piano composito senza `composition_required`
viene charged come DirectYUV (1 GiB) → `ParallelDisjoint` → due FullGraph nello
stesso execution domain.

Il fix (nel working tree, non committato) è corretto e conservativo:

- `Chronon3d/apps/chronon3d_cli/daemon/daemon_service_ipc_protocol.cpp:195`
  `plan_requires_composition` + `read_plan_admission_shape`;
- `Chronon3d/apps/chronon3d_cli/daemon/daemon_service_ipc_render_job.cpp:116`
  `parallel_disjoint = hint && !plan_shape.requires_composition`;
- test `Chronon3d/tests/cli/test_daemon_plan_admission_shape.cpp`.

### 0.3 Copy/cache — il guadagno storico

`TICKET-FINAL-JOB-BITSTREAM-REUSE`: r49 **495,6 s** → r78 **49,8 s** (−90%),
handoff **1,6 s**; r96 a cache fredda **140,9 s** con `payload_ms` **70,3 s**.
Il guadagno viene da cache, riuso e copy-window, **non** dai tre P0
architetturali, che restano aperti. Questo è il motivo per cui L1 viene prima
di L4: se la finestra è copiabile, non c'è encode da ottimizzare.

### 0.4 Cosa NON è una leva (non rifare)

- **Abbassare `kMeasuredFullGraphPeakBytes` (8,8 GB).** Non serve: la classe
  FullGraph resta seriale per sicurezza misurata (hang/EOF). Il fix corretto è
  la composition detection, non il floor.
- **`CHRONON_RENDER_JOB_SLOTS=2` / `gpu_lanes=3` con un solo socket.** Il
  daemon serializza per socket: le lane in più crescono solo
  `renderinggen_worker_lane_wait_seconds`.
- **Overlap in-daemon di due FullGraph.** Misurato: hang o crash. Non è un bug
  dell'harness.
- **Probe/hash ripetuti.** Già chiusi: `digest.Verifier` memoizza,
  `sourceCensus` è singleflight, il fragment cache distingue copy/encode.

## 1. Mappa codice → leva

| Leva | File da modificare | Funzione / punto | Cambiamento |
|---|---|---|---|
| **L0** | `Chronon3d/.../daemon_service_ipc_protocol.cpp` | `plan_requires_composition`, `read_plan_admission_shape` | già applicato; manca test eseguito + evidence + ledger |
| **L0** | `Chronon3d/.../daemon_service_ipc_render_job.cpp` | `parallel_disjoint` (riga ~112) | già applicato; verificare con build/test |
| **L1** | `refactored/internal/app/wiring/final_job_fragment_pipeline.go` | `finalJobWindowOnGOPGrid` (:407) | il gate copy deve essere soddisfatto dal producer |
| **L1** | `refactored/internal/app/wiring/final_job_segment_prepare.go` | `finalJobCopyWindowsEnabled` (:193), decisione copy (:325), `sourceCensus` (:498) | contare `copy/encode/cache/reuse`; eliminare gli encode evitabili |
| **L1** | `refactored/internal/app/wiring/final_job_placement.go` | `derivePlacement`, `probeFinalJobFragment` (:232) | ridurre il remux dei piazzamenti duplicati |
| **L1** | `refactored/internal/capabilities/scripts/final_job_plan_prepare.go` | `prepareFinalJobVideoSegments` (:20) | il producer deve consegnare sorgenti sulla griglia |
| **L2** | `refactored/internal/capabilities/scripts/final_job_payload.go` | chiamata a `prepareFinalJobVideoSegments` (:433) | spostare/overlappare la prep rispetto al payload |
| **L2** | `refactored/internal/capabilities/scripts/final_job_plan_prepare.go` | `finalJobPrepareSegments` (:459), `Parallelism` | misurare lane e join; riusare la cache prima del join |
| **L3** | `RenderingGen/renderinggen/config.yaml` | `chronon.socket_path` (:51) | `socket_paths` con 2–3 socket; `gpu_lanes ≤ socket` |
| **L3** | `RenderingGen/renderinggen/internal/config/lane_parity.go` | `RenderSockets` (:37) | già pronto; test `lane_parity_test.go` |
| **L3** | `RenderingGen/renderinggen/cmd/renderinggen/main.go` | `SpreadAcrossSockets` (:134) | già pronto; nessun codice nuovo |
| **L4** | `RenderingGen/renderinggen/internal/chronon/ipc.go` | `ipcCommandPreparePlan` (:36), `Render` (:341) | aggiungere il comando e inviarlo una volta per piano |
| **L4** | `Chronon3d/.../daemon_service_ipc_dispatch.cpp` | `ipc_prepare_plan` (:31) | oggi richiede una composition id: serve la variante per plan path |
| **L4** | `Chronon3d/.../daemon_service_ipc_protocol.cpp` | `decode_render_batch_payload` | `RENDER_BATCH` esiste; il client RenderingGen non lo usa |
| **L4** | `Chronon3d/.../utils/video/native_av_encoder_lifecycle.cpp` | open/close per job | sessione persistente = P4, non micro-fix |
| **L5** | `refactored/internal/capabilities/scripts/final_job_compiled_plan.go` | `finalJobComposePlanSegments` (:369), `finalJobMergeContiguousSegments` (:424) | fondere solo finestre provabilmente continue; altrimenti ridurre al producer |

## 2. L0 — Formalizzare il fix di ammissione (P0, subito)

**Obiettivo:** chiudere il punto 3 dell'audit con evidenza, senza dichiarare
una concorrenza che non esiste.

**Passi**

1. Compilare ed eseguire il test:
   `cmake --build Chronon3d/build/... --target chronon3d_cli_tests` (o il
   target che include `tests/cli/test_daemon_plan_admission_shape.cpp`) e
   lanciare il caso. Il test non risulta eseguito nell'export.
2. Scrivere `Chronon3d/docs/evidence/gpu-admission-two-job-2026-11-02.md` con:
   comandi, seriale 1434 ms, concorrente 933 ms, 1,98x, 501 ms, 1528 MiB,
   ~764 MiB/job, hang/EOF in-daemon, artefatti identici.
3. Aggiornare `refactored/docs/operations/TICKET-CLOSEOUT-LEDGER-2026-10-29.md`
   §7 riga 3: da «Bloccato» a **«Chiuso con evidenza: overlap in-daemon
   REJECTED; multi-runtime 1,98x»**. Il criterio «admitted_slots ≥ 2» va
   riscritto: non è raggiungibile in-daemon per i piani compositi, e non è
   necessario se la produzione usa più socket.
4. Committare il fix e il test (oggi `git -C Chronon3d status` mostra i file
   modificati, non committati).

**Gate:** test verde; evidence presente; ledger senza «Bloccato» per il punto 3;
`git -C Chronon3d status` pulito.

**Rischio:** il fix è conservativo (un solo layer video senza chiavi di
compositing = DirectYUV; tutto il resto = FullGraph). Non allargare la regola
per «far passare» un piano composito: è esattamente il caso che ha fatto
crashare il daemon.

## 3. L1 — Copy-window al 100% (P0, la leva strutturale)

**Obiettivo:** per ogni finestra interamente contenuta in una sorgente
certificata e allineata alla griglia, il frammento deve essere **copiato**, non
ri-encodato. Criterio: `encode = 0`, `copied + reused + cached = 100%`.

**Cosa c'è già**

- `PrepareFinalJobVideoSegment` sceglie copy se
  `finalJobWindowOnGOPGrid(request) && finalJobCopyWindowsEnabled()` e il census
  certifica `start + FrameCount <= census.frames`
  (`final_job_segment_prepare.go:325`).
- `sourceCensus` è memoizzato per SHA e singleflight (`:498`).
- Il copy è `-c copy` e non viene ri-probato: il certificato deriva dal census.
- La cache distingue `copy-enabled` da `encode-only` (`fragmentWorkKey`).

**Perché non è al 100%**

Il gate è nel **consumatore**. Se la sorgente non è sulla griglia di delivery
(24/1 o 1/90000, GOP chiuso 48, `bf=0`, primo keyframe a 0, frame count noto),
il preparer non può copiare e ricade sull'encode. Il cambio vero è nel producer
che genera le sorgenti overlay; il ledger §7 lo chiama «criterio residuo» e lo
colloca fuori da questi tre repo.

**Passi**

1. Instrumentare il receipt: contare per run `copy`, `encode`, `cache`, `reuse`
   e `padded_frames` (i campi esistono in `finalJobSegmentWindow`). Oggi il
   ticket non ha il conteggio r100: va estratto prima di stimare.
2. Per ogni `encode`, loggare il motivo del fallback: time base non
   certificata, GOP non allineato, `start + frames > census.frames`, sorgente
   non certificata. Il predicato è in `finalJobWindowOnGOPGrid` e
   `probe.certify`.
3. Trasferire il contratto al producer: la sorgente deve essere già
   sulla griglia e con GOP chiuso. Il punto di emissione va identificato nel
   repo che produce `runtimeAssets` (overlay render / batch manifest); il
   consumatore resta `prepareFinalJobVideoSegments`.
4. Rimuovere il remux dei piazzamenti duplicati dove il contratto lo consente:
   `derivePlacement` (`final_job_placement.go:24`) rimuxa e ripubblica un
   container distinto per ogni piazzamento. È imposto dal Master
   (`manifest binding is not exact`), quindi non si tocca senza il contratto
   dell'assembler: va negoziato, non aggirato.
5. Rerun r100 con la stessa timeline e confrontare `encode`, `payload_ms`,
   `handoff`, wall.

**Gate:** `encode = 0` per le finestre coperte; `copied+reused+cached = 100%`;
nessun `tpad` su finestre complete; `profile_id = VELOX_ASSEMBLY_READY_V1`,
`copy_eligible = true`, `closed_gop = true`, `frame_count` esatto.

**Rischio:** se la sorgente non è sulla griglia, non c'è copy: non mascherare
il fallback abbassando il gate. Il criterio del ledger è esplicito.

## 4. L2 — Preparazione fuori dal critical path (P0/P1)

**Obiettivo:** ridurre `prepare_join` (10,7 s in r100) senza cambiare il piano.

**Cosa c'è oggi**

`final_job_payload.go:433` chiama `prepareFinalJobVideoSegments` in modo
obbligatorio, dopo la compilazione del piano e prima di FINALIZE. La funzione
ha già due fasi: validazione sequenziale e preparazione sulle lane del
preparer (`finalJobPrepareSegments`, `final_job_plan_prepare.go:459`); il join
è il punto in cui si aspettano tutte le lane.

**Passi**

1. Misurare nel receipt `segment_prep`, `payload_ms`, `prepare_ms`, `handoff`:
   il ledger dice che il budget chiude per costruzione, ma il valore r100 di
   `prepare_join` non è scomposto.
2. Se il piano è disponibile solo al payload, la prep non può partire prima di
   `generate`/`scene_analysis`; l'overlap realistico è con upload, audio e
   finalize. Spostare la chiamata in una goroutine che parte dopo il compile e
   viene joinata prima di FINALIZE, con `errgroup` e contesto cancellabile.
3. Alternativa più forte: il producer prepara i frammenti **prima** del final
   job, così `prepare_join` è solo cache/reuse. È lo stesso cambio di L1: se le
   sorgenti sono già sulla griglia, la prep non deve encodare.
4. Aggiungere un test di concorrenza (`-race`) sul nuovo join e un test che
   dimostri che l'ordine di binding resta quello del piano.

**Gate:** `prepare_join` ridotto o assorbito; nessuna differenza nel piano
canonico; `go test -race` sul package; cache/reuse > 0.

**Rischio:** anticipare la prep senza il piano compilato produce lavoro
inutile. Non aggirare il compile: il piano è la dipendenza.

## 5. L3 — Multi-socket / multi-daemon (P0, misurato)

**Obiettivo:** usare i tre daemon già vivi invece di accodare tre lane su uno.

**Stato:** `config.yaml` ha `gpu_lanes: 3` ma un solo `socket_path`
(`/var/run/chronon3d/chronon.sock`). I daemon `-b` e `-c` sono attivi su
`/run/chronon3d-b/chronon.sock` e `/run/chronon3d-c/chronon.sock`. Il codice
di spread è già pronto:

- `internal/chronon/socket_spread.go:59` `SpreadAcrossSockets`;
- `cmd/renderinggen/main.go:134`;
- `internal/config/lane_parity.go:37` `RenderSockets`;
- `internal/config/config.go:198` `SocketPaths`, validato (niente duplicati,
  niente stringhe vuote).

**Passi**

1. Verificare che i tre socket siano serviti e che i daemon siano la stessa
   build/versione del worker.
2. In `config.yaml` sostituire `socket_path` con:
   ```yaml
   socket_paths:
     - /var/run/chronon3d/chronon.sock
     - /run/chronon3d-b/chronon.sock
     - /run/chronon3d-c/chronon.sock
   ```
   Tenere `gpu_lanes: 3` (un lane per socket) o `2` con due socket. Non
   mettere più lane che socket: il surplus è coda.
3. Allineare `infra/native/renderinggen-b.yaml` (già su `-b`) e la unit
   `renderinggen-worker-b.service`; la unit A non deve duplicare il worker B.
4. Eseguire `go test ./internal/config ./internal/chronon ./cmd/renderinggen`.
5. Rerun r100 e confrontare `overlay_render`, `remote_final_job`,
   `renderinggen_worker_lane_wait_seconds`, `gpu_lane_wait_ms`, VRAM di picco.

**Gate:** riduzione misurata del wall sulla **stessa** run; artefatti
identici; VRAM per job ~764 MiB; nessun daemon oltre il limite sicuro.

**Rischio:** NVENC è un solo engine e i tre daemon condividono la GPU. Il
1,98x è misurato su job da 48 frame; su job più lunghi il guadagno può essere
inferiore. Partire da 2 socket, misurare, poi valutare il terzo. Non usare
questo per giustificare l'overlap in-daemon: è un'altra cosa e crasha.

## 6. L4 — Tassa fissa per frammento (P1)

**Obiettivo:** ridurre i ~300–400 ms per frammento quando il frammento dura
1–3 s. Misure storiche: startup profilo ~345 ms, `plan_decode_validate` ~10 ms,
`plan_compile` ~20 ms, encoder open ~104 ms, encoder close ~155 ms. Su un job
da 24 frame (~650–713 ms) è il 40–50% del tempo.

**Cosa c'è e cosa manca**

- `PREPARE_PLAN` esiste sul filo (`ipcCommandPreparePlan = 2` in
  `RenderingGen/renderinggen/internal/chronon/ipc.go:36`) ma il client non lo
  invia mai. Il handler Chronon (`ipc_prepare_plan`,
  `daemon_service_ipc_dispatch.cpp:31`) richiede una **composition id**
  registrata, non un plan path: il producer RenderingGen usa plan path. Serve
  una variante `PREPARE_PLAN <plan_path>` (o un id di composizione) prima di
  poterlo usare davvero.
- `RENDER_BATCH` esiste lato daemon (`decode_render_batch_payload`, max 64) ma
  il client RenderingGen manda un `RENDER_JOB` per item. Un batch per piano
  riusa il piano e il daemon caldo.
- La sessione encoder è per job: open/close ~259 ms. La sessione persistente
  cross-fragment è il pezzo grosso ed è P4, non un micro-fix.

**Passi**

1. Estrarre dal receipt il breakdown per frammento (startup, compile, open,
   encode, close, hash, publish). Non stimare: il numero decide quale delle
   tre leve vale.
2. Se domina il compile: aggiungere `PreparePlan` al client IPC e il comando
   per plan path; inviarlo una volta per piano prima del primo render.
3. Se domina l'open/close: prototipare la sessione persistente solo per
   frammenti dello stesso piano, con un test che dimostri la chiusura corretta
   e l'assenza di leak.
4. Se domina il numero di job: passare a `RENDER_BATCH` per i frammenti dello
   stesso piano, mantenendo l'ordine e la certificazione per item.

**Gate:** mediana del wall per frammento ridotta; nessuna regressione di
certificato; test di concorrenza e di chiusura.

**Rischio:** `PREPARE_PLAN` non è un comando drop-in: il contratto attuale è
per composition id. Non dichiarare la leva disponibile prima di aver aggiunto
la variante e il test.

## 7. L5 — Meno frammenti (P2)

**Obiettivo:** ridurre il numero di job quando il montaggio lo consente.

**Cosa c'è**

`finalJobComposePlanSegments` (`final_job_compiled_plan.go:369`) taglia la base
attorno a ogni finestra overlay e aggiunge un segmento per overlay;
`finalJobMergeContiguousSegments` (`:424`) fonde solo segmenti contigui dello
stesso asset e della stessa finestra sorgente. Nel caso r49: 85 frammenti,
272,8 s / 85 ≈ 3,2 s per frammento. **Non** è il numero della r100: il JSON
r100 non espone il conteggio dei frammenti.

**Nota 2026-11-03.** Questi frammenti sono finestre disgiunte **dello stesso
piano composto**, quindi la classe di ammissione corretta è
`ChunkedDisjointSamePlan`, non `DifferentPlan`; e l'overlap dentro un daemon è
stato misurato fallire (`Chronon3d/docs/evidence/gpu-admission-two-jobs-2026-11-02.md`
§3: stallo a 75 s, morte del daemon), quindi la leva restante è
**per-processo** (più runtime, 1,98× misurato), non per-slot. Non pianificare
N frammenti in parallelo dentro lo stesso daemon finché quel gate non passa.

**Passi**

1. Contare i frammenti della run corrente dal piano compilato, non dal
   `script_run` (che è un campo diverso).
2. Valutare solo fusioni **provabilmente** a pixel identici: stesso asset,
   timeline contigua, sorgente contigua. La regola attuale è già questa; non
   allargarla a un gap, perché il gap è contenuto diverso.
3. Ridurre il numero al producer: comporre più overlay in un pass o allungare
   le finestre overlay, con un gate di pixel equality su un golden.
4. Misurare frammenti, byte, wall e cache hit prima/dopo.

**Gate:** pixel equality sul golden; nessun cambio di timeline; wall e byte
ridotti.

**Rischio:** una fusione scorretta cambia le immagini. Il gate pixel è
obbligatorio, non opzionale.

## 8. Ordine di esecuzione consigliato

1. **L0** (ore): chiude il debito del 02-11 e rende il ledger onesto.
2. **L3 con 2 socket** (ore): unico guadagno già misurato; misura il before/after
   sulla r100, non su un bench separato.
3. **L1** (giorni): instrumentare, poi contratto al producer. È la leva che
   elimina l'encode, non solo lo serializza meglio.
4. **L2** (giorni): misurare `prepare_join`, poi overlap o cache/reuse.
5. **L4** (giorni): partire dal breakdown reale; non implementare tutte e tre
   le sotto-leve insieme.
6. **L5** (settimane): solo con gate pixel e dopo che L1/L2 hanno ridotto il
   numero di encode.

## 9. Gate globali di accettazione

- `go build ./...` e `go test` sui package toccati; per Chronon il target del
  test di ammissione.
- Stessa run r100 prima/dopo: confrontare `timing.stages`,
  `segment_prep` (copy/encode/cache/reuse), `overlay_render`,
  `remote_final_job`, `handoff`, VRAM di picco.
- Nessun cambio di certificato: `profile_id = VELOX_ASSEMBLY_READY_V1`,
  `copy_eligible`, `closed_gop`, `frame_count`, `padded_frames` solo sul
  deficit misurato.
- Ledger aggiornato con comando + esito osservato. Un test verde su fixture
  non chiude un gate di throughput: sono livelli di evidenza diversi.

## 10. Rischi trasversali

| Rischio | Mitigazione |
|---|---|
| Tre daemon su una GPU saturano NVENC o la VRAM | partire da 2, misurare; VRAM ~764 MiB/job, 16 GB disponibili |
| Overlap in-daemon di FullGraph | non abilitarlo: hang/EOF misurati; il fix lo rifiuta |
| Copy-window non raggiungibile | il gate è del producer; non abbassare il predicato |
| `PREPARE_PLAN` non è drop-in | aggiungere la variante plan-path e il test prima di usarlo |
| Fusione frammenti cambia i pixel | golden pixel equality obbligatorio |
| Misura su shape diverso | rimisurare sulla r100, non solo sul bench 48-frame |

## 11. Riferimenti

- `ops/jobs/garlasco_editorial_e2e_20261030/job-full-r100.json` — timing r100.
- `freebuff-chat-2026-11-02T10-48-25.391Z.md` — misura GPU, 1,98x, hang/EOF,
  fix di ammissione.
- `refactored/docs/operations/TICKET-FINAL-JOB-BITSTREAM-REUSE-2026-11-01.md` —
  r49 → r78, copy/cache, payload.
- `refactored/docs/operations/TICKET-CLOSEOUT-LEDGER-2026-10-29.md` §7 — stato
  dei 12 punti e gate dei tre bloccati.
- `Chronon3d/docs/evidence/` — manca `gpu-admission-two-job-2026-11-02.md`.
- `RenderingGen/renderinggen/config.yaml` — `gpu_lanes`, `socket_path`,
  `encode_preset: p1`.
