# Piano d'Azione — Consolidamento Clips & Metadata (PipelineGen)

> **Data**: 2026-07-04
> **Analisi basata su**: AGENTS.md methodology (godlike/06 SSOT, godlike/07 no-fake-availability, Pattern 0-11)
> **Metodologia**: Checklist code smell × 4 aree strutturali (Fragilità Architetturale, Complessità, Prestazioni, Dead Code)
> **Regola**: NO BRANCHES, ONLY MAIN, commit + push diretto su `origin/main`, trailer `Co-authored-by:`

---

## Riepilogo Analisi

Il sottosistema clips & metadata soffre di **accumulo di tipi duplicati** creati da 3 ondate migratorie consecutive mai completate, **due implementazioni** del servizio metadati con formule quality-score divergenti, e **proliferazione di adapter** che ora sono un costo di manutenzione.

### Problemi Identificati (ordinati per impatto)

| # | Problema | Categoria | Impatto |
|---|----------|-----------|---------|
| 1 | 7 tipi metadata duplicati (`ClipMetadataFile`, `ClipRichMetadata`, `ClipMetadata`, `CanonicalClipMetadata`, `ClipMetadataInput`, `BuildClipMetadataInput`, `ClipAsset`) | Dead Code + YAGNI | Ogni nuovo campo tocca 3-4 tipi |
| 2 | Due `MetadataService` (`youtube/metadata/` vs `youtube/usecase/`) con formule quality-score diverse | Duplicazione | Bug di scoring divergenti |
| 3 | Stub DRIVE-008 fail-closed ancora in produzione (3 metodi `UploadFile`/`UploadFileWithDescription`) | Dead Code | Codice morto compilato |
| 4 | 17+ file di adapter in `internal/app/` per clips/metadata — molti pass-through puri | Over-Engineering | Costo manutenzione |
| 5 | Scattering geografico: logica clip tocca 9 directory diverse | Feature Envy | Rallenta onboarding |

---

## Azioni (ordinate per priorità)

### 🔥 AZIONE 1 — Collassare `ClipMetadata` + `ClipRichMetadata` dentro `CanonicalClipMetadata`

**Target**: 7 tipi → 1 tipo canonico + 1 tipo input + 1 tipo file serialization helper
**File coinvolti**:
- `internal/application/youtube/dto/types.go` — `ClipRichMetadata` (15 campi), `ClipMetadata` (7 campi)
- `internal/application/youtube/dto/metadata_types.go` — `CanonicalClipMetadata` (14 campi), `ClipMetadataInput` (9 campi)
- `internal/application/youtube/dto/metadata_core.go` — `ClipMetadataFile` (30+ campi)
- `internal/application/youtube/usecase/metadata_service.go` — consumer di `ClipRichMetadata`
- `internal/application/youtube/usecase/metadata_service_write.go` — consumer di `ClipMetadataFile`
- `internal/application/youtube/adapters/metadata_service_helpers.go` — consumer di `ClipRichMetadata`
- `internal/application/youtube/adapters/metadata_helpers.go` — consumer di `ClipRichMetadata`
- `internal/application/youtube/dto/tagutil_semantic.go` — `FallbackClipRichMetadata`, `NormalizeClipRichMetadata`

**Cosa fare**:
1. Aggiungere i campi mancanti a `CanonicalClipMetadata` (Tags, SourceTags, ClipTags, SearchKeywords, People, CleanTitle, ShortTitle, CleanTranscript, EmbeddingText, Duplicate*)
2. Creare `type ClipRichMetadata = CanonicalClipMetadata` come alias zero-copy per i consumer legacy
3. Eliminare `ClipMetadata` struct (7 campi) — ridondante con `CanonicalClipMetadata`
4. Convertire `ClipMetadataFile` in helper function `func ClipMetadataFileFromCanonical(m CanonicalClipMetadata) ClipMetadataFile`
5. Aggiornare `FallbackClipRichMetadata` / `NormalizeClipRichMetadata` per operare su `*CanonicalClipMetadata`
6. Aggiornare `usecase/metadata_service.go` e `adapters/metadata_service_helpers.go` per usare `CanonicalClipMetadata`
7. `rg` audit: zero riferimenti a `ClipRichMetadata` e `ClipMetadata` come tipi distinti (solo through alias)
8. Rimuovere i type alias quando i consumer sono migrati

**Deadline**: 2026-07-18
**Stima**: 4-5 commit incrementali (un commit per fase)

---

### 🔥 AZIONE 2 — Unificare i due `MetadataService`

**Target**: `youtube/usecase/MetadataService` → fuso dentro `youtube/metadata/MetadataService`
**File coinvolti**:
- `internal/application/youtube/usecase/metadata_service.go` (~540 LOC) — LEGACY, da eliminare
- `internal/application/youtube/usecase/metadata_service_write.go` (~150 LOC) — LEGACY, da spostare
- `internal/application/youtube/metadata/service.go` (~280 LOC) — CANONICO, da espandere
- `internal/application/youtube/usecase/callbacks.go` — consumer del MetadataService legacy
- `internal/application/youtube/adapters/metadata_service_helpers.go` — consumer del MetadataService legacy
- `internal/application/youtube/adapters/service.go` — wiring

**Cosa fare**:
1. Portare `WriteClipMetadataFile` da `usecase/metadata_service_write.go` a `youtube/metadata/service.go` (rinominato `WriteClipMetadataFile`)
2. Portare `calculateQualityScore` legacy → sostituito dalla formula canonica 40/40/20 in `metadata/service.go` (già presente come `CalculateQualityScore`)
3. Identificare tutti i consumer del `usecase.MetadataService` e farli puntare a `metadata.MetadataService`
4. Verificare che `callbacks.go` e `metadata_service_helpers.go` compilino con il nuovo target
5. `rg audit`: zero riferimenti a `usecase.MetadataService`
6. `git rm` dei file legacy

**Deadline**: 2026-07-25
**Stima**: 2-3 commit

---

### ⚡ AZIONE 3 — Completare CUTOVER DRIVE-008 (rimuovere stub fail-closed)

**Target**: 3 stub `UploadFile`/`UploadFileWithDescription` che restituiscono `ErrLegacySurfaceRetired`
**File coinvolti**:
- `internal/app/clips_adapters_drive.go` — `clipsDriveAdapter.UploadFile`, `clipsDriveAdapter.UploadFileWithDescription`
- `internal/app/youtube_drive_legacy_adapter.go` — `sourcingDriveAdapter.UploadFileWithDescription`
- `internal/application/clips/ports.go` — `ClipDriveUploaderPort` (rimuovere `UploadFile` + `UploadFileWithDescription` dalla interfaccia)
- `internal/application/assets/sourcing/ports.go` — `DrivePort` (rimuovere `UploadFileWithDescription`)
- `internal/infrastructure/drive/errors.go` — `ErrLegacySurfaceRetired` (rimuovere)
- `architecture/deprecations.yaml` — aggiornare status DRIVE-008 a `removed`

**Pre-flight check OBBLIGATORIO**:
```bash
# Verificare che NESSUN caller production chiami questi metodi
rg '\.UploadFile\(' internal/application internal/api --glob '!**/*_test.go' | grep -v 'clips_adapters_drive\|youtube_drive_legacy\|assettransferclient\|worker/runner_manifest_test\|worker/runner_test\|clip_ops_test\|voiceover/qdrant_indexing_e2e_test'
rg '\.UploadFileWithDescription\(' internal/application internal/api --glob '!**/*_test.go'
```
Se il pre-flight mostra 0 caller: rimozione sicura.
Se mostra caller >0: prima migrare i caller a `delivery.Publisher.Publish`, POI rimuovere.

**Cosa fare**:
1. Pre-flight check
2. Rimuovere i metodi dalle interfacce port
3. Rimuovere i metodi dagli adapter
4. Rimuovere `ErrLegacySurfaceRetired` se nessun altro consumer
5. Aggiornare `architecture/deprecations.yaml#DRIVE-008` a `status: removed`

**Deadline**: 2026-07-18
**Stima**: 1-2 commit

---

### 📋 AZIONE 4 — Consolidare adapter clips in meno file

**Target**: 17+ file adapter → 6-8 file raggruppati per dominio
**File coinvolti** (in `internal/app/`):
- `clips_adapters_repo.go` (Repo + Voiceover + Image + Cleanup + Jobs + SourceResolver) → già raggruppato, OK
- `clips_adapters_drive.go` (Drive + MetaWriter) → OK
- `clips_ops_adapters.go` (CleanupPort + JobsPort) → merge in `clips_adapters_repo.go`
- `clips_adapters_cfg.go` → OK, standalone
- `clips_adapters_index.go` → OK, standalone
- `clips_adapters_artifact.go` → OK, standalone
- `clips_dispatcher_adapter.go` → OK, standalone
- `youtube_adapters.go` (ClipStore + MonitorsStore + ClipIndexer + Ollama + DriveFolderMgr + FolderMemory) → 6 adapter, OK
- `youtube_drive_legacy_adapter.go` (SourcingDrive + SourcingClipStore) → **MORIRÀ con Azione 3**
- `youtube_metadata_adapter.go` (Metadata + Enrichment + Config + Transcriber + Search + Hash + Logger) → 7 adapter, **da splittare**
- `youtube_fetch_adapter.go`, `youtube_publisher_adapter.go`, `youtube_enrichment_adapter.go`, `youtube_dispatcher_adapter.go`, `youtube_asset_mapper.go` → già split

**Cosa fare**:
1. Merge `clips_ops_adapters.go` dentro `clips_adapters_repo.go` (stesso dominio)
2. Rinominare `youtube_metadata_adapter.go` → split in 3 file: `youtube_metadata_adapter.go` (solo Metadata+Enrichment), `youtube_transcriber_adapter.go`, `youtube_sourcing_adapters.go` (Config+Search+Hash+Logger)
3. Verifica: ogni file adapter ha ≤ 3 adapter interni
4. `gofmt + go vet + go build` sul subtree `internal/app/`

**Deadline**: 2026-08-01
**Stima**: 2 commit

---

### ⚡ AZIONE 5 — Verificare e unificare quality-score formula

**Target**: Una sola formula `CalculateQualityScore` in tutto il codebase
**File coinvolti**:
- `internal/application/youtube/metadata/service.go::CalculateQualityScore` — formula CANONICA 40/40/20 pesata
- `internal/application/youtube/usecase/metadata_service.go::calculateQualityScore` — formula LEGACY `(transcript/2000)+(tags/10)+(duration/600)+(title/100)`
- `internal/application/youtube/adapters/metadata_helpers.go::calculateQualityScore` — TERZA variante legacy
- `internal/application/youtube/adapters/metadata_helpers.go::calculateHeuristicQualityScore` — QUARTA variante

**Cosa fare**:
1. Verificare che la formula canonica 40/40/20 (`CalculateQualityScore` in `metadata/service.go`) sia quella corretta
2. Sostituire tutte le altre 3 varianti con chiamate a `metadata.CalculateQualityScore`
3. Test: verificare che i valori di quality score siano nel range [0.0, 1.0] per clip reali

**Deadline**: 2026-07-18
**Stima**: 1 commit (parte dell'Azione 1 o 2)

---

### 📋 AZIONE 6 — Rimuovere `BuildClipMetadataInput` duplicato

**Target**: `usecase/segments_service.go::BuildClipMetadataInput` → `dto.ClipMetadataInput`
**File coinvolti**:
- `internal/application/youtube/usecase/segments_service.go` — definizione di `BuildClipMetadataInput`
- `internal/application/youtube/dto/metadata_types.go` — `ClipMetadataInput` canonico

**Cosa fare**:
1. Verificare che i campi di `BuildClipMetadataInput` siano un subset di `ClipMetadataInput`
2. Aggiungere eventuali campi mancanti a `ClipMetadataInput`
3. Sostituire `BuildClipMetadataInput` → `ClipMetadataInput` in tutti i consumer
4. `rg` audit: zero riferimenti a `BuildClipMetadataInput`
5. Rimuovere la definizione

**Deadline**: 2026-07-18 (parte dell'Azione 1)
**Stima**: 1 commit

---

### 📋 AZIONE 7 — Documentare tipi rimasti e loro ruolo nel `doc.go`

**Target**: Chiarezza per i futuri maintainer su quale tipo serve a cosa
**File**: `internal/application/youtube/dto/doc.go`

**Cosa fare**:
1. Aggiornare il doc.go con la mappa dei tipi sopravvissuti:
   - `CanonicalClipMetadata` — unico tipo output del builder
   - `ClipMetadataInput` — unico tipo input del builder
   - `ClipMetadataFile` — helper di serializzazione on-disk (NON un tipo di dominio)
   - `ClipAsset` — entità per il ClipAtomicWriter
   - `ExtractItem` — DTO risposta HTTP
2. Aggiungere commento "If you need a new metadata field, add it to CanonicalClipMetadata. All other types derive from it."

**Deadline**: 2026-07-18
**Stima**: 1 commit

---

## Sequenza di Esecuzione

```
AZIONE 1 (collapse tipi) ──┬── AZIONE 5 (unify quality-score) ──┬── AZIONE 2 (unify MetadataService)
                           │                                      │
                           ├── AZIONE 6 (remove BuildClipMetadataInput)
                           │
                           └── AZIONE 7 (update doc.go)
                                    │
AZIONE 3 (DRIVE-008 CUTOVER) ───────┤
                                    │
AZIONE 4 (consolidate adapters) ────┘
```

- **Blocco 1** (Azioni 1+5+6+7): Collapse tipi — eseguire in sequenza (ogni commit dipende dal precedente)
- **Blocco 2** (Azione 2): Unificazione MetadataService — dipende da Blocco 1
- **Blocco 3** (Azione 3): DRIVE-008 CUTOVER — indipendente, parallelizzabile
- **Blocco 4** (Azione 4): Adapter consolidation — indipendente, parallelizzabile

---

## Riepilogo per Categoria Code Smell

| Categoria | Stato Pre | Azioni | Stato Post |
|-----------|-----------|--------|------------|
| **Tipi Duplicati (YAGNI)** | 🔴 7 tipi per lo stesso concetto | 1, 6, 7 | 🟢 1 tipo canonico |
| **Duplicazione Logica** | 🔴 2 MetadataService | 2 | 🟢 1 servizio unificato |
| **Dead Code** | 🔴 3 stub fail-closed + tipi legacy | 3 | 🟢 Rimosso |
| **Over-Engineering** | 🟡 17+ file adapter | 4 | 🟢 ~10 file |
| **Feature Envy** | 🟡 Logica sparsa su 9 dir | 1, 2 | 🟡 Migliorato (6 dir) |
| **Primitive Obsession** | 🟡 4 formule quality-score | 5 | 🟢 1 formula |
| **Mancanza Astrazione** | 🟢 Pattern 0 ovunque | — | 🟢 |

---

## Regole di Esecuzione

1. **NO BRANCHES** — ogni commit atterra direttamente su `origin/main`
2. **Commit auto-sufficienti** — ogni azione è un commit atomico con `gofmt + go vet + go build` verde sul subtree target
3. **Trailer obbligatorio**: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`
4. **Push incrementale** dopo ogni commit:
   ```bash
   git -c user.email='agent@pipelinegen.local' \
       -c user.name='PipelineGen Agent' \
       commit -m '<subject>

   <body>

   Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
   git fetch origin && git rebase origin/main && git push origin main
   ```
5. **Race recovery**: se push rejected, diagnosticare con `git log --oneline HEAD..@{u}` e applicare Git-Lesson-4/5 (byte-equivalent-replay → accept; textual-conflict → rebase)
6. **Nessun `--force`** su `origin/main` — il fast-forward exit è sempre possibile
7. **Pre-flight audit obbligatorio** prima di ogni `git rm`: `rg <symbol> internal/ --glob '!**/*_test.go'` deve tornare 0 hit in produzione

---

## Wave-Tracker Entry

Live wave-tracker anchor (slim-schema per godlike/06): **`architecture/current.yaml#CLIPS-META-2026-07-04`**

Per-action SHAs land sui matching `linked_issues` slot. La wave flips to `status: done / exit_signal: true` quando tutte le 7 azioni sono completate e verificate.

---

## Author + sign-off

- **Author:** PipelineGen Agent
- **Date:** 2026-07-04
- **Owner:** architecture doc maintainer
- **Co-authored-by:** PipelineGen Agent `<agent@pipelinegen.local>` (per AGENTS.md Git-Lesson-3)
- **Commit (plan-only):** `docs(architecture): register CLIPS-META-2026-07-04 action plan — clips & metadata consolidation` (direct-to-main per AGENTS.md Git-Lesson-2)
- **Audit-pin canonical anchor:** `architecture/current.yaml#CLIPS-META-2026-07-04`
