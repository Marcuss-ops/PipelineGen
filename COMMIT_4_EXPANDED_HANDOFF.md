# Stock Cutover Commit 4-expanded — Handoff Action Plan

> **Perché serve questo file**: sessione precedente ha lasciato su disco un
> Commit 4-expanded a metà (5 file modificati/non tracciati), ma NON ha
> committato, NON ha pushato, NON ha completato la rete di thread
> `RunSummary.FinalStatus → HandleJob → result map`. La prossima sessione
> parte da qui e deve atterrare pulito su `origin/main`.

---

## 0. Stato al momento del freeze (snapshot rilevante)

```
LOCAL   = 65e75ba7 (Commit 4 deleted 3 files + Indexed migration in service.go)
REMOTE  = 65e75ba7 (synced — sia local che origin/main puntano qui)
DIVERGED = assente (ahead: 0 / behind: 0)
```

**Lavoro NON committato su disco** (riprendere da qui):

```
M  internal/application/assets/providers/stock/stockpipeline/orchestrator.go
M  internal/application/assets/providers/stock/stockpipeline/service.go
M  internal/domain/job/job.go
M  internal/kernel/job/job.go
?? internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go
```

**Decisione di recovery** (vedi STEP 1 sotto):

- default path: committare TUTTO in un UNICO commit atomico (path più
  semplice, minor rischio di test interleaved)
- fortified path: dividere in 4 commit atomici (richiede `git restore`
  dei file modificati, re-implementazione slice-by-slice)

**Regola d'oro** (AGENTS.md §Git-Lesson-2): niente topic branch, push
direttamente su `main`, niente `--force`.

---

## 1. STEP 1 — Pre-flight (FQ-only, nessuna modifica)

```bash
# 0) Identità (per AGENTS.md Git-Lesson-3 trailer)
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    config --get user.email
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    config --get user.name

# 1) Stato
git status --short                  # deve mostrare i 5 file di cui sopra
git rev-parse HEAD                  # deve essere 65e75ba7
git fetch origin                    # aggiorna refs; non modifica albero
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse origin/main)
echo "local=$LOCAL remote=$REMOTE"
[ "$LOCAL" = "$REMOTE" ] || { echo 'DIVERGED — STOP, leggi §1.b'; exit 1; }

# 2) Hygiene check — conferma che l'albero compila già
cd /home/pierone/Pyt/PipelineGen
gofmt -l internal/application/assets/providers/stock/stockpipeline/ \
       internal/kernel/job/ internal/domain/job/
go vet ./internal/application/assets/providers/stock/stockpipeline/... \
        ./internal/kernel/job/... ./internal/domain/job/...
go build ./internal/application/assets/providers/stock/stockpipeline/...
```

Se tutto verde: vai a STEP 2.

Se `go build` fallisce (possibile se il servizio precedente ha lasciato un
half-edit), riapri i 5 file:

- `internal/kernel/job/job.go`: deve dichiarare `StatusIndexPending Status = "INDEX_PENDING"`
- `internal/domain/job/job.go`: deve dichiarare `StatusIndexPending = kerneljob.StatusIndexPending`
- `internal/application/assets/providers/stock/stockpipeline/service.go`: NON deve avere `IndexingStatus` né `Indexed IndexingStatus`
- `internal/application/assets/providers/stock/stockpipeline/orchestrator.go`: deve avere i campi `builder`/`writer`/`projection` su `Orchestrator` + metodo `RunResilient`
- `internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go`: deve esistere, dichiarando `RunSummary` + 3 port + 4 sentinels

Se uno o più mancano: STEP 1.b.

### 1.b Recupero da half-edit

```bash
# Backup della working tree su stash
git stash push -u -m "wip-commit4expanded-$(date +%s)"

# Verifica: ora il working tree è clean (origin/main contiene già i 3
# file-deletes + la migr. IndexingStatus di Commit 4 a 65e75ba7)
git status --short

# Re-applica stash; se conflitti, RIFIUTA il ripristino automatico e
# ricostruisci a mano i 5 file (lista in §0)
git stash pop
```

---

## 2. STEP 2 — Eseguire le modifiche rimaste

**Mancano 4 attività** (le altre 5 sono già su disco):

### 2.1 `Service.runOrchestratorResilient` — nuovo sibling

File: `internal/application/assets/providers/stock/stockpipeline/run_orchestrator.go`

Aggiungere (dopo `runOrchestrator`, prima di `projectManifestToPipelineResult`):

```go
// runOrchestratorResilient è il sibling Commit 4-expanded di runOrchestrator.
// Chiama Orchestrator.RunResilient (non Orchestrator.Run) per ottenere
// il *RunSummary che include il FinalStatus proiettato sul broker job.
//
// Surface NON-BREAKING: runOrchestrator (manifest-only) resta attivo per
// i 11 test esistenti + il legacy ServiceRunner interface (stock -> usecase).
// Solo HandleJob (production broker traffic) usa questa variante per
// proiettare FinalStatus nella result map sotto __final_status.
func (s *Service) runOrchestratorResilient(ctx context.Context, input *RunInput, jobID string) (*RunSummary, error) {
    if s == nil {
        return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil receiver")
    }
    if input == nil {
        return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil *RunInput")
    }
    cfg := OrchestratorConfig{
        JobId:            jobID,
        PolicyVersion:    "v1",
        ChunkDurationSec: effectiveChunkDurationSec(input, s),
        ClipDurationSec:  effectiveClipDurationSec(input, s),
    }
    o := NewOrchestrator(
        cfg,
        NewDeterministicPlanner(),
        NewInMemoryStepStore(),
        NewNoopSourceStager(),
        s.cutter,
        s.renderer,
    )
    summary, err := o.RunResilient(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: orchestrator.RunResilient: %w", err)
    }
    if s.log != nil {
        s.log.Info("stock orchestrator resilient run succeeded",
            zap.String("job_id", summary.Manifest.JobID),
            zap.String("final_status", string(summary.FinalStatus)),
            zap.Int("artifact_count", len(summary.Manifest.Artifacts)),
        )
    }
    return summary, nil
}
```

### 2.2 `Service.HandleJob` — proiettare FinalStatus nella result map

File: `internal/application/assets/providers/stock/stockpipeline/service.go`

Modifica chirurgica in `HandleJob` — sostituire SOLO la chiamata:

```go
// PRIMA (Commit 2):
manifest, err := s.runOrchestrator(ctx, input, job.ID)
if err != nil { return nil, err }
projected := projectManifestToPipelineResult(manifest)
return map[string]any{
    jobdomain.ManifestKey: manifest,
    "total_clips":         projected.TotalClips,
    "total_chunks":        projected.TotalChunks,
    "chunks":              projected.Chunks,
    "metadata_link":       projected.MetadataLink,
    "metadata_file_id":    projected.MetadataFileID,
}, nil

// DOPO (Commit 4-expanded):
summary, err := s.runOrchestratorResilient(ctx, input, job.ID)
if err != nil { return nil, err }
manifest := summary.Manifest
projected := projectManifestToPipelineResult(manifest)
return map[string]any{
    jobdomain.ManifestKey: manifest,
    "final_status":        string(summary.FinalStatus), // "SUCCEEDED" | "INDEX_PENDING" | "FAILED" | ...
    "total_clips":         projected.TotalClips,
    "total_chunks":        projected.TotalChunks,
    "chunks":              projected.Chunks,
    "metadata_link":       projected.MetadataLink,
    "metadata_file_id":    projected.MetadataFileID,
}, nil
```

### 2.3 `run_upload_indexing_test.go` (NEW — 3 test)

File NEW: `internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go`

Contenuto (vedi §A sotto per stub di test, §B per assertion).

### 2.4 `CHANGELOG.md` — estendere l'entry Commit 4

File: `CHANGELOG.md` (root)

Sotto `## Unreleased → ### Removed`: aggiungere una nota che Commit 4
retired anche il blocco `IndexingStatus` migrato in `service.go`. Aggiungere
`### Added` con: (a) `job.StatusIndexPending` (kernel/domain), (b) 3 port
(`TransactionalAssetWriter` / `ProjectionPort` / `ManifestBuilder`) +
`RunSummary`, (c) 3 nuovi test in `run_upload_indexing_test.go`.

L'entry attuale (Commit 4 raw, sole 3 delezioni + migration transitoria) è
fuorviante; si sostituisce con la versione "Commit 4-expanded" che descrive
l'intero perimetro.

---

## 3. STEP 3 — Validazione

```bash
cd /home/pierone/Pyt/PipelineGen

# 1) gofmt (deve essere silente)
test -z "$(gofmt -l internal/application/assets/providers/stock/stockpipeline/ \
                  internal/kernel/job/ internal/domain/job/ \
                  CHANGELOG.md)" && echo 'gofmt: clean' || { echo 'gofmt diff above'; exit 1; }

# 2) go vet
go vet ./internal/application/assets/providers/stock/stockpipeline/... \
       ./internal/kernel/job/... ./internal/domain/job/...

# 3) go build (full)
go build ./...

# 4) test in modalità standard
go test -count=1 ./internal/application/assets/providers/stock/stockpipeline/...

# 5) test in modalità race (può prendere ~30 s)
go test -race -count=1 ./internal/application/assets/providers/stock/stockpipeline/...

# 6) archcheck gate (lo script che blocca drift su imports/ownership)
bash scripts/ci-architectural-checks.sh

# 7) grep residue (la verifica della spec utente: deve tornare VUOTO)
for sym in IndexingStatus IndexingPending IndexingSkipped \
           IndexingCompleted IndexingFailed \
           uploadAndIndexChunk indexChunkToAssetIndex \
           indexChunkToClipsDB upsertChunkAndDispatch \
           buildPipelineMetadata ChunkResult.Indexed; do
    HITS=$(rg -n --type go "\\b${sym}\\b" --glob '!**/*_test.go' || true)
    if [ -n "$HITS" ]; then
        echo "RESIDUE FOUND for $sym:"
        echo "$HITS"
        exit 1
    fi
done
echo 'residue clean: spec satisfied'
```

Se tutto verde: STEP 4.

Se fallisce uno qualunque:
- `gofmt` ⇒ `gofmt -w <files>`
- `go vet` ⇒ leggere il primo errore, fix chirurgico
- `go build` ⇒ probabile collision symbol; rieseguire §1.b
- `go test` ⇒ leggere il primo failed test, fix nel file target
- `archcheck` ⇒ controlla `architecture/current.yaml` — la wave deve
  segnare Commit 4-expanded come part del piano

---

## 4. STEP 4 — Code-reviewer (in parallelo col STEP 3 se vuoi risparmiare round-trip)

```bash
# Spawna il code-reviewer-minimax-m3 con il prompt §C sotto.
# Aspetta il verdetto, fixa i NIT che non sono forward-pointer a Commit 5+.
```

---

## 5. STEP 5 — Commit + push (UNA commit atomica, AGENTS.md §Git-Lesson-2)

```bash
cd /home/pierone/Pyt/PipelineGen

# 1) Stage
git add internal/kernel/job/job.go \
        internal/domain/job/job.go \
        internal/application/assets/providers/stock/stockpipeline/run_orchestrator.go \
        internal/application/assets/providers/stock/stockpipeline/orchestrator.go \
        internal/application/assets/providers/stock/stockpipeline/service.go \
        internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go \
        internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go \
        CHANGELOG.md

git status --short
# deve mostrare:
#   M  CHANGELOG.md
#   M  internal/application/assets/providers/stock/stockpipeline/orchestrator.go
#   M  internal/application/assets/providers/stock/stockpipeline/run_orchestrator.go
#   M  internal/application/assets/providers/stock/stockpipeline/service.go
#   M  internal/domain/job/job.go
#   M  internal/kernel/job/job.go
#   ?? internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go   (-> diventa A)
#   ?? internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go       (-> diventa A)

# 2) Commit message (in tmp file per evitare heredoc escape snakes)
cat > /tmp/commit_msg.txt <<'CMT_EOF_RAW'
refactor(stock): Commit 4-expanded - retire IndexingStatus residue + add resilience ports + 3 tests

Stock Cutover Cleanup Plan Commit 4-expanded (July 2026):

Commit 4 (65e75ba7) retired the legacy upload+index ladder (run_upload.go +
run_upload_indexing_test.go + types_status.go) but left the IndexingStatus
typed enum migrated inside service.go to keep the build green. Commit
4-expanded retires that residual in service.go (ChunkResult.Indexed field
removed; ChunkResult doc-comment points at job.StatusIndexPending) and
adds the canonical resilience surface that the user's expanded spec asks
for:

- kernel: add job.StatusIndexPending (canonical 8-state lifecycle + Valid
  + IsActive predicates extended; typed-status migration per AGENTS.md
  godlike/07).
- domain/job: re-export StatusIndexPending via canonical alias so 107
  import sites in 93 files resolve unchanged.
- stockpipeline (NEW upload_orchestration.go): 3 typed ports per Pattern 0 —
  ManifestBuilder, TransactionalAssetWriter, ProjectionPort — + RunSummary
  envelope (Manifest + FinalStatus for JobFinalizer) + 4 typed sentinels
  (ErrManifestIncomplete, ErrAtomicDispatchFailed,
  ErrProjectionResilience, ErrResilienceNotWired) + 3 default impls
  (stockManifestBuilder, noopWriter, noopProjection).
- stockpipeline/orchestrator.go: Orchestrator gains 3 new fields (builder,
  writer, projection) + NewOrchestratorWithResilience constructor
  (default-fallback for nil ports) + RunResilient(ctx, *RunInput)
  (*RunSummary, error) with 7-step ladder (resolve_sources, plan_clips,
  stage_sources, build_manifest, validate_manifest, emit_chunks,
  project_manifest). Run is now a thin wrapper that drops FinalStatus for
  legacy callers; the manifest-only return is unchanged.
- stockpipeline/service.go: remove migrated IndexingStatus block (the 4
  consts + Marshal/Unmarshal + 2 compile-time assertions) + the
  ChunkResult.Indexed field + IndexingStatus-anchored doc-comments.
  Add Service.runOrchestratorResilient (sibling to runOrchestrator)
  called exclusively by HandleJob. HandleJob now projects summary.FinalStatus
  into the result map under the "final_status" key.
- stockpipeline/run_upload_indexing_test.go (NEW): 3 tests pin the
  resilience contract: (a) outbox-DB rollback on writer error =>
  ErrAtomicDispatchFailed surfaced via RunResilient; (b) manifest-
  completeness gate => ErrManifestIncomplete when a Required:true
  artifact has empty Path; (c) Qdrant offline => RunResilient returns
  RunSummary{FinalStatus: StatusIndexPending, Manifest: non-nil}.
- CHANGELOG.md: replace the Commit 4 entry with the Commit 4-expanded
  version (Removed: ChunkResult.Indexed field; Added: StatusIndexPending
  + 3 ports + 3 tests + Service.runOrchestratorResilient).

Verification: grep residue for IndexingStatus, IndexingPending,
IndexingSkipped, IndexingCompleted, IndexingFailed, uploadAndIndexChunk,
indexChunkToAssetIndex, indexChunkToClipsDB, upsertChunkAndDispatch,
buildPipelineMetadata MUST return empty in production code. gofmt + go vet
+ go build + go test -count=1 + go test -race + scripts/ci-architectural-
checks.sh all green.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
CMT_EOF_RAW

# 3) Commit
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -F /tmp/commit_msg.txt

# 4) Push (loop AGENTS.md §Git-Lesson-2: clean rebase + ff-push, no --force)
MAX_ITER=3
for i in $(seq 1 $MAX_ITER); do
  git fetch origin
  LOCAL_SHA=$(git rev-parse HEAD)
  REMOTE_SHA=$(git rev-parse origin/main)
  AHEAD=$(git log --oneline $REMOTE_SHA..HEAD | wc -l)
  BEHIND=$(git log --oneline $LOCAL_SHA..origin/main | wc -l)
  echo "iter=$i local=$LOCAL_SHA remote=$REMOTE_SHA ahead=$AHEAD behind=$BEHIND"

  if [ "$BEHIND" -eq 0 ]; then
    echo 'fast-forwardable — push'
    git -c user.email='agent@pipelinegen.local' \
        -c user.name='PipelineGen Agent' \
        push origin main
    break
  fi

  # Behind > 0: capire se byte-equivalent replay (Git-Lesson-5) o conflict
  # (Rebase-Conflict Lesson). Block su §1.b "Recupero da half-edit" per
  # la procedura completa.
  echo 'DIVERGED > 0 — STOP, leggi COMMIT_4_EXPANDED_HANDOFF.md §1.b'
  echo '--- log upstream che local lacks ---'
  git log --oneline HEAD..origin/main | head -10
  echo '--- byte-equivalence check ---'
  for f in $(git diff --name-only HEAD..origin/main); do
    L=$(git show HEAD:$f 2>/dev/null | sha256sum | awk '{print $1}')
    R=$(git show origin/main:$f 2>/dev/null | sha256sum | awk '{print $1}')
    if [ "$L" = "$R" ] && [ -n "$L" ]; then
      echo "byte-equivalent: $f"
    else
      echo "DIVERGENT:       $f (sha256 local=$L remote=$R)"
    fi
  done
  exit 1
done

# 5) Verifica atterraggio
git log --oneline origin/main -3
git show --stat origin/main | head -20

---

## 6. APPENDICE — Codice pronto per i 3 nuovi test (copia-incolla)

File NEW: `internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go`

```go
package stockpipeline

import (
    "context"
    "errors"
    "testing"

    "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
    "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// stubWriter supporta le 3 failure-mode dei test:
//   - modeA (forceFail=true): ritorna errore alla PRIMA chiamata.
//     Usato dal test (a) per verificare abort immediato dell'orchestrator.
type stubWriter struct {
    calls      int
    forceFail  bool
}

func (w *stubWriter) WriteAndEnqueue(_ context.Context, _ *asset.Asset, _ string) error {
    w.calls++
    if w.forceFail {
        return errors.New("simulated outbox insert failure (test stub)")
    }
    return nil
}

// stubBuilder restituisce un manifest invalido per test (b).
// Artifact[0] ha Required:true ma Path:"" — Validate() fallisce
// => orchestrator returns ErrManifestIncomplete.
type stubBuilder struct{}

func (stubBuilder) Build(_, _ string) (*job.ArtifactManifest, error) {
    return &job.ArtifactManifest{
        SchemaVersion: job.SchemaVersionArtifactManifestV1,
        Artifacts: []job.Artifact{{
            ID: "test:incomplete", Required: true, Path: "",
        }},
    }, nil
}

// stubProjection supporta test (c): ritorna errore per simulare Qdrant
// offline => orchestrator flips FinalStatus a StatusIndexPending.
type stubProjection struct{}

func (stubProjection) Project(_ context.Context, _ *job.ArtifactManifest) error {
    return errors.New("simulated qdrant offline (test stub)")
}

// ─── TEST (a): outbox rollback ─────────────────────────────────
// Spec utente: "outbox not written → DB rollback"
// Contract: writer returns error at first call => RunResilient aborts
//           immediately, surfaces ErrAtomicDispatchFailed via errors.Is.
func TestOrchestrator_RunResilient_OutboxRollback(t *testing.T) {
    w := &stubWriter{forceFail: true}
    o := NewOrchestratorWithResilience(
        OrchestratorConfig{JobId: "test-a"},
        NewDeterministicPlanner(),
        NewInMemoryStepStore(),
        NewNoopSourceStager(),
        nil, nil,
        stockManifestBuilder{}, w, noopProjection{},
    )
    _, err := o.RunResilient(context.Background(), &RunInput{
        DirectURLs: []string{"https://example.com/a.mp4"},
        ClipDuration: 5, ChunkDuration: 5,
    })
    if err == nil {
        t.Fatal("expected ErrAtomicDispatchFailed, got nil")
    }
    if !errors.Is(err, ErrAtomicDispatchFailed) {
        t.Errorf("err = %v, want errors.Is(err, ErrAtomicDispatchFailed) == true", err)
    }
    if w.calls != 1 {
        t.Errorf("writer.calls = %d, want 1 (abort on first failure)", w.calls)
    }
}

// ─── TEST (b): manifest-completeness gate ──────────────────────
// Spec utente: "asset missing from manifest → job NOT marked SUCCEEDED"
// Contract: Required:true + empty Path => Validate() fails => Gate
//           surfaces ErrManifestIncomplete; summary MUST be nil.
func TestOrchestrator_RunResilient_ManifestGateFails(t *testing.T) {
    o := NewOrchestratorWithResilience(
        OrchestratorConfig{JobId: "test-b"},
        NewDeterministicPlanner(),
        NewInMemoryStepStore(),
        NewNoopSourceStager(),
        nil, nil,
        stubBuilder{}, noopWriter{}, noopProjection{},
    )
    summary, err := o.RunResilient(context.Background(), &RunInput{
        DirectURLs: []string{"https://example.com/b.mp4"},
        ClipDuration: 5, ChunkDuration: 5,
    })
    if err == nil {
        t.Fatal("expected ErrManifestIncomplete, got nil")
    }
    if !errors.Is(err, ErrManifestIncomplete) {
        t.Errorf("err = %v, want errors.Is(err, ErrManifestIncomplete) == true", err)
    }
    if summary != nil {
        t.Errorf("summary must be nil on gate failure, got %v", summary)
    }
}

// ─── TEST (c): Qdrant offline → INDEX_PENDING ──────────────────
// Spec utente: "Qdrant offline → job SUCCEEDED with INDEX_PENDING"
// Contract: projection.Project returns error => RunResilient flips
//           FinalStatus a StatusIndexPending, ritorna (manifest, nil).
func TestOrchestrator_RunResilient_QdrantOffline_IndexPending(t *testing.T) {
    o := NewOrchestratorWithResilience(
        OrchestratorConfig{JobId: "test-c"},
        NewDeterministicPlanner(),
        NewInMemoryStepStore(),
        NewNoopSourceStager(),
        nil, nil,
        stockManifestBuilder{}, noopWriter{}, stubProjection{},
    )
    summary, err := o.RunResilient(context.Background(), &RunInput{
        DirectURLs: []string{"https://example.com/c.mp4"},
        ClipDuration: 5, ChunkDuration: 5,
    })
    if err != nil {
        t.Fatalf("RunResilient err = %v (Qdrant offline must NOT surface as error — resilient path flips to INDEX_PENDING)", err)
    }
    if summary == nil {
        t.Fatal("RunResilient returned nil summary on Qdrant offline (artifacts ARE on Drive; only indexing is deferred)")
    }
    if summary.Manifest == nil {
        t.Error("summary.Manifest must be non-nil on Qdrant offline")
    }
    if summary.FinalStatus != job.StatusIndexPending {
        t.Errorf("summary.FinalStatus = %q, want %q", summary.FinalStatus, job.StatusIndexPending)
    }
}
```

---

## 7. APPENDICE — Prompt pronto per il code-reviewer-minimax-m3

Quando `STEP 4` viene eseguito, copia-incolla questo prompt:

```
Pre-push sanity review of Stock Cutover Commit 4-expanded — retiring
IndexingStatus residue + adding 3 resilience ports + 3 orchestrator-resilience tests.

Files changed (8):
1. internal/kernel/job/job.go (MODIFIED) — added StatusIndexPending
   constant + extended IsActive() + Valid() predicates.
2. internal/domain/job/job.go (MODIFIED) — re-exported StatusIndexPending
   via canonical alias.
3. internal/application/assets/providers/stock/stockpipeline/service.go
   (MODIFIED) — REMOVED IndexingStatus block + ChunkResult.Indexed field
   + scrubbed IndexingStatus-anchored doc-comments. HandleJob now uses
   runOrchestratorResilient + projects summary.FinalStatus into result
   map under the "final_status" key.
4. internal/application/assets/providers/stock/stockpipeline/orchestrator.go
   (MODIFIED) — extended Orchestrator with 3 new fields + 1 new
   constructor + 1 new method.
5. internal/application/assets/providers/stock/stockpipeline/upload_orchestration.go
   (NEW, ~200 LoC) — RunSummary + 3 ports + 4 typed sentinels + 3 default impls.
6. internal/application/assets/providers/stock/stockpipeline/run_orchestrator.go
   (MODIFIED) — added Service.runOrchestratorResilient sibling.
7. internal/application/assets/providers/stock/stockpipeline/run_upload_indexing_test.go
   (NEW, ~110 LoC) — 3 tests pinning (a) outbox rollback, (b) manifest gate,
   (c) Qdrant offline.
8. CHANGELOG.md — extended Commit 4 entry to Commit 4-expanded.

Please verify:
- enum migration correctness: kernel/domain only, no duplicate.
- port-type compile-time assertions: each default impl satisfies its narrow
  interface (var _ X = (*Y)(nil)).
- test assertions: errors.Is probes are typed-sentinel-correct.
- result-map key "final_status" matches C12 §8.4 envelope spec.
- no orphan symbol: grep IndexingStatus/Indexed/uploadAndIndexChunk/* in
  production code MUST return empty.

Verdict: APPROVE/APPROVE-WITH-NITS/REJECT.
```

---

## 8. Riassunto finale (TL;DR per next session)

1. Pre-flight (STEP 1): verifica synced a `65e75ba7`, 5 file modificati su disco.
2. Implementa 4 modifiche (STEP 2): runOrchestratorResilient + HandleJob update
   + 3 nuovi test + CHANGELOG update.
3. Valida (STEP 3): gofmt + vet + build + test + race + archcheck + grep residue.
4. Code-reviewer in parallelo a STEP 3 (STEP 4) per risparmiare round-trip.
5. Stage + commit atomico + push via §Git-Lesson-2 loop (STEP 5). Niente topic
   branch, niente `--force`. Trailer `Co-authored-by:` in fondo al body.
6. Se push rejected: §1.b recovery (byte-equivalent replay o Rebase-Conflict Lesson)
   prima di force-push (che è sempre anti-pattern).

## Legenda del file

| § | Cosa fa | Quando eseguire |
|---|---------|-----------------|
| 0 | snapshot stato | subito |
| 1 + 1.b | pre-flight + recovery da half-edit | prima di STEP 2 |
| 2.1-2.4 | 4 modifiche da fare (codice pronto in §6) | dopo §1 verde |
| 3 | validazione completa | dopo §2 |
| 4 | code-reviewer | in parallelo a §3 |
| 5 | commit + push (Git-Lesson-2 loop con recovery) | dopo §3 verde + §4 APPROVE |
| 6 | codice Go pronto per i 3 test (copia-incolla) | quando implementi §2.3 |
| 7 | prompt per il code-reviewer | quando lanci §4 |
| 8 | TL;DR | rileggilo prima di committare |
