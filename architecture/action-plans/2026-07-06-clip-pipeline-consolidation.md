# CLIP-PIPELINE-CONSOLIDATION-2026-07-06 — Consolidamento Pipeline Clip YouTube

## §0 — Context

Audit architetturale del 2026-07-06 sul flusso di caricamento clip YouTube in PipelineGen.
L'analisi ha rivelato **due implementazioni separate** dello stesso flusso logico
che convivono nel codebase con contratti, formati clipID e strategie di dedup diversi.

**I due path:**
1. **`youtube/service.go::Register()`** (Path 1 sync + Path 2 batch async via worker)
   — 450-line god method, 14 step sequenziali, clipID `yt_<videoID>_<hash8>`
2. **`process_segment.go::Execute()`** (Path 3 channel monitor)
   — 9 step decomposti, retry, ffprobe, clipID `yt_<videoID>_<start>_<end>_v1`

**Problemi identificati (8):**

| # | Categoria | Problema | Severità |
|---|-----------|----------|----------|
| P1 | Duplicazione | Due pipeline che fanno la stessa cosa con contratti diversi | CRITICAL |
| P2 | Complessità | `Register()` god method 450+ linee, complessità ciclomatica esplosiva | HIGH |
| P3 | Primitive Obsession | `clipID`, `videoID`, `fileHash`, URL, timestamp tutti `string`/`float64` | HIGH |
| P4 | Fragilità | 21+ siti `strings.Contains(err.Error(), ...)` invece di typed errors | HIGH |
| P5 | Stato Globale | Package-level `sync.Mutex`/`sync.Map` in `semantic_enricher.go`, `search_service.go`, `detach.go` | MEDIUM |
| P6 | Legacy Accumulation | 178+ riferimenti legacy/deprecated, 93+ TODO marker | MEDIUM |
| P7 | I/O Bloccante | Path 1 sincrono sul thread HTTP (mitigato dal batch async Path 2) | LOW |
| P8 | Import Cycle | `FIX-IMAGES-ROUTING-CYCLE` documentato (carry-forward) | LOW |

## §1 — Ordine di Esecuzione (Priority-Ordered)

Basato su impatto × frequenza (matrice della checklist architetturale), l'ordine
di attacco è:

```
P2 (decomponi Register) → P1 (Unifica pipeline) → P3 (Typed primitives)
    → P4 (Typed errors) → P5 (Stato globale) → P6 (Legacy cleanup)
    → P7 → P8 (forward-pointer)
```

### Perché questo ordine:
- **P2 prima**: decomporre `Register()` in use case separati è il prerequisito per P1 (unificazione). Non puoi unificare ciò che non è decomposto.
- **P1 subito dopo**: una volta che entrambi i path usano use case separati, l'unificazione diventa meccanica.
- **P3 prima di P4**: i typed primitives rendono più facile scrivere typed errors (hai un tipo a cui attaccare i sentinels).
- **P5-P8**: cleanup progressivo, non bloccante per i primi 4.

## §2 — Per-PR Execution Checklist

### Wave A: Decomposizione God Method (P2)
Deadline: 2026-07-13

**PR-CLIP-DECOM-1** — Estrai `ResolveClipMetadata` use case
- Estrarre da `Register()`: URL parsing, videoID extraction, metadata population, durata
- Files: NEW `internal/application/assets/sourcing/youtube/usecase/resolve_metadata.go`
- Ports: nessuno (pure logic)
- Test: 5 TDD (valid URL, invalid URL, empty name fallback, description truncation, duration calc)

**PR-CLIP-DECOM-2** — Estrai `DownloadAndHashClip` use case
- Estrarre da `Register()`: fetch yt-dlp, MD5 hash, derive clipID
- Files: NEW `internal/application/assets/sourcing/youtube/usecase/download_hash.go`
- Ports: FetchProviderPort (esistente), hash computation
- Test: 4 TDD (happy path, fetch error, hash error, empty hash fallback)

**PR-CLIP-DECOM-3** — Estrai `PublishClipToDrive` use case
- Estrarre da `Register()`: resolve folder, upload Drive via Publisher
- Files: NEW `internal/application/assets/sourcing/youtube/usecase/publish_drive.go`
- Ports: PublisherPort (esistente)
- Test: 4 TDD (happy path, publisher error, nil publisher, metadata upload)

**PR-CLIP-DECOM-4** — Estrai `PersistClipAndIndex` use case
- Estrarre da `Register()`: DB save via dispatcher + outbox event
- Files: NEW `internal/application/assets/sourcing/youtube/usecase/persist_index.go`
- Ports: IndexDispatcherPort (esistente)
- Test: 4 TDD (happy path, dispatcher nil, write error, rich metadata fields)

**PR-CLIP-DECOM-5** — Refactor `Register()` a orchestratore sottile
- Sostituire il corpo di `Register()` con chiamate ai 4 use case sopra
- Il metodo deve scendere sotto le 80 righe (da 450)
- Test: i test esistenti di `Register()` devono continuare a passare
- godlike/07 minimum-blast-radius: zero cambiamento di comportamento

### Wave B: Unificazione Pipeline (P1)
Deadline: 2026-07-20

**PR-CLIP-UNIFY-1** — Allineare formato clipID
- Decisione: adottare `yt_<videoID>_<start>_<end>_v1` come formato canonico
- Il formato `yt_<videoID>_<hash8>` del Path 1 va deprecato
- Aggiungere migrazione SQL per backfill (forward-pointer)
- Aggiungere `ClipID` typed primitive qui

**PR-CLIP-UNIFY-2** — Condividere `ProcessYouTubeSegmentUseCase` tra i due path
- Far sì che `youtube/service.go::Register()` usi `ProcessYouTubeSegmentUseCase.Execute()` internamente
- Il Path 1 mantiene la stessa API esterna (RegisterClipCommand → RegisterClipResult)
- Internamente, la logica di download/validazione/persistenza è UNIFICATA
- Test: tutti i test esistenti di entrambi i path devono passare

**PR-CLIP-UNIFY-3** — Unificare strategia di dedup
- Sostituire `FindExisting(videoID, URL, start, end)` con `Cache.GetExisting(clipID)`
- La cache hit short-circuit di process_segment diventa il percorso canonico
- Il Force flag del Path 1 mappa a StrategyReplace del Path 3

**PR-CLIP-UNIFY-4** — Unificare contratti di errore
- `Register()` deve usare `ExtractionError` tipizzato invece di `fmt.Errorf` generici
- Il typed error contract di process_segment diventa canonico
- Aggiungere typed sentinels mancanti: `ErrClipAlreadyExists`, `ErrDriveUploadBlocked`

### Wave C: Typed Primitives (P3)
Deadline: 2026-07-27

**PR-CLIP-TYPE-1** — `ClipID` typed primitive
- Nuovo file: `internal/domain/asset/clip_id.go`
- `type ClipID string` con `func ParseClipID(s string) (ClipID, error)`, `func (c ClipID) VideoID() string`, `func (c ClipID) PolicyVersion() string`
- Sostituire tutte le occorrenze di `clipID string` con `ClipID`
- godlike/06 SSOT: `ClipID` vive SOLO in `internal/domain/asset/clip_id.go`

**PR-CLIP-TYPE-2** — `VideoID` e `FileHash` typed primitives
- `type VideoID string` + `type FileHash string`
- Costruttori validanti: `ParseVideoID`, `ParseFileHash`
- Sostituzione progressiva nei DTO e port

**PR-CLIP-TYPE-3** — `SegmentTimestamp` typed primitive
- `type SegmentTimestamp float64` con validazione `IsValid()`, `DurationTo(SegmentTimestamp) time.Duration`
- Sostituisce `float64` in `RegisterClipCommand`, `ProcessSegmentCommand`, `Segment`

### Wave D: Typed Errors (P4)
Deadline: 2026-08-03

**PR-CLIP-ERR-1** — Sostituire `strings.Contains(err.Error(), ...)` nei file Drive
- File target: `uploader_ops.go` (linee 523, 582), `uploader.go`
- 4 siti → typed sentinels (`ErrDriveFileNotFound`, `ErrDrivePermissionDenied`)

**PR-CLIP-ERR-2** — Sostituire nei file Artlist
- File target: `pixabay.go:224-225`, `downloader/*.go`
- 3 siti → typed sentinels

**PR-CLIP-ERR-3** — Sostituire nei file middleware e API
- File target: `middleware_validation.go:38`, `handler_reprocess.go:34`
- 3 siti → typed sentinels

**PR-CLIP-ERR-4** — Aggiungere CI gate Check 63
- `scripts/ci-architectural-checks.sh`: nuovo check che vieta `strings.Contains(err.Error(),`
- Allowlist per i siti rimanenti con forward-pointer
- godlike/08 transitional baseline con owner + deadline

### Wave E: Stato Globale + Legacy Cleanup (P5, P6)
Deadline: 2026-08-17

**PR-CLIP-CLEAN-1** — Spostare `enrichMetaMu` dentro la struct del servizio
- Da `var enrichMetaMu sync.Mutex` package-level a campo `mu sync.Mutex` su `*Service`
- Test: nessun cambiamento di comportamento

**PR-CLIP-CLEAN-2** — Spostare `searchL1`/`metadataL1` dentro la struct
- Da `var searchL1 sync.Map` package-level a campo su `*SearchService`
- Injection-ready per test isolation

**PR-CLIP-CLEAN-3** — Rimuovere riferimenti legacy dormienti
- `sourcing.DrivePort` residui (già physical-retired, pulire i commenti)
- Legacy script endpoint references in documentazione
- TODO marker senza deadline → aggiungere deadline o rimuovere

**PR-CLIP-CLEAN-4** — `PR-CLIP-HOTSPOT-CROSSREF` — git-log frequency cross-validation
- `git log --since=90.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30`
- Verificare che nessun hotspot non coperto dal piano sia emerso
- Appender come slim-shape `linked_issues` se necessario

## §3 — Per-PR Git Workflow (Direct-to-Main)

Ogni PR atterra direttamente su `main` per AGENTS.md Git-Lesson-2:

```bash
git fetch origin
git rebase origin/main
# ... modifiche ...
git add <files>
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git push origin main  # NO --force, NO --force-with-lease, NO topic branches
```

## §4 — Verification Gates (per ogni PR)

```
gofmt -l <modified_files>     # deve essere vuoto
go vet ./<subtree>/...        # exit 0
go build ./<subtree>/...      # exit 0
go test -short -count=1 ./<subtree>/...  # PASS
```

## §5 — godlike/06 SSOT 4-Surface Lockstep

Per CANONICAL.md §1, ogni PR chiude su 4 superfici:
1. **Action plan** (questo file) — narrativa canonica
2. **`architecture/current.yaml`** — wave-tracker con `linked_issues` slim-shape
3. **`CHANGELOG.md`** — closure meta-entry
4. **`AGENTS.md`** — mirror entry

## §6 — Honest Scope-Lock (godlike/07)

- I 6 pre-existing build issues (`PRE-EXISTING-BUILD-ISSUES-2026-07-04`) NON sono regressions di nessuna PR di questo piano
- `FIX-IMAGES-ROUTING-CYCLE` rimane carry-forward — non bloccante per questo piano
- La convergenza completa dei due path (Wave B) è vincolata al completamento della Wave A
- Il formato clipID unificato richiede una migrazione SQL per i dati esistenti (forward-pointer in Wave B)

## §7 — Cross-References

- `architecture/current.yaml#CLIP-PIPELINE-CONSOLIDATION-2026-07-06` — wave-tracker anchor
- `architecture/current.yaml#GODOBJ-2026-07-03` — precedente decomposizione god-object (stesso pattern)
- `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04` — precedente typed-primitives (stesso pattern P3)
- `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` — carry-forward NON toccato
- AGENTS.md §Git-Lesson-2 — direct-to-main workflow
- AGENTS.md §Git-Lesson-3 — Co-authored-by trailer
- AGENTS.md §Pattern 0 + Pattern 5 — port abstraction + file split

## §8 — Lifecycle Audit-Trail

| Data | Evento |
|------|--------|
| 2026-07-06 | Action plan creato (questo commit). 17 PR in 5 wave. |
| 2026-07-13 | Deadline Wave A (5 PR) |
| 2026-07-20 | Deadline Wave B (4 PR) |
| 2026-07-27 | Deadline Wave C (3 PR) |
| 2026-08-03 | Deadline Wave D (4 PR) |
| 2026-08-17 | Deadline Wave E (4 PR) |
| 2026-08-22 | Wave-flip a `status: done / exit_signal: true` |

## §9 — Signature

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
AGENTS.md Git-Lesson-3.
