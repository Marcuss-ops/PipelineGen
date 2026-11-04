# Ledger di chiusura ticket — 2026-10-29

Scopo: un solo posto con lo stato reale dei ticket aperti, l'evidenza di ciò
che è stato chiuso in questa sessione, ciò che richiede ancora lavoro di codice
e ciò che è bloccato da evidenza esterna (GPU, host autorizzato, CI sullo
stesso SHA). Un ticket non si chiude perché il suo codice compila: si chiude
quando il criterio di accettazione dichiarato è verificato.

> **Aggiornamento 2026-10-31.** Le sezioni §1–§2 sono la fotografia del 29-10.
> Dopo quella data: `OVERLAY_RENDER_PER_SCENE` è **default-on** (`d94109fab`),
> l'epoch cache è stabile e le tre run `closeout-20261030-r{1,2,3}` danno
> mediana **77,32 item/min** (65–70 item, senza mappe; gate ≥100 ancora FAIL).
> Il final job R14 ha chiuso l'handoff HTTPS con 11 frammenti e audio muxato.
> Restano aperti il mix 100 item con mappe, il contratto multi-artefatto
> final-job e il gate ≥100.

## 1. Chiuso in questa sessione, con evidenza

### 1.1 `make verify-agent` — di nuovo verde

> **Aggiornamento 2026-11-03.** `verify-overlay-model-alias`, la facade
> `model_alias.go` e `cmd/overlay-model-alias-gen` sono stati rimossi; gli
> import puntano direttamente a `internal/capabilities/overlays/model`. Le
> righe seguenti restano come evidenza storica del 29-10.

Il gate falliva su `verify-overlay-model-alias`: il facade generato
`refactored/internal/capabilities/overlays/model_alias.go` era disallineato dal
pacchetto `overlays/model` (mancava l'alias `ManifestPlacement`).

- Rigenerazione: `go run ./cmd/overlay-model-alias-gen --generate` (+1 riga,
  nessuna modifica manuale).
- Drift re-check: `go run ./cmd/overlay-model-alias-gen` → `facade in sync`.
- Gate completo: `make verify-agent` → `verify_exit=0`. Foundation (incluso
  gitleaks), static (`go vet ./...`, `go build ./...`) e
  `verify-changed-components` passano; 17 componenti da cache, 3 eseguiti.

Nota operativa: ~5 minuti del gate sono la scansione gitleaks su ~10.5 GB, non
un hang. Non va interrotta.

### 1.2 Partizione overlay in micro-batch (punto 3 di §9.9)

`groupManifestCandidatesIntoMicroBatches` in
`internal/capabilities/scripts/render_queue_split.go`: dentro ogni scena, gli
item con finestre letterali sovrapposte formano una componente connessa e
compongono insieme; componenti disgiunte diventano batch separati in ordine di
piano. Il percorso `OVERLAY_RENDER_PER_SCENE` è **default-on** dal 30-10
(`d94109fab`); il flag resta un kill switch esplicito.

- Test: `TestMicroBatchesNeverSplitOverlappingItems` (overlap unito, disgiunto
  separato, ordine di piano preservato).
- Suite mirata: `go test ./internal/capabilities/scripts -run
  'MicroBatch|GroupManifest|PlanSpansMultipleScenes|EnqueueChrononPlanSplit|Packed|SplitManifest'`
  → PASS.

**Limite esplicito:** è correttezza offline della partizione, non una misura di
throughput né una parità pixel-level. Nessun guadagno viene dichiarato.

### 1.3 Attribuzione per batch e verifiche locali dei punti 1–4

`cmd/goldensummary` separa attesa in coda, servizio, attesa del producer e lane
GPU, con frame/tempi/byte di upload quando presenti; sugli snapshot r4–r6 tutti
i batch risultano attribuiti. `go test ./cmd/goldensummary -count=1` → PASS.
Il test sintetico del mix materializza 100 item; i test di territorio e
ammissione mappe passano.

Restano **non chiusi** e non vanno dichiarati verdi: A/B GPU reale sulle
partizioni, benchmark reale dei job dedicati sovrapposti, tre run del golden
completo con mappe, gate ≥100 item/min.

### 1.4 Scandione secrets del dev loop — da 233,76 s a 2,77 s (misurato)

`scripts/ci/ci-no-secrets-audit.sh` eseguiva `gitleaks detect --source .`, cioè
leggeva tutto il working tree: **11,83 GB in 233,76 s**, quasi tutti artefatti
sotto `data/` (11,9 GB) che non entrano mai in un commit. Ora lo script
materializza in una directory temporanea i soli file che potrebbero essere
committati (`git ls-files -co --exclude-standard`: tracciati + untracked non
ignorati, 6221 file, ~50 MB di contenuto) e scansiona quella.

| Misura | Prima | Dopo |
|---|---|---|
| Dati scansionati | 11,83 GB | 49,16 MB |
| Tempo (`/usr/bin/time`) | 233,76 s | 2,4–2,6 s (2,77 s alla prima misura, prima della correzione di §1.5) |
| Esito su repo pulito | PASS | PASS (`make verify-no-secrets` verde) |

**Verifica che continui a intercettare:** probe end-to-end con una generic api
key in un file untracked non ignorato (`secret-scan-probe.md`) → script esce
**1** e il file compare nel reperto; il probe rimosso riporta PASS. Nota: con
gitleaks 8.30 le regole `ghp_...`/`AKIA...` provate non scattano su valori a
bassa entropia, la regola generic-api-key e la chiave privata RSA sì.

**Cambio di semantica, esplicito:** i file ignorati (`.env`, `*.tmp`, output di
render) non sono più scansionati. Il ramo fallback dello stesso script li
escludeva già e non possono essere committati senza `git add -f`; chi vuole
coprire quel caso deve farlo prima del force-add.

### 1.5 Il gate secrets era un no-op: difetto pre-esistente corretto

Verificando la modifica 1.4 con un probe end-to-end è emerso un difetto più
grave della lentezza: `refactored/.gitleaks.toml` conteneva solo un blocco
`[allowlist]`, senza `[[rules]]` e senza `[extend] useDefault = true`. Una
config così **sostituisce** il set di regole built-in con zero regole, quindi
`gitleaks` usciva `0` su qualunque input. Il gate `verify-no-secrets` non
poteva fallire dal 2026-10-16, cioè da quando la config è stata introdotta.

- Evidenza: probe con una generic api key in un file non ignorato → **rc=0**
  prima della correzione, **rc=1** dopo.
- Effetto collaterale scoperto abilitando le regole: 616 finding su repo
  pulito, **tutti** nello stesso artefatto generato
  `ops/benchmarks/a0_bbox_census_20261028.json` (regola `generic-api-key` su
  identificatori esadecimali), nessuno in codice sorgente.
- Correzione: `[extend] useDefault = true` più un allowlist **per percorso**
  di quell'unico file generato. La regola resta attiva su tutto il resto; non
  è un'esclusione per regola né un allargamento dei pattern esistenti.

Stato finale del gate: rileva (probe rc=1), verde su repo pulito (rc=0),
scope 49,16 MB invece di 11,83 GB, 2,4–2,6 s invece di 233,76 s.
`make verify-agent` completo: **PASS** (`verify_exit=0`).

## 2. Raggiungibile con lavoro di codice, non ancora fatto

Questi non richiedono GPU per essere *implementati*; richiedono GPU solo per
l'accettazione finale. Sono i candidati naturali per la prossima sessione.

| Ticket | Cosa manca | Come si chiude |
|---|---|---|
| `refactored/docs/operations/TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md` | A/B GPU, 3 run golden con mappe | §9.9.3 punto 4 e §9.9.4: run su host idle, poi mediana ≥100 item/min |
| `refactored/docs/operations/TICKET-OVERLAY-RENDER-PER-SCENE-2026-10-29.md` §11 | Overlap coperto; restano video background sincronizzato, filtri con storia temporale, transizioni | Verifica pixel-level per ciascun caso, poi promozione del flag |
| `Chronon3d/docs/tickets/TICKET-ADD-LOADER-FOR-CHRONON-JSON.md` | Loader JSON Chronon mancante | Implementare il loader + test di caricamento, poi chiudere il forward-point |
| `Chronon3d/docs/tickets/TICKET-TEXT-QUALITY-V2.md` | P1-3 e P2 aperti | Implementare P1-3/P2; P1-1 e P1-2 sono già nel codice |
| `Chronon3d/docs/tickets/TICKET-LIGHTS-SHADOWS-PARTICLES.md` | Scope luci/ombre scelto il 2026-10-08, PARTIAL | Completare lo scope scelto e verificarlo su render |
| `Chronon3d/docs/tickets/TICKET-SVG-TEXT-ADVANCED-SCOPES.md`, `TICKET-SVG-TEXT-PARITY-REMAINING.md` | In progress | Chiudere gli scope residui e allineare la parità SVG text |
| `Chronon3d/docs/tickets/TICKET-COMPILER-BOUNDARY-WAVE7-9.md`, `TICKET-COMPILER-DEMOLITION-WAVES.md`, `TICKET-HOTSPOT-DECOMPOSITION.md`, `TICKET-SHAPE-FILL-COMPAT.md` | ACTIVE | Completare le wave dichiarate nel ticket e allegare l'evidenza |

## 3. Bloccati da evidenza esterna (non chiudibili qui)

Il criterio residuo è una misura su GPU/host o una CI su SHA esatto. Chiuderli
senza quella misura sarebbe un claim falso. Il motivo è quello dichiarato dal
ticket stesso.

| Ticket | Blocco |
|---|---|
| `Chronon3d/docs/tickets/TICKET-100X-PERF-WAVES.md` | B2 sotto il gate 300 render-loop fps; sidecar NVENC null; golden MTSDF e gate VRAM non verificati |
| `Chronon3d/docs/tickets/archive/TICKET-PERFORMANCE-AUDIT-2026-10.md` | Misure native GPU/CI esterne |
| `Chronon3d/docs/tickets/TICKET-AUDIT-ROADMAP-WAVES.md` | CI exact-SHA ed evidenza render GPU |
| `Chronon3d/docs/tickets/TICKET-MAIN-TRUTH-STAGE-1-2.md` | Certificazione same-SHA non verde |
| `Chronon3d/docs/tickets/TICKET-FFMPEG-PIPE-SINK-DEMOLITION.md` | Encoder pipe diagnostico ancora presente; gate nativi non verdi |
| `Chronon3d/docs/tickets/TICKET-ZERO-COPY-ZWAVES.md` | Certificazione GPU host-dependent |
| `Chronon3d/docs/tickets/TICKET-SCALE-P0-ASSET-VRAM-GOP.md` | Certificazione native-image |
| `Chronon3d/docs/tickets/TICKET-CLI-STRICT-GPU-NATIVE-MAPPING.md` | Verifica GPU-host same-SHA della strict lane |
| `Chronon3d/docs/tickets/TICKET-AUDIT-2026-09-12-REMEDIATION.md` | Chiusura same-SHA da certificare |
| `Chronon3d/docs/tickets/TICKET-200-stage-plan.md` | Stato `VERIFY`: serve l'esecuzione dello stage plan |

## 4. Design-only: nessun codice da chiudere

`TICKET-LAYER-CONTENT-REF.md` (`PROPOSED`), `ADR-DRAFT-HUD-STRUCTURED-BUFFER.md`
e `ADR-DRAFT-NODE-CONTEXT-OWNERSHIP.md` (`DRAFT`), ADR-024/025/031/032/033 in
`PROPOSED`. Restano aperti **per scelta**, non per mancanza di lavoro: si
chiudono con una decisione (accept/reject), non con un commit di codice.

## 5. Procedura per chiudere un ticket bloccato

1. Predisporre l'host: GPU idle, topologia lane dichiarata, versioni worker e
   renderer annotate, cache nota.
2. Eseguire il comando di accettazione dichiarato nel ticket (per l'overlay:
   `ops/jobs/overlay_throughput_golden_100_20261022/run_golden_v3.sh <label>`,
   tre label distinte).
3. Conservare payload e hash, versioni, mix materializzato, snapshot job,
   summary, errori e fallback, verifica artefatti/frame/timing.
4. Calcolare la statistica richiesta (per l'overlay: mediana delle tre run
   comparabili). PASS solo se il criterio è soddisfatto **e** i controlli di
   parità precedenti sono verdi; altrimenti FAIL/BLOCKED con causa nominata.
5. Aggiornare la riga di stato del ticket con la data, il comando eseguito e
   l'esito; non promuovere a CLOSED un ticket con un criterio non misurato.

## 6. Regola di onestà del ledger

- `CLOSED` richiede: criterio dichiarato + comando eseguito + esito osservato.
- `BLOCKED` richiede: criterio residuo nominato + perché non è eseguibile qui.
- Un test verde su fixture non chiude un gate che parla di throughput o di
  pixel: sono livelli di evidenza diversi e vanno tenuti separati.

## 7. Audit architetturale esterno — stato verificato al 2026-11-02

Mappa unica dei 12 punti dell'audit (PipelineGen + RenderingGen + Chronon3d) sul
codice corrente. «Chiuso» significa che il criterio è nel codice e pinnato da un
test; «bloccato» significa che il criterio vive fuori da questi tre repository o
richiede una misura su GPU.

| # | Punto | Stato | Evidenza |
|---|---|---|---|
| 1 | Preparazione troppo tardiva | **Parziale** | `prepareFinalJobVideoSegments` è ancora obbligatorio in `final_job_payload.go:433`; cache, riuso e copy-window hanno rimosso il costo per le finestre già calde. |
| 2 | Identità contenuto = identità segmento | **Bloccato** | `derivePlacement` rimuxa e pubblica un contenitore distinto (`final_job_placement.go`); il vincolo è del Master (r55/r66). |
| 3 | Concorrenza GPU | **Chiuso 2026-11-02 con misura** | Misurati due job batch indipendenti: multi-runtime **1,98×**, ~764 MiB/job, artefatti identici; nello stesso daemon gli stessi job in `parallel_disjoint` mandavano in stallo (stesso piano) o uccidevano il daemon (piani diversi). Fix: `extract_plan_admission_shape` rifiuta il dominio condiviso ai piani compositi (`daemon_service_ipc_protocol.cpp`, `daemon_service_ipc_render_job.cpp`). Evidenza: `Chronon3d/docs/evidence/gpu-admission-two-jobs-2026-11-02.md`. |
| 4 | Hash ripetuti sulla stessa sorgente | **Chiuso** | `digest.Verifier` memoizza per path+size+mtime con singleflight (`kernel/digest/verifier.go`); `stagedSource` non ri-hasha a ogni finestra. |
| 5 | Probe concorrenti duplicati | **Chiuso** | `sourceCensus` passa da `censusFlights.Do` (`final_job_source_census.go`): un `ffprobe` per SHA. |
| 6 | Cache copy/encode | **Chiuso** | `fragmentWorkKey` include `copy-enabled`/`encode-only` e `finalJobFragmentFormatVersion = 9`. |
| 7 | Conversioni e trasferimenti ripetuti | **Parziale** | Sorgente una volta (`lockSource`), frammenti in cache, `rememberFragmentPath`; il piazzamento duplicato resta remux+publish. |
| 8 | Finalizzazione per segmento sequenziale | **Chiuso** | Pool bounded a 4 con ordine di piano preservato (`RenderingGen/.../segments.go`). |
| 9 | Contratti video distribuiti | **Parziale** | Dentro PipelineGen `finalJobDelivery` deriva da `kernelmedia.DefaultAssemblyMediaContract`; RenderingGen e Chronon hanno ancora i propri registry. |
| 10 | Segmentazione subordinata al layout | **Non verificabile qui** | Il mux segmentato vive nel worker remoto, fuori da questi tre repo. |
| 11 | Congelamento frame con `tpad` | **Chiuso 2026-11-02** | `finalJobFragmentVideoFilter` aggiunge `tpad` solo per il deficit misurato dal census; `padded_frames` è nel receipt per finestra e per handoff. |
| 12 | Metriche aggregate contaminate | **Chiuso** | Contatori per scope handoff (`final_job_fragment_pipeline.go`, già `final_job_segment_stats.go`). |

**Correzione all'audit — profilo del frammento.** L'audit afferma che il profilo
dei frammenti è tornato `VELOX_OVERLAY_VIDEO_ONLY_V1`. Non è così: in
`final_job_segment_prepare.go` `finalJobSegmentProfile` è
`kernelmedia.AssemblyMediaContractID` (V1) perché r90 è fallita con
`manifest binding is not exact` quando il frammento portava il profilo
video-only. `VELOX_OVERLAY_VIDEO_ONLY_V1` resta corretto per `plan.Output` e per
i batch manifest (r80–r83); non per il certificato del frammento. L'audit ha
confuso le due identità.

**Gate dei tre punti bloccati (non si chiudono con un test su fixture):**

1. *Preparazione a monte* — criterio: una run in cui `segment_prep` non esegue
   alcun encode per finestra (`encode = 0`, `copied + reused + cached = 100%`)
   con le sorgenti prodotte dallo step di render, non dal final job. Richiede il
   cambio del producer, fuori da questi repo.
2. *Multi-placement* — criterio: due piazzamenti dello stesso SHA referenziano
   **un** asset e il Master accetta il piano (nessun
   `manifest binding is not exact`). Richiede il contratto dell'assembler.
3. *GPU* — **chiuso il 2026-11-02** (evidenza
   `Chronon3d/docs/evidence/gpu-admission-two-jobs-2026-11-02.md`). Misura: due
   job FullGraph 1080p indipendenti su due runtime caldi, **1,98×** (1 476 ms
   seriali → 1 040 ms concorrenti), **~764 MiB per job** (1 527 MiB per la
   coppia). `admitted_slots ≥ 2` è stato **osservato solo dentro un daemon**, e
   l'esito è il fallimento — stallo con lo stesso piano, morte del processo con
   piani diversi — perché una richiesta senza `composition_required` veniva
   chargiata come DirectYUV (1 GiB) mentre il piano composito eseguiva su
   FullGraph. Il floor 8,8 GB **non** è stato abbassato: non era il collo di
   bottiglia. Fix pinnato: un piano composito non entra mai nel dominio
   condiviso, qualunque hint dichiari (`extract_plan_admission_shape`),
   verificato live: i due job completano serializzati e il rifiuto è loggato;
   31 casi / 177 asserzioni verdi in
   `chronon3d_daemon_render_concurrency_tests`. La concorrenza reale resta
   multi-runtime, che è già la topologia del deployment
   (`socket_paths` con tre socket).

Il taglio del 02-11 ha anche unificato l'uscita dei quattro esiti del frammento
(`finishFragment`: reuse, cache, derivato, prodotto) e rimosso 9 funzioni del
pool dead-code; dettagli in `deadcode-residuals-classification-2026-10-28.md`.
Verifica eseguita: `go build ./...`, `go test ./internal/app/wiring
./internal/capabilities/scripts`, `go test ./internal/processor` in RenderingGen.
