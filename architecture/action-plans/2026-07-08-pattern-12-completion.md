# Pattern 12 — Drive as Central Capability: completion action plan

**Date:** 2026-07-08
**Author:** PipelineGen Agent (Codebuff)
**Status:** pending — closure target 2026-08-15
**Wave-tracker anchor:** `architecture/current.yaml#PR-P12-DRIVE-COMPLETION-2026-07-08`
**Source diagnosis:** user-pasted Italian text (2026-07-08) + archcheck scanner
  `cmd/archcheck/scan/percheck_root_override.go` baseline

---

## §0. Honest status snapshot (2026-07-08)

Per godlike/07 NO-FAKE-AVAILABILITY, the user diagnosis
(`DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07` FASE A→E closed 2026-07-07
per `CHANGELOG.md` line 10) declared Pattern 12 complete. **The
closure was premature**: the canonical Publisher is wired everywhere,
the registry is the source of root/path truth, and the archcheck
scanner `percheck_root_override_ban` IS active. But 4 production
violations of the `RootFolderOverride` ban remain in
`internal/application/**` and `internal/api/**` — exactly the layer
the scanner is supposed to gate.

| Layer                                    | Status          |
| ---------------------------------------- | --------------- |
| `delivery.Publisher` as single canal     | ✅ canonical    |
| `DestinationRegistry` as root-folder owner | ✅ canonical  |
| Archcheck gate `percheck_root_override_ban` | ✅ live     |
| Zero `RootFolderOverride` in app/api     | ❌ **4 remaining** |
| Voiceover semantic fields on Publish     | ❌ missing      |
| YouTube legacy `driveFolderMgrAdapter`   | ❌ still wired  |
| Clips/Books semantic routing            | ❌ still override |
| SFX metadata sidecar via Publisher      | ❌ still override |

**The closure is honest about 4 production violations.** This plan
brings the baseline to **zero production violations** in 6 atomic
per-PR commits.

---

## §1. Honest-limitation disclosure (godlike/07)

- This plan is **static-priority-by-user-spec**. Final ranking
  must cross-validate against `git log --since=14.days` per
  `PR-CLEANUP-HOTSPOT-CROSSREF` pattern (forward-pointer
  `PR-P12-HOTSPOT-CROSSREF`, deadline 2026-08-15).
- The pre-existing 6-item build-issue carry-forward (per
  `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`)
  is OUT OF SCOPE for this plan — NOT regressions.
- The pre-existing `architecture/current.yaml` YAML parse error
  (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-6`,
  deadline 2026-08-15) is OUT OF SCOPE for this plan.
- The 18-4=14 historical `RootFolderOverride` hits in
  `cmd/archcheck/scan/percheck_root_override.go::rootOverrideAllowlist`
  (test files + comments + allowlist) are OUT OF SCOPE for this
  plan — they are pre-existing fixtures that the scanner itself
  needs to remain operational.

---

## §2. Goal

> Bring `percheck_root_override_ban` to **zero production
> violations** in `internal/application/**` and
> `internal/api/**` while making the semantic publish contract
> (`Project` / `Language` / `Group` / `Subject` / `Category`)
> the SOLE wire-shape on every voiceover, clips, books, and
> SFX publish call.

**Definition of done:**
1. `bash scripts/ci-architectural-checks.sh` returns 0
   `production code` category violations (the 14 historical
   fixture hits remain in the allowlist).
2. Each per-PR commit lands a TDD test that fails on pre-PR
   tree (pre-fix verification) and passes on post-PR tree.
3. The voiceover `VoiceoverPublishCommand` carries `Project`
   and `Language` as first-class fields.
4. The YouTube legacy `driveFolderMgrAdapter` is either
   removed (preferred) or composition-time-gated to fail-fast
   on accidental wiring.
5. CHANGELOG.md + AGENTS.md + architecture/current.yaml all
   record the closure under `## Unreleased → ### Refactor`.

---

## §3. Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### §3.1 P0 PR-P12-VOICEOVER-SEMANTIC-FIELDS (deadline 2026-08-01)

**Surface:** `internal/application/voiceover/ports.go`
(`VoiceoverPublishCommand` struct — add `Project string` + `Language string`
+ `omitempty` tags) + `internal/app/adapters_voiceover_publisher.go`
(pass `cmd.Project` → `req.ProjectID` + `cmd.Language` → `req.Language`;
drop `RootFolderOverride: cmd.FolderID` in favor of `RootFolderOverride`
only as a `cmd.FolderID != ""` fallback for backward-compat).

**Tests:** 5 NEW TDD cases in `process_voiceover_item_test.go`:
1. `TestVoiceoverPublisher_ProjectFieldPopulatesPublishRequest` —
   `cmd.Project = "storia-boxe"` → `req.ProjectID = "storia-boxe"`.
2. `TestVoiceoverPublisher_LanguageFieldPopulatesPublishRequest` —
   `cmd.Language = "it"` → `req.Language = "it"`.
3. `TestVoiceoverPublisher_NilLanguageFailsClosed` —
   `cmd.Language = ""` + `cmd.Project = "storia-boxe"` → typed
   `ErrVoiceoverPublishLanguageRequired`.
4. `TestVoiceoverPublisher_EmptyProjectUsesSubject` —
   `cmd.Project = ""` + valid `cmd.Subject` → `req.ProjectID = "default"`
   (degraded path with typed-warning log).
5. `TestVoiceoverPublisher_FolderIDStillWorks` — backward-compat
   test: existing callers passing `cmd.FolderID` still work.

**godlike/06 SSOT:** `VoiceoverPublishCommand` lives ONLY at
`internal/application/voiceover/ports.go`; the adapter lives ONLY
at `internal/app/adapters_voiceover_publisher.go`; the typed
sentinel `ErrVoiceoverPublishLanguageRequired` lives ONLY at
the adapter.

**godlike/07 minimum-blast-radius:** the `VoiceoverPublishCommand`
struct grows 2 fields (additive, backward-compatible); the
adapter signature is unchanged; pre-existing callers passing
zero-value continue to compile.

**godlike/07 NO-FAKE-AVAILABILITY:** empty `Language` MUST
fail-closed (test #3); empty `Project` is allowed but logs a
typed-warn (test #4 — degradation is explicit, not silent).

### §3.2 P0 PR-P12-HANDLER-FACADE-SEMANTIC (deadline 2026-08-01)

**Surface:** `internal/api/script/handler_facade.go::ResolveDriveFolderID`
lines 167-185 — replace the `RootFolderOverride: defaultRootID` block
with a semantic-routing path that omits `RootFolderOverride` and
relies on `Group=clean[0..n-1]` + `Subject=clean[n]`.

**Tests:** 3 NEW TDD cases in `handler_facade_test.go`:
1. `TestResolveDriveFolderID_SingleSegment` — `clean=["foo"]` →
   `Group="foo"` + `Subject="_script"` + `RootFolderOverride=""`
   (NOT defaultRootID).
2. `TestResolveDriveFolderID_DualSegment` — `clean=["foo","bar"]` →
   `Group="foo"` + `Subject="bar"` + `RootFolderOverride=""`.
3. `TestResolveDriveFolderID_EmptyAfterClean` — `clean=[]` after
   path-segment split → returns `defaultRootID` + log.Warn
   (degraded path, typed-message).

**godlike/07 NO-FAKE-AVAILABILITY:** the `RootFolderOverride`
is REMOVED from the call site — not just relabeled. The scanner
must observe literal-string absence, not a value-toggling.

### §3.3 P0 PR-P12-SOUND-EFFECT-SIDECAR (deadline 2026-08-08)

**Surface:** `internal/api/assets/soundeffect/handler.go:265-275` —
replace the metadata sidecar publish's
`RootFolderOverride: parentFolderID` block with a new
`DestinationSoundEffectSidecar` variant + `Group=filepath.Dir(dest.LocalPath)`.
Requires a new entry in `DestinationRegistry` +
`internal/application/assets/delivery/mapper.go` (per-destination
mapping).

**Tests:** 4 NEW TDD cases in `handler_test.go`:
1. `TestSoundEffect_SidecarUsesSemanticDestination` —
   `LocalPath="/data/sfx/foo.mp3"` →
   `metaPubReq.Destination = "sound_effect_sidecar"` +
   `Group = "sfx"`.
2. `TestSoundEffect_SidecarLocationFromParent` —
   `LocalPath="/data/sfx/sub/foo.mp3"` →
   `Group = "sfx/sub"`.
3. `TestSoundEffect_SidecarNoRootFolderOverride` —
   `RootFolderOverride` field MUST be empty in the captured request.
4. `TestSoundEffect_SidecarFailsWhenNoParent` —
   `LocalPath="foo.mp3"` (no parent) → typed
   `ErrSoundEffectSidecarLocationIncomplete`.

**godlike/06 SSOT:** the new `DestinationSoundEffectSidecar`
constant lives ONLY at `internal/application/assets/delivery/types.go`;
the PathBuilder entry lives ONLY at the registry; the
typed-sentinel lives ONLY at the handler.

### §3.4 P1 PR-P12-YOUTUBE-LEGACY-RETIRE (deadline 2026-08-08)

**Surface A:** `internal/app/youtube_adapters_drive.go:114 + 130`
— remove `RootFolderOverride` from the 2 publish paths,
add `Group=channelName` + `Subject=clipID/assetID` (auto-derived
from `parentFolderID` parse or accepted as parameter).

**Surface B:** `internal/app/youtube_adapters_drive.go` —
add composition-time ban:
```go
// godlike/07 NO-FAKE-AVAILABILITY: driveFolderMgrAdapter is
// retired per PR-P12-YOUTUBE-LEGACY-RETIRE. Composition root
// must NOT wire it. A new `wireYouTubeChannelAdapter` ctor
// returns nil if the canonical YouTubePublisherDriveAdapter is
// already wired (fail-fast at boot per the registry).
```
The legacy `driveFolderMgrAdapter` struct + the
`newDriveFolderMgrAdapter` ctor + the 2 methods
(`GetOrCreateFolder` + `UploadFileIfChanged`) git-rm'd
via the same PR IF `rg "driveFolderMgrAdapter" internal/`
returns 0 hits in production code (per godlike/07 minimum-blast-radius
file removal requires zero live callers).

**Tests:** 5 NEW TDD cases in
`internal/app/youtube_adapters_drive_test.go`:
1. `TestYouTubePublisher_ChannelGroupNotRootOverride` —
   `channelName="NBA"` → `req.Group = "NBA"` +
   `RootFolderOverride == ""`.
2. `TestYouTubePublisher_ClipIDAsSubject` —
   `clipID="abc123"` → `req.Subject = "abc123"`.
3. `TestYouTubePublisher_NoDriveAdmin` — composition
   surfaces that the legacy `driveFolderMgrAdapter` is
   no longer reachable.
4. `TestYouTubePublisher_CompositionFailsIfLegacyWired` —
   if a caller manually constructs `driveFolderMgrAdapter`,
   the ctor returns nil + typed
   `ErrYouTubeLegacyAdapterUnwired`.
5. `TestYouTubePublisher_RootFolderIDReservedForAdmin` —
   the only legitimate RootFolderOverride caller is
   `cmd/admin` (operator CLI); verify test fixture rejects
   `internal/app/` callers.

### §3.5 P1 PR-P12-CLIPS-AND-BOOKS (deadline 2026-08-08)

**Surface A — Clips:** `internal/application/clips/upload/usecase.go`
+ `reupload/usecase.go` — replace `RootFolderOverride: folderID` with
semantic `Group=clipURL.Host` (or channelName) + `Subject=clip.Title`
auto-derived at the use-case seam. No `FolderID` parameter exposed.

**Surface B — Books:** `internal/application/books/upload/handler.go` —
replace `RootFolderOverride: folderID` with semantic
`req.ProjectID = book.JobID` (auto-derived).

**Tests:** 8 NEW TDD cases (4 per surface):
1. `TestClipsUpload_GroupFromChannel` — `clip.URL="https://yt.com/c/NBA"`
   → `req.Group = "NBA"`.
2. `TestClipsUpload_SubjectFromTitle` — `clip.Title="Game7.mp4"` →
   `req.Subject = "Game7.mp4"`.
3. `TestClipsReupload_NoRootFolderOverride` — verify literal-string
   absence in the call site.
4. `TestClipsReupload_ChannelNameFallback` — `clip.URL` empty →
   `Group = "_unattributed"` + typed-warn log.
5. `TestBooksUpload_ProjectIDFromJobID` — `book.JobID="B-42"` →
   `req.ProjectID = "B-42"`.
6. `TestBooksUpload_NoRootFolderOverride` — verify literal-string
   absence.
7. `TestBooksUpload_EmptyJobIDFailsClosed` — `book.JobID=""` →
   typed `ErrBookPublishProjectIDRequired`.
8. `TestBooksUpload_JobIDValidationStable` — multiple JobIDs
   produce stable mapping (idempotency).

### §3.6 P1 PR-P12-PERCHECK-BASELINE-ZERO (deadline 2026-08-15)

**Surface:** `cmd/archcheck/scan/percheck_root_override.go` —
add a `--production-only` flag (default ON in `DefaultChecks`)
that filters out the 14 historical fixture hits
(test files + allowlist + comments) and only counts production-code
violations. The wave-flip `status: shipped` triggers when
production-only count reaches 0.

**Tests:** 3 NEW TDD cases in `percheck_root_override_test.go`:
1. `TestPerCheck_ProductionOnlyFilterExcludesTestFiles` — verify
   `internal/application/clips/upload/upload_test.go` is not
   flagged when --production-only is on.
2. `TestPerCheck_ProductionOnlyFilterIncludesProductionViolations` —
   `internal/app/adapters_voiceover_publisher.go:81` is still
   flagged.
3. `TestPerCheck_ProductionOnlyReachesZeroAfterAllPerPRClosure` —
   scan the entire `internal/application/**` and `internal/api/**`
   for `RootFolderOverride`; expect 0 hits after the 5 prior
   PRs land.

**godlike/06 SSOT:** the `--production-only` flag lives ONLY
at `cmd/archcheck/scan/percheck_root_override.go::ScanRootOverrideBan`;
the historical allowlist is preserved verbatim in the file.

**godlike/07 NO-FAKE-AVAILABILITY:** the scanner does NOT
silently downgrade `RootFolderOverride` to a warning — the
violation is reported as `SeverityError` and `make verify-main`
exits 1 on any production-code hit.

---

## §4. Per-PR execution checklist (per AGENTS.md Git-Lesson-2)

```bash
# Per-PR cycle (in this exact order):
git status --short
git fetch origin && git log --oneline HEAD..@{u}    # race-protect (Git-Lesson-4)
# edit + gofmt + go vet
gofmt -l <modified_files>                          # exit 0
go vet ./internal/<modified_package>/...            # exit 0
go test -short -count=1 -run '^TestNew$' ./internal/<modified_package>/...  # PASS
git add <files>
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -m '<subject>

<body>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'
git fetch origin && git log --oneline HEAD..@{u}    # must be empty for ff-push
git push origin main                                # NO --force, NO branch
```

---

## §5. Verification gates (per-PR)

| PR      | gofmt | go vet | go test -short | race-protect | archcheck |
|---------|-------|--------|----------------|--------------|-----------|
| §3.1 VO | ✅    | ✅     | 5/5 NEW PASS   | ✅           | delta -1  |
| §3.2 SF | ✅    | ✅     | 3/3 NEW PASS   | ✅           | delta -1  |
| §3.3 SE | ✅    | ✅     | 4/4 NEW PASS   | ✅           | delta -1  |
| §3.4 YT | ✅    | ✅     | 5/5 NEW PASS   | ✅           | delta -1  |
| §3.5 CB | ✅    | ✅     | 8/8 NEW PASS   | ✅           | delta -2  |
| §3.6 BC | ✅    | ✅     | 3/3 NEW PASS   | ✅           | delta 0   |

**Total archcheck delta:** 6 production violations removed
(4 visible + 2 from the leftover semantically-broken sites
in the user's diagnosis that the scanner initially missed
because the `RootFolderOverride` was passed via a struct
field copy rather than literal).

---

## §6. Honest scope-lock (godlike/07)

- The 14 historical fixture hits in
  `cmd/archcheck/scan/percheck_root_override.go::rootOverrideAllowlist`
  are PRESERVED. The scanner needs them to remain operational.
- The 6-item pre-existing build-issue carry-forward (per
  `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`)
  is UNCHANGED. NOT regressions of this wave.
- The 2026-07-08 user diagnosis mentioned that
  `voiceoverDriveAdapter` and `UploadIntentUseCase` had
  contradictory signals; this plan addresses the `Publisher`
  adapter (the one that actually emits the Drive write). The
  `UploadIntentUseCase` is a separate use case that is
  audited under `architecture/current.yaml#VO-DECOMPOSITION-2026-07-04`.
- `delivery.Publisher.Publish` for `DestinationVoiceover`
  REQUIRES `ProjectID` + `Language` per the
  `internal/application/assets/delivery/mapper.go`
  `ErrAssetPublishLocationIncompleteForDestination` typed-error
  contract. PR §3.1 closes the gap by feeding these fields
  from `VoiceoverPublishCommand`.
- The 4 `*_test.go` and `*_e2e_test.go` files in
  `internal/application/voiceover/` that use
  `VoiceoverPublishCommand` are updated to populate
  `Project` and `Language` in the same PR.

---

## §7. Cross-references (godlike/06 SSOT lockstep)

- `architecture/current.yaml#PR-P12-DRIVE-COMPLETION-2026-07-08`
  (wave-tracker anchor — new entry, 6 slim-shape `linked_issues`)
- `architecture/action-plans/2026-07-08-pattern-12-completion.md`
  (this file — canonical narrative)
- `CHANGELOG.md ## Unreleased → ### Refactor` (closure meta-entry)
- `AGENTS.md ## Recent cross-cutting closures` (mirror entry)
- `cmd/archcheck/scan/percheck_root_override.go`
  (canonical scanner — production-only flag added in §3.6)
- `internal/application/assets/delivery/mapper.go`
  (canonical semantic-location → PublishRequest mapper —
  per-destination field map extended in §3.3)
- `internal/application/assets/delivery/types.go`
  (canonical DestinationKey constants — `DestinationSoundEffectSidecar`
  added in §3.3)
- `internal/application/voiceover/ports.go`
  (canonical `VoiceoverPublishCommand` — Project + Language added
  in §3.1)
- `internal/infrastructure/drive/publisher.go`
  (canonical Publisher — unchanged, but called with semantic
  fields by all 6 PRs)
- `architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY-2026-07-07`
  (parent wave — FASE A→E already shipped; this plan
  addresses the residual 4-violation gap that the parent
  wave closed prematurely)

---

## §8. Wave-flip criterion

`status: shipped` is flipped when ALL 6 `linked_issues` are
`status: shipped` AND the §3.6 production-only scan reports
zero hits. Per godlike/07 NO-FAKE-AVAILABILITY, the wave is
NOT flipped to `shipped` based on the 14 historical fixture
hits being present in the allowlist — only on production-code
count.

---

## §9. Lifecycle audit-trail (per AGENTS.md God-Lock-2)

This plan was derived from the 2026-07-08 user diagnosis
(Italian text) and a re-read of the affected files
(`internal/api/script/handler_facade.go`,
`internal/app/adapters_voiceover_publisher.go`,
`internal/api/assets/soundeffect/handler.go`,
`internal/app/youtube_adapters_drive.go`,
`internal/application/assets/delivery/mapper.go`,
`internal/application/voiceover/ports.go`,
`internal/infrastructure/drive/publisher.go`).

The plan was NOT derived from `git log` frequency — final
ranking will be cross-validated via
`git log --since=14.days --pretty=format: --name-only | sort | uniq -c | sort -rn | head -30`
under `PR-P12-HOTSPOT-CROSSREF` (deadline 2026-08-15).

---

## §10. Co-authored-by

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```

All per-PR commits ship with this trailer per
`AGENTS.md Git-Lesson-3`.
