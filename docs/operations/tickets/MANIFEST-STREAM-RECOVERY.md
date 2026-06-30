# MANIFEST-STREAM-RECOVERY — Manifest cutover PR 1–8 · recovery plan

> **Status**: `pending` · **Owner capability**: `internal/application/assets/manifest` · **Deadline**: `2026-07-10`
> **Tracking issue**: `architecture/current.yaml#id-26 follow_up_tickets.PR-MANIFEST-STREAM-RECOVERY`
> **Auto cross-refs**: AGENTS.md Agent Execution Playbook (§"One task solves one responsibility"); godlike/07 §"Temporary deprecation record"; godlike/11 §"Scope discipline".

---

## Identità ticket

| Campo | Valore |
|---|---|
| **id** | `MANIFEST-STREAM-RECOVERY` |
| **title** | Manifest cutover PR 1–8 — recovery plan from deleted `voiceover-typed-port-m2` branch |
| **owner_capability** | `internal/application/assets/manifest` (future canonical home; today the surface is split between `internal/application/clips/`, `voiceover`, scripts post-processors, etc.) |
| **status** | `pending` |
| **deadline** | `2026-07-10` (allineata a Wave 21 PR-G mega-package split) |
| **tracking_issue** | `architecture/current.yaml#id-26 follow_up_tickets.PR-MANIFEST-STREAM-RECOVERY` |
| **linked extinct ref** | branch tip `26562107` (deleted June 2026; blueprint cache in `/tmp/voiceover-blueprint/`) |

---

## Regola chiave (ripresa da AGENTS.md Agent Execution Playbook)

> *"One task solves one responsibility."*

Questa wave **NON** entra nei commit voiceover `V1..V7`, anche se parte del blueprint vivrebbe naturalmente in `internal/application/voiceover/` o `internal/application/scripts/adapters/`. Il typed-port voiceover ha già i suoi 5 caller attivi su `main`; aggiungere cutover del manifest service lì dentro raddoppia la blast radius per ogni commit e rompe la regola di migrazione EXPAND → BACKFILL → CUTOVER → CONTRACT di godlike/07 (ogni phase in PR separati, owner singolo).

**Conseguenze pratiche**:

1. Ogni PR MANIFEST-STREAM tocca **SOLO** file sotto `internal/application/assets/manifest/`, `internal/app/wire_assets*.go`, `internal/api/assets/manifest/` (TBD al V1 EXPAND).
2. Le voci `voiceover.*` restano fuori perimetro; nessuna PR MANIFEST colpisce `internal/app/wire_script.go::voiceoverSvcAdapter` o `internal/application/scripts/adapters/processor_voiceover.go`.
3. Ordering bloccante: Wave 21 PR-G.2 BACKFILL (mega-package split, deadline 2026-07-10) DEVE chiudersi PRIMA di MANIFEST-V2 BACKFILL, perché il ManifestService port layer attraversa `internal/application/` boundaries che PR-G.2 sposta via `git mv`.

---

## Scope: i 12 commit sul branch cancellato `26562107`

Recuperati dal blueprint (`git archive 26562107 | tar -x -C /tmp/voiceover-blueprint`) + da `git log --name-only 26562107 -- "internal/application/assets/manifest**"`. Raggruppati per phase canonica godlike/07 (EXPAND → BACKFILL → CUTOVER → CONTRACT):

### Phase CONTRACT (older artifacts needing alias or deprecation-record cleanup)

| # | Commit | Subject |
|---|---|---|
| 1 | `4698d981` | `wip(manifest): W14 PR 2 handler wiring + cutover adapters` |
| 2 | `87d33830` | `fix(build): add ArtlistBundle.ManifestService field for fix-build-base` |

### Phase CUTOVER (repeats indicate the prior branch had not converged)

| # | Commit | Subject |
|---|---|---|
| 3 | `d5134ebb` | `feat(manifest): wire canonical manifest service + cutover adapters` |
| 4 | `ec111bb4` | `feat(manifest): wire canonical manifest service + cutover adapters` |
| 5 | `58a81f7f` | `feat(manifest): wire canonical manifest service + cutover adapters` |
| 6 | `65509e0b` | `feat(manifest): wire canonical manifest service + cutover adapters` |
| 7 | `d7f21ac8` | `feat(manifest): wire canonical manifest service + cutover adapters` |
| 8 | `02d87996` | `refactor(manifest): PR 7 — true upload-then-replace via Files.Update` |

### Phase BACKFILL / contract test hardening

| # | Commit | Subject |
|---|---|---|
| 9 | `11c2d2c1` | `test(manifest): PR 6 exact-N=5 same-folder concurrent merge-by-AssetID subtest` |
| 10 | `75c4fbb7` | `test(manifest): PR 6 hardening — same-folder concurrent merge-by-AssetID subtest` |
| 11 | `96bf28e9` | `test(manifest): PR 6 — 7-case service test matrix with FakeDriveAdapter` |

### Phase CONTRACT (e2e suites)

| # | Commit | Subject |
|---|---|---|
| 12 | `1206fc82` | `test(manifest): PR 8 — 4 end-to-end integration tests (same-path, abort-on-download-error, mapper flattening, local-corruption recovery)` |

**Osservazione operativa**: i commit #3–#7 sono **5 occorrenze** dello stesso subject su `26562107` — diagnosticano che il prior agent aveva iterato `wire canonical manifest service + cutover adapters` cinque volte senza convergere. La recovery DEVE sostituire quella sequenza con **una sola** PR EXPAND→BACKFILL→CUTOVER pulita (vedi acceptance §"M-V3 CUTOVER" sotto).

---

## Acceptance criteria: 5-PR recovery cycle

Equivalente al voiceover V-cycle (V1..V7), ma con scope strettamente manifest-stream. Ogni PR è atomic, GREEN al landing, e annotato con `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` trailer.

### M-V1 — `EXPAND canonical manifest service contract` 🟢 Low
- Crea `internal/application/assets/manifest/types.go` con `ManifestEntry`, `ManifestEntry.Builder`, `ManifestError`, `ManifestConflict`. Zero implementazioni; solo tipi.
- Crea `internal/application/assets/manifest/ports.go` con `type ManifestService interface { Upload(ctx, cmd ManifestUploadCommand) (ManifestEntry, error); Reconcile(ctx, entries []ManifestEntry) (ReconcileReport, error); SamePathConcurrent(ctx, ...) ... }`.
- Compile-time assertion: `var _ ManifestService = (ManifestServicePort)(nil)`.
- Aggiorna `architecture/ownership/application.yaml::application_assets_manifest.canonical_files` (canonical owner per-application facade contracts, post-dc6add3e) con i due nuovi file.
- ZERO call-site / wire-up touched.

### M-V2 — `BACKFILL typed ManifestService port on ArtlistBundle` 🟢 Low
- Tipa `ArtlistBundle.ManifestService` come `ManifestService` (NON `interface{}`) — fix diretto del commit `87d33830 fix(build)` che aveva aggiunto il field con il minimo sindacale per chiudere build.
- Crea `internal/application/assets/manifest/service.go` con `type Service struct { drive drive.DriveUpload; ... }` + metodi concreti (NOOP impl al landing).
- Registra in `internal/app/wire_assets.go` via `BuildManifestBundle(opts)` helper.
- `go vet ./internal/application/assets/manifest/... && go build ./...` exit 0.

### M-V3 — `CUTOVER manifest wire + cutover adapters (single, converged)` 🟠 Medium
- UNICA PR che sostituisce i 5 commit ripetuti `feat(manifest): wire canonical manifest service + cutover adapters` (#3-#7).
- Drop-in: composition root consuma `ManifestService` typed; ogni adapter downstream usa interface typed.
- Deprecation records in `architecture/deprecations.yaml` per ogni vecchio call site sostituito (godlike/07 §"Migration sequence").
- **REQUIRES**: Wave 21 PR-G.2 BACKFILL chiusa su `main` (per non collidere con il mega-package split in flight, deadline 2026-07-10).

### M-V4 — `PR 6 hardening test matrix (FakeDriveAdapter + same-folder concurrency)` 🟡 Medium
- Recovery dei commit #9–#11 — unica PR unificata.
- Test matrix: 7 case del FakeDriveAdapter (success, partial failure, retry-after-429, idempotent retry, drive-error mid-upload, file-already-exists, share-link-rotated).
- Concurrency subtest: `exact-N=5 same-folder concurrent merge-by-AssetID` — asserzione deterministica dell'ordine di merge-by-AssetID (vedi godlike/08 §"Zero-baseline rule" per il determinismo come SSOT invariant).
- `go test ./internal/application/assets/manifest/... -race -count=1` exit 0.

### M-V5 — `PR 7 + PR 8 (true upload-then-replace via Files.Update + 4 e2e tests)` 🔴 High
- Recovery dei commit #8 + #12.
- PR 7 (`true upload-then-replace`): migra da `Files.Insert` + post-hoc `update` flag a `Files.Update` atomico (Drive API v3 contract).
- PR 8 (4 e2e tests): `same-path`, `abort-on-download-error`, `mapper flattening`, `local-corruption recovery` — ognuno richiede fixture Drive-side e `velox_client.py submit --type manifest.reconcile --payload scripts/testdata/manifest_e2e_<case>.yaml` smoke run (AGENTS.md Pattern 6 esige e2e reale).
- Subject unico: questo è `M-V5` aggregato per ridurre attrito di review; il singolo landing commit ha body che cita PR 7 + PR 8 come scope.

---

## Cross-reference al wave-tracker canonico

`architecture/current.yaml#id-26 follow_up_tickets.PR-MANIFEST-STREAM-RECOVERY` — entry esterna (NON Wave 21 stessa), popolata con:

```yaml
- id: PR-MANIFEST-STREAM-RECOVERY
  title: "MANIFEST-STREAM-RECOVERY — Manifest cutover PR 1–8 (recovery from deleted voiceover-typed-port-m2 branch)"
  owner_capability: internal/application/assets/manifest
  status: pending
  deadline: 2026-07-10
  tracking_issue: "docs/operations/tickets/MANIFEST-STREAM-RECOVERY.md (this file)"
  acceptance: |
    Tutti i 12 commit del branch cancellato 26562107 (Manifest cutover PR 1–8)
    sono recuperati via le 5 PR acceptance-grade descritte sopra (M-V1..M-V5),
    in EXPAND → BACKFILL → CUTOVER → CONTRACT sequence (godlike/07).
  notes: |
    Wave separata dal voiceover typed-port (V1..V7) per AGENTS.md Agent
    Execution Playbook "One task solves one responsibility". Deadline
    2026-07-10 allineata a Wave 21 PR-G mega-package split completion;
    qualsiasi MANIFEST-V2 BACKFILL PR che atterra PRIMA di PR-G.2 BACKFILL
    su `main` collide con il mega-package `git mv` in flight.
```

Quando `MANIFEST-V5` atterra e gli e2e test sono verdi, l'entry sopra viene ruotata a `status: shipped_through` + `promoted_to: architecture/deprecations.yaml` (per ogni vecchio call-site sostituito).

---

## Status log

| Data | Evento |
|---|---|
| 2026-06-28 | Ticket aperto. Branch `26562107` cancellato, blueprint cached in `/tmp/voiceover-blueprint/` solo per reference read-only. `main` HEAD `a960e7286`. |
| TBD | M-V1 EXPAND — primo commit verde. |
| TBD | M-V5 CONTRACT — chiusura ticket + flip status. |
| TBD (entro 2026-07-10) | Wave 21 PR-G.2 BACKFILL settlement (prerequisite per M-V2 BACKFILL). |

---

## Link utili

- Blueprint cache reference: `/tmp/voiceover-blueprint/` (read-only, post Land non più considerato source-of-truth).
- Wave tracker canonical pointer: `architecture/current.yaml#id-26 follow_up_tickets.PR-MANIFEST-STREAM-RECOVERY`.
- Ownership canonical pointer: `architecture/ownership/application.yaml::application_assets_manifest.canonical_files` (post-dc6add3e; a V1 EXPAND).
- AGENTS.md Agent Execution Playbook: §"One task solves one responsibility".
- godlike/07 §"Migration sequence" (EXPAND → BACKFILL → CUTOVER → CONTRACT).
- godlike/08 §"Zero-baseline rule" (transitional_baselines require owner + deadline; ogni PR MANIFEST-V* deve rispettare).
