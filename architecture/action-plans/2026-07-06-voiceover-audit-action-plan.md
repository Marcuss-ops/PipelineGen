# Voiceover Audit Action Plan — 2026-07-06

> **Origin**: Audit architetturale del sottosistema voiceover condotto il 2026-07-06
> usando la checklist di fragilità architetturale / complessità / performance /
> dead code. L'analisi completa è nel messaggio dell'agente in conversazione.
>
> **Regola operativa**: ogni azione atterra come commit diretto su `origin main`
> (NO branches, NO PR, NO `--force`). Per AGENTS.md Git-Lesson-2 + Git-Lesson-3.
>
> **Convenzione trailer**: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`

---

## Stato pre-action-plan

| Metrica | Valore |
|---|---|
| File production nel sottosistema | ~55 file Go |
| Port dichiarati | 13+ interfacce (Pattern 0) |
| File più grandi | `types.go` (644), `finalizer.go` (539), `ports.go` (507) |
| Import infra diretti | 2 violazioni (`orphan_sweeper.go`, `upload_intent.go`) |
| Path duplicati | 2 pipeline coesistenti (batch legacy + use case moderno) |

---

## Azione #1 — 🔥 PRIORITÀ ASSOLUTA: Migrare il path batch legacy al PipelineExecutor

**Deadline**: 2026-07-15
**File coinvolti**: `process.go`, `stages.go`, `stage_synthesize.go`, `stage_destination.go`, `stage_finalize.go`, `stage_postprocess.go`, `stage_persist.go`
**Categoria**: Dead code elimination + DRY

**Problema**: Due pipeline coesistono nel package voiceover:
- **Path batch legacy**: `Service.Generate` → `GenerateBatch` → `processLanguage` → `synthesizeStage` / `destinationStage` / `finalizeStage` (NON usa PipelineExecutor, NON ha dedupe gate + media_assets projection + cleanup outbox)
- **Path use case moderno**: `GenerateVoiceoversUseCase.Execute` → `processOneLanguage` → `PipelineExecutor.RunPipeline` (ha TUTTI i 6 step del finalizer)

**Azione**: Modificare `process.go::processLanguage` per delegare a `PipelineExecutor.RunPipeline` invece di chiamare i 3 stage inline. Dopo la migrazione, i 3 stage file legacy (`stage_synthesize.go`, `stage_destination.go`, `stage_finalize.go`) e i 2 forward-pointer (`stage_postprocess.go`, `stage_persist.go`) diventano dead code e vanno rimossi.

**Gap colmati dal PipelineExecutor**:
- Dedupe gate (Step 1 del finalizer) — assente nel path batch legacy
- `media_assets` projection (Step 4) — assente nel path batch legacy
- `voiceover.cleanup.requested` outbox (Step 6) — assente nel path batch legacy

**Verification gates**:
```bash
gofmt -l internal/application/voiceover/
go vet ./internal/application/voiceover/...
go build ./internal/application/voiceover/...
go test -short -count=1 ./internal/application/voiceover/...
```

**Rischio**: MEDIO. Il path batch è usato da `Generate`, `GenerateWithDestination`, `GenerateBatch`, e `GeneratePromo`. I test esistenti in `service_test.go` devono continuare a passare.

---

## Azione #2 — 🔥 PRIORITÀ ASSOLUTA: Introdurre port per orphan_sweeper.go

**Deadline**: 2026-07-15
**File coinvolti**: `orphan_sweeper.go`, `orphan_sweeper_test.go`, `internal/app/lifecycle_adapters.go`
**Categoria**: I/O Binder fix (Pattern 0)

**Problema**: `orphan_sweeper.go:68` importa direttamente `internal/infrastructure/database/sqlite/scripts` (package concreto), violando il Pattern 0.

**Azione**: 
1. Dichiarare un port `UploadIntentsRepository` in `voiceover/ports.go` (l'interfaccia esiste già in `orphan_sweeper.go`! Basta spostarla in `ports.go` e renderla esportata)
2. Spostare l'adapter concreto (`uploadIntentsAdapter`) in `internal/app/lifecycle_adapters.go`
3. Rimuovere l'import `internal/infrastructure/database/sqlite/scripts` da `orphan_sweeper.go`
4. Aggiungere `var _ UploadIntentsRepository = (*uploadIntentsAdapter)(nil)` compile-time pin

**Verification gates**:
```bash
rg 'internal/infrastructure' internal/application/voiceover/orphan_sweeper.go
# deve restituire 0 hit (solo commenti)
gofmt -l internal/application/voiceover/orphan_sweeper.go
go vet ./internal/application/voiceover/...
go build ./internal/application/voiceover/...
go test -short -count=1 -run TestOrphanSweeper ./internal/application/voiceover/...
```

---

## Azione #3 — 🔴 ALTA: Introdurre port per upload_intent.go

**Deadline**: 2026-07-18
**File coinvolti**: `upload_intent.go`, `upload_intent_test.go`, `internal/app/lifecycle_adapters.go`
**Categoria**: I/O Binder fix (Pattern 0)

**Problema**: `upload_intent.go:73` importa direttamente `internal/infrastructure/database/sqlite/scripts` (package concreto).

**Azione**: 
1. Verificare se esiste già un port `UploadIntentsRepository` in `orphan_sweeper.go` e riutilizzarlo
2. Spostare l'adapter concreto in `internal/app/lifecycle_adapters.go`
3. Rimuovere l'import diretto da `upload_intent.go`

**Verification gates**:
```bash
rg 'internal/infrastructure' internal/application/voiceover/upload_intent.go
# deve restituire 0 hit
gofmt -l internal/application/voiceover/upload_intent.go
go build ./internal/application/voiceover/...
go test -short -count=1 -run TestUploadIntent ./internal/application/voiceover/...
```

---

## Azione #4 — 🔴 ALTA: Rimuovere campo morto TransactionalOutbox da UseCaseDeps

**Deadline**: 2026-07-18
**File coinvolti**: `usecase.go`, `internal/app/build_bundles_voiceover.go`
**Categoria**: Dead code elimination

**Problema**: `UseCaseDeps.TransactionalOutbox` è marcato "RETAINED but unused post-DRY". Il campo è tenuto solo per non rompere il composition root.

**Azione**:
1. Rimuovere il campo `TransactionalOutbox` da `UseCaseDeps`
2. Rimuovere il panic nil-check nel costruttore
3. Aggiornare `build_bundles_voiceover.go` per non passare più il campo
4. Verificare che il composition root compili

**Verification gates**:
```bash
gofmt -l internal/application/voiceover/usecase.go internal/app/build_bundles_voiceover.go
go build ./internal/application/voiceover/...
go build ./internal/app/...
go test -short -count=1 -run TestGenerateVoiceoversUseCase ./internal/application/voiceover/...
```

---

## Azione #5 — 🟡 MEDIA: Rimuovere o documentare FilenameBuilder port non usato

**Deadline**: 2026-07-22
**File coinvolti**: `process_voiceover_item.go`, `ports.go`, `filename_builder.go`
**Categoria**: YAGNI cleanup

**Problema**: `FilenameBuilder` port è dichiarato come richiesto nel costruttore di `ProcessVoiceoverItemUseCase`, ma il path per-item NON lo usa (fida di `item.Filename` pre-computato dal fanout). Il commento dice "port surface kept stable for future BACKFILL stages" — gancione preventivo YAGNI.

**Azione**: 
1. Opzione A (preferita): rendere `FilenameBuilder` nil-safe (non più required), spostare il panic in warn log
2. Opzione B: rimuoverlo del tutto se nessun test/caller lo usa
3. Documentare la decisione in un commento

**Verification gates**:
```bash
gofmt -l internal/application/voiceover/process_voiceover_item.go
go build ./internal/application/voiceover/...
go test -short -count=1 -run TestProcessVoiceoverItem ./internal/application/voiceover/...
```

---

## Azione #6 — 🟡 MEDIA: Estrarre adapter voiceover in file separati

**Deadline**: 2026-07-22
**File coinvolti**: `internal/app/adapters_voiceover_use_case.go` (609+ LOC)
**Categoria**: Modularità / manutenibilità

**Problema**: `adapters_voiceover_use_case.go` contiene TUTTI gli adapter concreti per voiceover in un unico file da 609+ linee. Questo è corretto per Pattern 0 ma il file è un mini god-object.

**Azione**: Splittare in 5 file, uno per adapter:
1. `adapters_voiceover_tts.go` — `useCaseTTSAdapter` + `useCaseAudioAdapter`
2. `adapters_voiceover_publisher.go` — `useCasePublisherAdapter`
3. `adapters_voiceover_repo.go` — `useCaseRepoAdapter`
4. `adapters_voiceover_dest.go` — `useCaseDestResolverAdapter`
5. `adapters_voiceover_projection.go` — `voiceoverProjectionAdapter` + `voiceoverPostCommitVerifierAdapter`
6. `adapters_voiceover_drive.go` — `voiceoverDriveAdapter` (se non già in un altro file)

**Pattern**: AGENTS.md Pattern 5 (split per capability). Ogni file porta il proprio `var _ voiceover.<Port> = (*<Adapter>)(nil)` compile-time pin.

**Verification gates**:
```bash
gofmt -l internal/app/adapters_voiceover_*.go
go vet ./internal/app/...
go test -short -count=1 ./internal/app/...
```

---

## Azione #7 — 🟡 MEDIA: Estrarre finalizeStage post-commit verification in metodo separato

**Deadline**: 2026-07-25
**File coinvolti**: `stage_finalize.go`
**Categoria**: Complessità ciclomatica

**Problema**: `finalizeStage` è ~160 LOC con 8+ branch (begin tx, finalize, commit, verify nil, verify reconciliation, verify warn, status update). La verifica post-commit è ~60 linee di logica inline.

**Azione**: Estrarre il blocco di post-commit verification (righe dopo `tx.Commit()`) in un metodo privato `verifyPostCommit(ctx, item, verifyErr)` che restituisce `(BatchItem, bool)`.

**Verification gates**:
```bash
gofmt -l internal/application/voiceover/stage_finalize.go
go build ./internal/application/voiceover/...
go test -short -count=1 -run TestFinalizeStage ./internal/application/voiceover/...
```

---

## Azione #8 — 🟢 BASSA: Registrare wave-tracker entry in architecture/current.yaml

**Deadline**: 2026-07-06 (oggi)
**File coinvolti**: `architecture/current.yaml`, `CHANGELOG.md`, `AGENTS.md`
**Categoria**: Bookkeeping / godlike/06 SSOT

**Azione**: Aggiungere l'entry `VOICEOVER-AUDIT-2026-07-06` in `architecture/current.yaml` con 7 `linked_issues` (una per azione #1-#7), deadline per banda, e exit_gate. Aggiornare CHANGELOG.md e AGENTS.md in lockstep.

**Verification gates**:
```bash
grep -c 'VOICEOVER-AUDIT-2026-07-06' architecture/current.yaml
# deve restituire >= 1
```

---

## Azione #9 — 🟢 BASSA: Standardizzare textHash (64 vs 16 char) in un unico tipo

**Deadline**: 2026-07-29
**File coinvolti**: `stages.go`, `planner.go`, `texthash.go`, `finalizer.go`, `persistence/repository.go`
**Categoria**: Primitive obsession residual

**Problema**: Due rappresentazioni di textHash convivono: `string` raw a 64 char (legacy batch path, `hashutil.SHA256String`) e `TextHash` typed a 16 char (`ComputeTextHash`, per-item path). Questo causa commenti lunghi ovunque per spiegare la differenza.

**Azione**: 
1. Rendere `TextHash` l'unica rappresentazione canonica (con metodo `String()` e `Full()` per i 64 char)
2. Migrare il path batch a usare `ComputeTextHash` (16 char) — il DB column accetta entrambe le lunghezze
3. Eliminare l'import di `internal/infrastructure/files` da `stages.go`

**Verification gates**:
```bash
rg 'hashutil\.SHA256String' internal/application/voiceover/
# deve restituire 0 hit in file production (non test)
gofmt -l internal/application/voiceover/
go build ./internal/application/voiceover/...
go test -short -count=1 ./internal/application/voiceover/...
```

---

## Ordine di esecuzione consigliato

```
Azione #8 (wave-tracker)   ← oggi, 5 minuti, bookkeeping
    ↓
Azione #1 (PipelineExecutor migration)  ← la più impattante, sblocca #7 e #9
    ↓
Azione #4 (TransactionalOutbox dead)   ← dipende da #1 (il campo è "unused post-DRY")
Azione #2 (orphan_sweeper port)        ← indipendente
Azione #3 (upload_intent port)         ← indipendente
    ↓
Azione #6 (split adapter file)         ← indipendente
Azione #7 (extract verify method)      ← dipende da #1
Azione #5 (FilenameBuilder YAGNI)      ← indipendente
    ↓
Azione #9 (textHash standardize)       ← dipende da #1
```

---

## Riepilogo per banda

| Banda | Azioni | Deadline |
|---|---|---|
| 🔥 P0 absolute | #1 (PipelineExecutor), #2 (orphan_sweeper port) | 2026-07-15 |
| 🔴 Alta | #3 (upload_intent port), #4 (TransactionalOutbox dead) | 2026-07-18 |
| 🟡 Media | #5 (FilenameBuilder), #6 (adapter split), #7 (verify extract) | 2026-07-25 |
| 🟢 Bassa | #8 (wave-tracker), #9 (textHash standardize) | 2026-07-29 |

---

## godlike/07 honest-limitation

1. **Azione #1 è la più rischiosa**: tocca il path batch usato da `Generate`, `GenerateWithDestination`, `GenerateBatch`, `GeneratePromo`. I test in `service_test.go` sono la rete di sicurezza.
2. **L'analisi è statica**: basata su complessità percepita e dimensione file. Il cross-reference `git log --since=90.days` (forward-pointer `PR-VO-HOTSPOT-CROSSREF`) va eseguito per validare.
3. **Pre-existing build issues**: le 5 issue pre-esistenti in `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` NON sono regressioni di queste azioni e vengono portate avanti.
