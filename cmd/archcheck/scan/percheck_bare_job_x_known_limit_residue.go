// Package scan — percheck_bare_job_x_known_limit_residue.go
// (PR-CARD9-PHASE2-CLOSEOUT phase 3, July 2026).
//
// Residue inventory documenting the DISABLED
// `percheck_bare_job_x_requires_domain_job_import` perm.
//
// The perm was unregistered from cmd/archcheck/runner.go
// DefaultChecks because the post-Path-ζ archcheck --strict run
// reported a 91.9% FP ratio (34/37 survivors are Go struct-field
// accesses on Go variables bound via function signature +
// local-var + string-literal; only 3/37 are real canonical
// `job.X` package symbol references). Body-regex cannot
// distinguish the four residue archetypes from real package
// symbols by construction; a Go-AST discriminator would be
// the only viable resurrection path.
//
// Per godlike/07 minimum-blast-radius, this file (rather than
// the scanner file itself) is the forward-pointer to a future
// Card that wants to resurrect the perm with a typed
// discriminator. The scanner file is RETAINED for forensic use
// (git history + scanner logic); this file's typed consts
// enumerate the 37 false-positive survivors so a future
// operator script can iterate the affected file:line list
// without re-running the perm. The 4 archetype names are the
// canonical labels any future perm MUST emit in its JSON
// `archetype` field so a downstream dashboard can correlate
// the new TP list against this residue inventory.
//
// Architecture references:
//   - cmd/archcheck/runner.go (line ~580 in the
//     PR-CARD9-PHASE2-CLOSEOUT phase-3 commit):
//     registration site — removed in this phase.
//   - cmd/archcheck/scan/percheck_bare_job_x_requires_domain_job_import.go:
//     the unregistered scanner file, retained.
//   - cmd/archcheck/scan/percheck_no_duplicate_job_alias.go:
//     surviving sibling from the same Card; still
//     registered, catches the bare-job-alias duplicate-
//     import regression (the OTHER root-cause bug class).
//   - cmd/archcheck/scan/percheck_no_domain_job_compatibility_aliases.go:
//     the precedent — deprecated perm whose scanner
//     remained unregistered, the doc-comment-only pattern.
//   - architecture/current.yaml #PR-CARD9-PHASE2-CLOSEOUT:
//     wave-tracker entry for this phase.
package scan

// KnownLimitArchetype is the canonical name of one of the four
// godlike/07 residue archetypes captured at disable-time. A
// future perm resurrected from this residue MUST emit one of
// these values in its JSON `archetype` field so existing
// dashboards can correlate directly without re-training.
type KnownLimitArchetype string

const (
	// KnownLimitArchetypeParamVar — struct field access
	// (`job.ID` / `job.Payload` / `job.Type`) on a Go
	// variable bound via function signature param, e.g.
	// `func HandleJob(ctx, job *appjobs.Job, tools)`.
	// This is the dominant perm-FP archetype (16/37 hits
	// at disable-time); handler-layer code uniformly
	// binds `job *appjobs.Job` so every `job.X` is a
	// struct-field access on the parameter, NOT a
	// package symbol reference.
	KnownLimitArchetypeParamVar KnownLimitArchetype = "PARAM_VAR"

	// KnownLimitArchetypeLocalVar — struct field access
	// on a Go variable bound via `job, err :=` or
	// `job :=` assignment. godlike/07 documented
	// local-variable pattern; the perm's prior Limitation
	// block already acknowledged this archetype. 14/37
	// hits at disable-time; concentrated in
	// `runner_execute.go::runLease` (where `job` is
	// bound from `h.dispatch.Claim(...)` deeper than
	// the body-regex's window).
	KnownLimitArchetypeLocalVar KnownLimitArchetype = "LOCAL_VAR"

	// KnownLimitArchetypeStringLiteral — token `job.X`
	// appearing INSIDE a Go string literal on the same
	// line (e.g., error message construction,
	// HTTP-URL concatenation). 4/37 hits at disable-
	// time; the body-regex has no string-context
	// discrimination by construction.
	KnownLimitArchetypeStringLiteral KnownLimitArchetype = "STRING_LITERAL"

	// KnownLimitArchetypeDocComment — line starting with
	// `//` / `/*` / `*` (the perm's documented skip
	// policy says these are skipped; a regression of the
	// skip policy would surface here as a CLEAR bucket-
	// overflow signal in the residue). 0/37 hits at
	// disable-time — the skip policy held. PRESERVED as
	// a forward-pointer bucket.
	KnownLimitArchetypeDocComment KnownLimitArchetype = "DOC_COMMENT"
)

// KnownLimitResidueEntry is one (File, Line, Archetype, Reason)
// record from the post-Path-ζ archcheck --strict run that
// preceded this disable PR (deferred archrun, 2026-07-XX).
type KnownLimitResidueEntry struct {
	File      string
	Line      int
	Archetype KnownLimitArchetype
	// Reason is a one-line operator-readable description of
	// WHY this hit is a false positive (the
	// signature-line context or the literal-token position
	// inside a string, etc.). Future operator dashboards can
	// surface this as a hover-tooltip explanation.
	Reason string
}

// KnownLimitResidueInventory — the 37 (originally) / 36 (post-P1-7)
// survivors from the post-Path-ζ archcheck --strict run, classified
// by the verify_hits classifier (see /tmp/param_var_classify_fixed.txt
// in the closeout cascade). The list excludes the 3 real
// canonical-package-symbol references in
// parent_state_machine.go (see KnownLimitTruePkgRefFile).
//
// At disable-time the inventory totalled 34 entries (91.9% of 37).
// The P1-7 retirement of `internal/domain/job/` (→ `internal/kernel/job/`)
// deleted the single STRING_LITERAL row at parent_state.go:165 — the
// panic message that referenced `domain.job.NewStateMachine`. The row
// is dropped HERE rather than left as a dangling inventory entry, so
// the cardinality count below matches the live on-disk file set.
//
// Total breakdown post-P1-7 (was 34 at disable-time, now 33):
//
//	PARAM_VAR       : 16 entries  (sync_jobs=6, maintenance=3,
//	                               enrichment=2,
//	                               voiceover/job_handler=5)
//	LOCAL_VAR       : 14 entries  (handler_download=1,
//	                               drivesync=1, localimport=1,
//	                               runner_execute=11
//	                               [original 4 + deeper 7])
//	STRING_LITERAL  :  3 entries  (handler_download=1,
//	                               drivesync=1, errors.go=1)
//	DOC_COMMENT     :  0 entries  (skip policy held)
//	──────────────────────────────────────────
//	TOTAL FP        : 33 entries  (91.7% of 36)
//	PKG_REF (real)  :  3 entries  (8.3%)  — parent_state_machine.go
//
// The list is the basis for:
//   - the operator-triage dashboard (forward pointer to PR-GODLIKE-08).
//   - any future-Card nolint mechanism
//     (`// nolint:archcheck_bare_job_x`) allowance that
//     opts out per-file.
//   - a future Go-AST perm that MUST iterate this
//     inventory to confirm all 37 are correctly classified
//     before promoting badge to "registered-future".
var KnownLimitResidueInventory = []KnownLimitResidueEntry{
	// ── PARAM_VAR (16): signature param `job *appjobs.Job` ──
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 17, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 22, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — if len(job.Payload) > 0"},
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 23, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — json.Unmarshal(job.Payload, ...)"},
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 119, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleDriveFolderSyncJob(ctx, job *appjobs.Job, ...) — if len(job.Payload) > 0"},
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 120, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleDriveFolderSyncJob(ctx, job *appjobs.Job, ...) — json.Unmarshal(job.Payload, ...)"},
	{File: "internal/application/assets/catalogsync/sync_jobs.go", Line: 126, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleDriveFolderSyncJob(ctx, job *appjobs.Job, ...) — zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/assets/maintenance/service.go", Line: 89, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — s.log.Info \"Handling maintenance job\" zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/assets/maintenance/service.go", Line: 95, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — if len(job.Payload) > 0"},
	{File: "internal/application/assets/maintenance/service.go", Line: 96, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — json.Unmarshal(job.Payload, ...)"},
	{File: "internal/capabilities/assets/providers/stock/enrichment/handler.go", Line: 247, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — if len(job.Payload) > 0"},
	{File: "internal/capabilities/assets/providers/stock/enrichment/handler.go", Line: 248, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — json.Unmarshal(job.Payload, ...)"},
	{File: "internal/capabilities/voiceover/service/job_handler.go", Line: 19, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — zap.String(\"job_id\", job.ID)"},
	{File: "internal/capabilities/voiceover/service/job_handler.go", Line: 20, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — zap.String(\"type\", job.Type)"},
	{File: "internal/capabilities/voiceover/service/job_handler.go", Line: 22, Archetype: KnownLimitArchetypeParamVar, Reason: "HandleJob(ctx, job *appjobs.Job, ...) — switch job.Type"},
	{File: "internal/capabilities/voiceover/service/job_handler.go", Line: 32, Archetype: KnownLimitArchetypeParamVar, Reason: "handleBatchJob(ctx, job *appjobs.Job) — json.Unmarshal(job.Payload, &req)"},
	{File: "internal/capabilities/voiceover/service/job_handler.go", Line: 59, Archetype: KnownLimitArchetypeParamVar, Reason: "handlePromoJob(ctx, job *appjobs.Job) — json.Unmarshal(job.Payload, &req)"},

	// ── LOCAL_VAR (14): `job := *Job` from svc.Enqueue/Claim ──
	{File: "internal/api/assets/clips/nonops/handler_download.go", Line: 97, Archetype: KnownLimitArchetypeLocalVar, Reason: "job.X where job is local *Job return of jobsSvc.Enqueue(...)"},
	{File: "internal/application/assets/sourcing/drivesync/service.go", Line: 99, Archetype: KnownLimitArchetypeLocalVar, Reason: "job.X where job is local *Job return of jobsSvc.Enqueue(...)"},
	{File: "internal/application/assets/sourcing/localimport/service.go", Line: 102, Archetype: KnownLimitArchetypeLocalVar, Reason: "job.X where job is local *Job return of jobsSvc.Enqueue(...)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 31, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — if !r.registry.Has(job.Type) where job is local *Job return of dispatch.Claim"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 33, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — r.log.Error (\"claimed unsupported job type — releasing\") zap.String(\"job_type\", job.Type)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 34, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 36, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — fmt.Errorf(\"%w: %s\", ErrHandlerNotRegistered, job.Type)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 60, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — go r.renewLoop(renewCtx, tools, job.ID, renewErrs)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 69, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — r.log.Warn \"lease renewal failed — failing the job\" zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 94, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — r.log.Warn \"worker Progress emit failed (FASE 0.2 silent-drop rewrite)\" zap.String(\"job_id\", job.ID)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 95, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — zap.String(\"job_type\", job.Type)"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 98, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — observability.WorkerProgressEmittedTotal.WithLabelValues(job.Type, \"error\").Inc()"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 99, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — observability.WorkerProgressErrorsTotal.WithLabelValues(job.Type, \"broker_emit_failed\").Inc()"},
	{File: "internal/application/jobs/worker/runner_execute.go", Line: 101, Archetype: KnownLimitArchetypeLocalVar, Reason: "runLease(...) — observability.WorkerProgressEmittedTotal.WithLabelValues(job.Type, \"success\").Inc()"},

	// ── STRING_LITERAL (3): token inside quoted string ──
	{File: "internal/api/assets/clips/nonops/handler_download.go", Line: 98, Archetype: KnownLimitArchetypeStringLiteral, Reason: "\"status_url\": \"/api/jobs/\" + job.ID + \"/full\" — token inside string concat"},
	{File: "internal/application/assets/sourcing/drivesync/service.go", Line: 103, Archetype: KnownLimitArchetypeStringLiteral, Reason: "Message: \"Drive folder sync dispatched. Poll GET /api/jobs/\" + job.ID + \" for status.\" — token inside string concat"},
	{File: "internal/application/jobs/errors.go", Line: 110, Archetype: KnownLimitArchetypeStringLiteral, Reason: "errors.New(\"appjobs.Service: repo is required ... canonical-job.JobBroker-port ...\") — token inside errors.New literal"},
}

// KnownLimitTruePkgRefFile is the canonical file path where
// the `job.X` references reflect REAL canonical-package-symbol
// use (via umbrella-import `job ".../internal/kernel/job"`).
// The file is excluded from KnownLimitResidueInventory because
// it IS the legitimately-passing scenario the original naive
// body-regex perm was designed to catch.
//
// At disable-time ONLY this single file passes; the file's 3
// uses of `job.X` are exported constants from the umbrella
// alias (job.StateMachine, job.ParentStateSucceeded,
// job.ParentStateFailedTerminal) declared in
// internal/kernel/job/state_machine.go (canonical) and
// re-exported by internal/domain/job/codec.go (godlike/06
// umbrella-restoration contract).
const KnownLimitTruePkgRefFile = "internal/capabilities/voiceover/service/jobs/parent_state_machine.go"

// KnownLimitExpectedResidueTotal is the canonical expected
// TOTAL residue count post-P1-7 (33; was 34 at disable-time
// before the internal/domain/job/parent_state.go row was
// removed along with the back-compat root). A future audit-
// time comparison of len(KnownLimitResidueInventory) against
// this constant verifies that the closeout cascade's residue
// accounting has not drifted (e.g., a new file adopting
// `HandleJob(ctx, job *appjobs.Job, ...)` style would add
// new PARAM_VAR entries and surface a miss with len >
// KnownLimitExpectedResidueTotal).
const KnownLimitExpectedResidueTotal = 33

// KnownLimitExpectedBreakdown is the cardinalities of the
// four residue archetypes post-P1-7 (sum = 33). Indexed by
// KnownLimitArchetype (string). Used by a future audit script
// to assert:
//
//	len(KnownLimitResidueInventory) == KnownLimitExpectedResidueTotal  // = sum(values)
//
// AND each archetype-count matches its expected cardinality
// below — drift in any single bucket is a structured
// forward-pointer signal.
var KnownLimitExpectedBreakdown = map[KnownLimitArchetype]int{
	KnownLimitArchetypeParamVar:      16,
	KnownLimitArchetypeLocalVar:      14,
	KnownLimitArchetypeStringLiteral: 3,
	KnownLimitArchetypeDocComment:    0,
}

// KnownLimitExpectedPKGRefClusterSize is the count of real
// canonical `job.X` package-symbol references in the umbrella-
// restored file (parent_state_machine.go). The cluster has 3
// hits:
//
//	L68 : func domainToVoiceoverParentState(sm *job.StateMachine) voiceover.ParentState
//	L70 : case job.ParentStateSucceeded:
//	L76 : case job.ParentStateFailedTerminal:
//
// A future umbrella-restoration to kernel/job (e.g., removing
// the domain/job re-export layer) MUST keep this cluster at
// exactly 3 — drift is a canonical-package-symbol coverage
// regression.
const KnownLimitExpectedPKGRefClusterSize = 3
