# DataServer — Decomposition Priorities (2026-07-06)

> **Status**: 0 commit landed — questo doc è un action plan basato sull'audit
> snapshot del 2026-07-06 sul repo `Marcuss-ops/VeloxEditiingg` (DataServer).
>
> **Sister doc**: `docs/plans/2026-07-03-dataserver-action-plan.md` (17 commit
> già atterrati su `main` HEAD `700751088` — copertura feature + delivery plan,
> NON decomposition).
>
> **Tipo**: refactor pure code-motion — niente nuove feature, niente
> semantic changes. Lookup paths `X.Y` preservati per tutti i caller
> esistenti via same-package visibility.

---

## TL;DR

6 priorità di decomposition, in ordine di esecuzione consigliato:

1. **`enqueue.go`** — centro di gravità, mescola 6 responsabilità (orchestrazione, normalizzazione, payload compat, asset rewriting, delivery-plan rules, identity/fingerprint)
2. **`handler_test.go`** — abbassa rumore dei test, rende i refactor successivi più sicuri
3. **`bootstrap_composition.go`** — composition root che sta ri-accumulando (`appComponents`, `routerBundle`, `buildAppComponents`, `wirePostBuild`, `buildSupervisor`)
4. **`sqlite_finalize_writer.go`** — refactor chirurgico con regola TX dura (**un solo `BeginTx`, un solo `Commit`, un solo `Rollback`**)
5. **`script/handler.go`** — cleanup light (route registration + registry ingress + bypass creator + flag parsing + DB loader)
6. **`drive/service.go`** — cleanup light (token + HTTP wrapper + files + folders + permissions)

**Verdetto netto**: il primo file da spezzare è
``DataServer/internal/jobs/enqueue/enqueue.go``. Subito dopo
``DataServer/internal/handlers/server/script/handler_test.go`` perché renderà
il resto della zona script/enqueue più doloroso man mano che cresce.

---

## File che NON si toccano subito

Già ben separati e non a rischio di accumulo — restano **byte-stabili**:

- `DataServer/cmd/server/bootstrap.go` — refactor 939 → ~200 già chiuso (godoc lo dichiara)
- `DataServer/internal/jobs/enqueue/enqueue_clips.go` — delega a normalizer + timeline builder
- `DataServer/internal/jobs/enqueue/clip_input_normalizer.go` — copre 3 input shapes
- `DataServer/internal/jobs/enqueue/narrated_clip_timeline.go` — singola responsabilità
- `DataServer/internal/jobs/enqueue/enqueue_slideshow.go` — piccolo

---

## Action Plan — Task cliccabili (in ordine)

### Fase 1: `enqueue.go` split [PRIORITÀ MASSIMA]

Centro di gravità dell'enqueue. Mescola 6 responsabilità che continueranno a
crescere ad ogni nuovo job type.

- [ ] **1.1** — Snapshot baseline: `wc -l DataServer/internal/jobs/enqueue/enqueue.go` + `git log --since=90.days -- DataServer/internal/jobs/enqueue/` per confermare il centro di gravità
- [ ] **1.2** — `enqueue.go` slim orchestrator (Enqueuer, NewEnqueuer, Enqueue, PrepareJobAndTask)
- [ ] **1.3** — `compile.go` (compileSceneVideoJob, executor, capabilities)
- [ ] **1.4** — `payload_scene_video.go` (normalizeSceneVideoPayload, normalizeScenes, normalizeVoiceoverList)
- [ ] **1.5** — `response.go` (buildSceneVideoResponse, buildIdempotentResponse)
- [ ] **1.6** — `asset_rewrite.go` (resolveVoiceoverPayload, resolveSceneImagePayload, syncAudioURLFromVoiceover)
- [ ] **1.7** — `delivery_plan.go` (validatePlanPayload, extractPlanMaxRetry, PlanResolver, ResolvedPlan)
- [ ] **1.8** — `identity.go` (DeriveForwardingJobID, sceneVideoFingerprint)
- [ ] **1.9** — `errors.go` (validationError)
- [ ] **1.10** — Verifica gates: `gofmt -l` + `go vet ./internal/jobs/enqueue/...` + `go build ./...` + `go test -short -count=1 ./internal/jobs/enqueue/...`

**Pattern atteso (post-split)**:

```
DataServer/internal/jobs/enqueue/
  enqueue.go
  compile.go
  payload_scene_video.go
  response.go
  asset_rewrite.go
  delivery_plan.go
  identity.go
  errors.go
```

### Fase 2: `handler_test.go` split [abbassa rumore test]

Il file è già carico: temp DB, seed destination, asset service config, mock
creator, route Gin, payload lunghi, assert multi-endpoint. Slow da leggere,
facile da duplicare.

- [ ] **2.1** — `handler_test.go` slim (solo integration test top-level ad alto livello)
- [ ] **2.2** — `handler_test_fixtures_test.go` (`newTestRouter`, `seedDestinationMain`, temp DB setup)
- [ ] **2.3** — `handler_creator_test.go` (test creator stage)
- [ ] **2.4** — `handler_clips_test.go` (generate-from-clips)
- [ ] **2.5** — `handler_slideshow_test.go` (slideshow-video)
- [ ] **2.6** — Estrarre 3 payload builder test-only (per evitare copia di blocchi enormi di mappe JSON):
  ```go
  func validSceneImagePayload() map[string]interface{}
  func validClipPayload() map[string]interface{}
  func validSlideshowPayload() map[string]interface{}
  ```
- [ ] **2.7** — Verifica gates: Go compile (test package) + `go test -short -count=1 -v ./internal/handlers/server/script/...`

### Fase 3: `bootstrap_composition.go` split [composition root]

La parte più rischiosa è `buildSupervisor` — registra runner critical,
restartable e one-shot nello stesso blocco. Separarli evita il mega-file di
ritorno.

- [ ] **3.1** — `bootstrap_composition.go` slim (appComponents + buildAppComponents linear)
- [ ] **3.2** — `bootstrap_routes_bundle.go` (routerBundle)
- [ ] **3.3** — `bootstrap_postbuild.go` (wirePostBuild)
- [ ] **3.4** — `bootstrap_supervisor.go` (buildSupervisor orchestrator)
- [ ] **3.5** — `bootstrap_supervisor_runners.go` (registerCritical, registerRestartable, registerOneShot)

### Fase 4: `sqlite_finalize_writer.go` split [chirurgico]

⚠️ **Regola dura**: un solo `BeginTx`, un solo `Commit`, un solo `Rollback`
nell'orchestrator. Gli helper devono continuare a ricevere `*sql.Tx`, **non**
aprire transazioni proprie. Il file è delicato: ha già step su 5 tabelle
(`jobs`, `tasks`, `artifacts`, `job_deliveries`, `artifact_uploads`).

- [ ] **4.1** — `sqlite_finalize_writer.go` slim (FinalizationWriter, SQLiteFinalizeWriter, FinalizeVerified orchestrator)
- [ ] **4.2** — `sqlite_finalize_precondition.go` (uploadCASPrecondition, loadUploadSessionForCASInTx, validateFinalizingUploadTx)
- [ ] **4.3** — `sqlite_finalize_job_task.go` (markJobSucceededTx, markTaskSucceededTx)
- [ ] **4.4** — `sqlite_finalize_artifact.go` (markArtifactReadyTx)
- [ ] **4.5** — `sqlite_finalize_delivery.go` (resolveDeliveryDestinationsTx, insertPendingDeliveriesTx)
- [ ] **4.6** — `sqlite_finalize_upload.go` (completeUploadTx)
- [ ] **4.7** — **Snapshot TX invariants PRIMA e DOPO il refactor**: 5 scenari happy-path + 3 failure-path, verificarne byte-equivalent execution

### Fase 5: `script/handler.go` split [light refactor]

Non è ancora enorme ma mescola: route registration, registry ingress,
`GenerateWithImagesHandler`, bypass creator logic, flag parsing, job status
handler, DB loader.

- [ ] **5.1** — `handler.go` slim (ScriptHandlers, NewScriptHandlers, RegisterRoutes)
- [ ] **5.2** — `generate_images.go` (GenerateWithImagesHandler)
- [ ] **5.3** — `ingress_registry.go` (newScriptIngressRegistry)
- [ ] **5.4** — `creator_bypass.go` (shouldBypassCreator, isTruthyFlag, firstStringValue)
- [ ] **5.5** — `jobs_handler.go` (ScriptJobHandler, ScriptByIDHandler, loadJob)

### Fase 6: `drive/service.go` split [light refactor]

Classico file che cresce male: token loading/refresh, HTTP request wrapper,
file listing, folder CRUD, sharing, metadata, delete.

- [ ] **6.1** — `service.go` slim (NewService, struct-level core only)
- [ ] **6.2** — `service_auth.go` (SetToken, getToken, LoadFirstToken)
- [ ] **6.3** — `service_http.go` (doAPIRequest)
- [ ] **6.4** — `files.go` (ListFiles, GetFileMetadata, DeleteFile, GetFileLink)
- [ ] **6.5** — `folders.go` (GetFolder, CreateFolder, GetOrCreateFolder)
- [ ] **6.6** — `permissions.go` (ShareFile)

---

## Regole di routing per ogni PR puro code-motion

- **NO nuovi exported symbols** — lookup path `X.Y` preservato per TUTTI i caller esistenti via same-package visibility
- **NO signature changes**, **NO dep changes**, **NO semantic changes**
- **Ogni PR auto-sufficiente** — passa `gofmt -l + go vet + go build + go test -short` sul proprio subtree
- **NO branch, NO `--no-ff`** — commit diretto su `main` (AGENTS.md §Git-Lesson-2)
- **Co-authored-by trailer** obbligatorio (AGENTS.md §Git-Lesson-3)
- **Race-protect** via `git fetch origin && git log --oneline HEAD..@{u}` (AGENTS.md §Git-Lesson-4)
- **Byte-equivalent-replay recovery** se race (AGENTS.md §Git-Lesson-5)

---

## Ordine di esecuzione raccomandato

1. `enqueue.go` — massima priorità, mix di responsabilità (cresce ad ogni nuovo job type)
2. `handler_test.go` — abbassa rumore test, rende safe i refactor successivi
3. `bootstrap_composition.go` — composition root + supervisor (rischio ri-accumulo)
4. `sqlite_finalize_writer.go` — chirurgico, con TX invariants before/after
5. `script/handler.go` — cleanup light
6. `drive/service.go` — cleanup light

---

## Cross-references

- **`docs/plans/2026-07-03-dataserver-action-plan.md`** — sister action plan, 17 commit landed (feature + delivery plan)
- **`docs/plans/2026-07-03-porting-analysis-enqueue-clips.md`** — porting analysis di `enqueue_clips.go` (sorella minore per la zona enqueue)
- **AGENTS.md §Git-Lesson-2/3/4/5** — workflow rules per commit diretto su `main` con race protection
- **AGENTS.md §Pattern 5** (Splittare un package, Giugno 2026 v2) — regola corretta per split multi-file

---

## Nota housekeeping (FUORI scope di questo doc)

Nel CWD `C:\Users\pater\Pyt\PipelineGen` ci sono 3 file **untracked**:

- `internal/application/assets/sourcing/youtube/adapters.go`
- `internal/application/assets/sourcing/youtube/errors.go`
- `internal/application/assets/sourcing/youtube/register_helpers.go`

Sono probabilmente leftover del commit
`refactor(youtube): split service.go 511 LoC into 4 files per Pattern 5`
(snapshot HEAD `1eaad3b5`, già **superseded** da `3642f904`). Fuori scope di
questo action plan: da verificare in un cleanup separato (probabilmente
furono creati localmente ma mai `git add`-ati prima del commit sul main).

L'audit del 2026-07-06 NON copre questi 3 file — si riferisce a `DataServer`,
non a PipelineGen.
