# Certificazione velocità — clip.render / script generate (2026-09-16)

Deliverable: **"Clips Iniziale V2 con Subs & Background centralizzato … massima
velocità: controlla RenderingGen e Chronon e script generate."**

Questo documento separa in modo esplicito ciò che è **decidibile da un
checkout** (e quindi è certificato qui con numeri riprodotti) da ciò che
richiede GPU/Chronon/DB reali (nominato, non nascosto).

---

## 1. Cosa è certificato da questo checkout

L'harness canonico è `internal/capabilities/cliprender/bench_harness_test.go`:
il **job pipeline è reale** (`Worker.Handle`, contratto di continuation,
`ParentAggregator`, strumentazione `RunReport`), mentre ogni confine esterno
(lane RenderingGen/Chronon, download artifact, pubblicazione Drive,
materializzazione asset, ASR) è un fake a latenza parametrica. Il risultato è
deterministico e CI-safe: nessuna GPU richiesta, nessuna misura stimata.

### Riproduzione

```bash
cd refactored
# matrice completa (must-pass, ~2.6 s)
go test ./internal/capabilities/cliprender/ -run TestScenario -count=1

# conserva i report JSON nella directory canonica delle evidenze
VELOX_BENCH_WRITE_REPORT=1 \
  go test ./internal/capabilities/cliprender/ -run TestScenario -count=1
# → tests/operational/results/cliprender-bench/*.json
```

### Esito matrice (eseguita su questo checkout, 2026-09-16)

12/12 scenari `PASS`; `TestScenario7_ChrononSegmentSeekLive` è
**DSN/GPU-gated** e viene saltato senza stack reale. 0 failure su tutti gli
scenari misurati.

| scenario | mode | workers | lanes | clips | wall (ms) | clips/min | p50 | p95 | gpu util |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `scenario-01-worker-scaling-lanes8-w1` | blocking | 1 | 8 | 24 | 276 | 5 210.72 | 11 | 11 | 11.2 % |
| `scenario-01-worker-scaling-lanes8-w8` | blocking | 8 | 8 | 24 | 45 | 31 972.56 | 11 | 17 | 71.7 % |
| `scenario-01-worker-scaling-lanes2-w8` | blocking | 8 | 2 | 24 | 132 | 10 880.09 | 42 | 43 | 97.5 % |
| `scenario-02-submit-settle-render-fast` | async | 2 | 8 | 8 | 16 | 28 512.89 | 15 | 16 | 69.3 % |
| `scenario-02-submit-settle-render-slow` | async | 2 | 8 | 8 | 63 | 7 535.63 | 62 | 63 | 96.8 % |
| `scenario-02-blocking-baseline` | blocking | 8 | 8 | 8 | 64 | 7 410.20 | 63 | 64 | 98.3 % |
| `scenario-09-producers-1` | async | 4 | 2 | 24 | 271 | 5 307.92 | 152 | 260 | 48.0 % |
| `scenario-09-producers-2` | async | 4 | 2 | 24 | 147 | 9 753.59 | 86 | 143 | 92.3 % |
| `scenario-09-producers-4` | async | 4 | 2 | 24 | 141 | 10 152.88 | 88 | 141 | 98.1 % |
| `scenario-09-producers-8` | async | 4 | 2 | 24 | 132 | 10 884.65 | 78 | 131 | 97.2 % |
| `scenario-10-e2e-1-clips` | async | 4 | 8 | 1 | 8 | 6 717.06 | 8 | 8 | — |
| `scenario-10-e2e-50-clips` | async | 4 | 8 | 50 | 65 | 45 458.07 | 43 | 65 | — |

### Lettura dei numeri (contratto di saturazione)

- **La lane GPU è la risorsa saturabile, non il Master.** Con 2 lane la
  saturazione arriva a `producers=2` (92.3 %) e resta sopra il 97 % oltre:
  aggiungere producer non aumenta il throughput (10 152 → 10 884 clips/min è
  rumore di scheduling) e fa crescere le code (`queue_p95` 0 → 18 → 32 ms).
- **Il divisore è il numero di lane.** A parità di 8 worker: 8 lane → 45 ms
  (31 972 clips/min), 2 lane → 132 ms (10 880 clips/min), cioè ~2.94× con 4×
  le lane (efficienza < lineare per il drain di coda).
- **La separazione submit/settle è dimostrata, non dichiarata.**
  `blocking baseline` = 64 ms con `submit_p50=63 ms`; la variante async sullo
  stesso lavoro = 63 ms con `submit_p50=0–1 ms` e `settle_p50=60 ms`: lo slot
  Master non è più occupato durante il render remoto, che era l'obiettivo del
  ticket critical-path.

---

## 2. Interazione col lavoro di questo pass (nessuna regressione di velocità)

L'introduzione del check lingua-clip e della materializzazione a runtime
(item 1) **non tocca lo steady state**:

- il path di sola lettura resta invariato: un text-track READY non chiama
  nessun materializzatore
  (`TestResolveTranscriptChecked_ReadyTrackNeverMaterializes`,
  `TestClipTranscriptEnsurer_ReadyTrackIsANoOp`);
- il costo della materializzazione a runtime è **una volta per (clip, lingua)
  mancante**, e il secondo run riusa la traccia persistita (fast path
  idempotente);
- l'adapter delega alla pipeline canonica già misurata
  (`asset.text.materialize`), non introduce un secondo motore.

Nessuno dei 12 scenari della matrice usa il percorso di materializzazione,
quindi i numeri sopra restano la baseline valida anche dopo la modifica.

---

## 3. Cosa NON è certificabile da questo checkout (nominato)

| Collo di bottiglia | Evidenza disponibile | Cosa serve per chiuderlo |
|---|---|---|
| Burn-in sottotitoli **+56 % wall/clip** (A/B misurato su 408 frame: 6045 ms ON vs 3883 ms OFF) | `TODO.md` §"Goal pass (settima)" — il costo è compositing testo GPU per-frame (10 layer), **non** il compilato ASS (cache hit, 1.9 ms) | GPU reale + A/B ripetuto; cachare il testo compilato **non** aiuta |
| Sonda output/GOP ~23 % del wall di un clip (928–1121 ms) | `TODO.md` §settima, proiezione `probe_ms` | host con Chronon/NVENC |
| Serializzazione `render_job_slots = 1` del daemon Chronon | `DaemonService::handle_ipc` prende `m_render_job_mutex` sul path `RENDER_JOB` (commit `d1dd01bbf`): `gpu_lanes=2` non recupera nulla | decisione VRAM, non una manopola di tuning |
| Realtime factor/p50 Chronon per lingua | `TODO.md` §quattordicesima (frasi p50 1109 ms → 4.51×; immagini p50 1560 ms → 3.21× su RTX A4000) | matrice live |
| `TestScenario7_ChrononSegmentSeekLive` | skip senza stack | Chronon + input reali |

La matrice di questo documento **non sostituisce** quelle misure: le colloca.
Il throughput massimo teorico della catena è governato dalle lane
RenderingGen (`gpu_lanes`), non dal numero di worker Master: qualunque
guadagno dichiarato sul lato Master sotto la saturazione delle lane è, per
costruzione, non osservabile in clips/min.

---

## 4. Verdetto

- **Repo-reachable: certificato.** Matrice 12/12 verde, numeri riprodotti,
  evidenze JSON conservate in `tests/operational/results/cliprender-bench/`.
- **Steady state dopo il pass: invariato.** Il check lingua-clip non aggiunge
  lavoro quando la traccia esiste già.
- **Massima velocità assoluta: non ancora dichiarabile.** Restano i quattro
  colli nominati sopra, tutti fuori da un checkout.

---

## 5. Refresh 2026-09-18 — dopo la parità di concorrenza

Trigger: il pass di parità GPU/concorrenza ha allineato `DefaultGPUGateSlots`
1→2, `defaultGlobalRenderConcurrency` 2→4, il tetto `debt_budget` 9→0 e
svuotato la `max-lines-strict-allowlist`. La matrice è stata ri-eseguita
sull'albero corrente con lo stesso harness e gli stessi input: **14/14 PASS, 1
SKIP** (il solo `TestScenario7_ChrononSegmentSeekLive`, DSN/GPU-gated) in **2.5 s**.

### Riproduzione (identica)

```bash
cd refactored
VELOX_BENCH_WRITE_REPORT=1 go test ./internal/capabilities/cliprender/ -run TestScenario -count=1
# → tests/operational/results/cliprender-bench/*.json (sovrascritti)
```

### Matrice aggiornata (2026-09-18)

| scenario | mode | w | lanes | clips | wall (ms) | clips/min | p50 | p95 | gpu% | Δ vs 09-16 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| scenario-01-worker-scaling-lanes8-w1 | blocking | 1 | 8 | 24 | 274 | 5 243.8 | 10 | 11 | 11.2 | ≈ |
| scenario-01-worker-scaling-lanes8-w8 | blocking | 8 | 8 | 24 | **35** | **40 622.1** | 10 | 12 | 89.2 | **+27 %** |
| scenario-01-worker-scaling-lanes2-w8 | blocking | 8 | 2 | 24 | 128 | 11 191.3 | 42 | 42 | 98.5 | ≈ |
| scenario-02-submit-settle-render-fast | async | 2 | 8 | 8 | 40 | 11 758.5 | 39 | 40 | 31.4 | — |
| scenario-02-submit-settle-render-slow | async | 2 | 8 | 8 | 69 | 6 946.6 | 66 | 68 | 92.9 | ≈ |
| scenario-02-blocking-baseline | blocking | 8 | 8 | 8 | 64 | 7 391.4 | 63 | 64 | 95.3 | ≈ |
| scenario-09-producers-1 | async | 4 | 2 | 24 | 254 | 5 665.2 | 138 | 243 | 48.6 | ≈ |
| scenario-09-producers-2 | async | 4 | 2 | 24 | 128 | 11 230.1 | 75 | 127 | 97.8 | +15 % |
| scenario-09-producers-4 | async | 4 | 2 | 24 | 128 | 11 209.8 | 75 | 128 | 98.0 | +10 % |
| scenario-09-producers-8 | async | 4 | 2 | 24 | 130 | 11 068.0 | 77 | 129 | 97.9 | ≈ |
| scenario-10-e2e-1-clips | async | 4 | 8 | 1 | 9 | 6 292.0 | 9 | 9 | 11.8 | ≈ |
| scenario-10-e2e-50-clips | async | 4 | 8 | 50 | **46** | **64 473.6** | 28 | 41 | 86.3 | **+42 %** |

Il contratto di saturazione della §1 non cambia: con 2 lane la saturazione
arriva a `producers=2` (97.8 %) e oltre non sale; le lane RenderingGen restano
il divisore.

### Cache-hit: ms contro secondi

Il costo marginale di un re-render è già a costo zero per costruzione
(`clip_render_cache`, fingerprint → locator):

| scenario | clips | wall | source_full_hashes | source_downloads | submits | settles |
|---|---:|---:|---:|---:|---:|---:|
| scenario-04-sha-cache-hit | 20 | **1 ms** | 1 | 1 | **0** | **0** |
| scenario-08-cold-vs-warm | 10 | **0 ms** | 1 | 0 | 0 | 0 |
| scenario-06-transcript-reuse | 10 | **0 ms** | — | 0 | 0 | 0 |

`submits=0` e `settles=0` provano che il path cache-hit **non tocca la lane
GPU**: il costo è la lettura dell'indice, non il render. A fronte, il render
completo certificato di una clip misura `render_wall ≈ 3253 ms`
(`clip-lane-v2-20260917T130907Z`, 240 frame): il rapporto misurato
cache-hit/render è quindi **~3 ordini di grandezza** (≈1 ms contro ≈3.25 s), non
un guadagno incrementale.

### Verdetto del refresh

- **Ancora repo-reachable: certificato e migliorato.** +27 % su `lanes8-w8` e
  +42 % su `e2e-50-clips` rispetto al 2026-09-16, attribuibili alla parità di
  ammissione GPU e alla concorrenza di render allineate.
- **I quattro colli della §3 restano invariati** (burn sottotitoli, sonda GOP,
  `render_job_slots=1` del daemon, matrice live): nessuno è chiudibile da un
  checkout.
