# Localized render links — a document must link its OWN language's video

Date: 2026-09-18
Scope: `internal/capabilities/scripts` (document projection + scene clip projection)
Status: fixed, tested, gate green.

## Symptom

The Dolly Parton 5-scene × 4-language live run reported SUCCEEDED, rendered and
uploaded 20 MP4s, and then published **one language's video into every
language's document**. Observed on the wire:

| Document | Scene 1 `Clip` link | DB row |
|---|---|---|
| French | `1l4uZ-MQCWcqdPwXoCOMV5Xlmxpmr3cI-` | `yt_vLRjqTIiMjc_0_25_v1.**fr**.3f7d7319fd30.mp4` |
| Spanish | `1l4uZ-MQCWcqdPwXoCOMV5Xlmxpmr3cI-` | *(the same French file)* |

The Spanish render existed and was correct all along
(`1uRcxGwi3zPXZn9385MgMb1DCIUpoIrkR` = `..._0_25_v1.**es**.47c84d32948a.mp4`).
It was simply never linked. French won every scene because it happened to be
the language that finished last.

## Root cause

The document is rendered **once per language from the same run result**
(`runner_phase_document.go` → `modelScriptOutputForDocument(result, lang)`), but
the render-link projection was keyed on the **clip alone**:

```go
// internal/capabilities/scripts/document_model.go (before)
renderedLinks := latestLocalizedRenderLinks(result)   // map[clipID]link
...
if link := renderedLinks[clip.ID]; link != "" { binding.DriveLink = link }
```

`latestLocalizedRenderLinks` folded every entry of `result.LocalizedRenders`
into `links[clipID]`, discarding `rendered.Language`. One clip is rendered once
per language, so `renderedLinks[clip]` held whichever language wrote last — and
every language's document read the same value. The projection was language-blind
in a multilingual run by construction.

### Secondary defect: the shared scene clip link was language-blind too

`applyLocalizedRenderLinkLocked` (runner_deps.go) projects a rendered link onto
the language-less `Scene.Clip` reference:

```go
scene.Clip.DriveLink = rendered.DriveLink
```

Same failure: one field, N languages, last writer wins. It was called in a loop
over **all** renders after the voiceover fan-out joined, so the shared field
ended up holding an arbitrary language's video.

This projection is **load-bearing and was not removed**: `DurableResultToDomain`
carries the clip link into the durable domain envelope, and that envelope has no
`LocalizedRenders` field — dropping the projection would have silently removed
the only rendered-clip reference from the durable surface.

## Fix

**1. The projection is per (clip, language)** — `localizedRenderLinksFor(result, language)`:

1. the newest render whose language **is** the requested one;
2. a language-less render (rows written before the per-language contract) fills
   the gap only when (1) is absent;
3. otherwise nothing is substituted and the source clip link is kept.

A render of a **different** language is deliberately never a candidate: a
document built for language X must not link language Y's video merely because
that video exists.

**2. The shared scene clip reference is deterministic** — only the
**source-language** variant of a clip may replace the source clip's own link, so
the result no longer depends on which language finished last. A run with no
declared source language (restored legacy checkpoint) keeps the legacy
accept-any-render behaviour so those results are not silently dropped.

## Drive folder map

> **The rows below are HISTORICAL: they were committed under the pre-change
> destination.** Nothing migrates and nothing looks a render up by folder, so
> they stay where they were written. A row produced by a docs-enabled run TODAY
> carries its per-language folder in the documents tree (see "Per-language
> separation" below), never the clips library child.

`media_assets.folder_id` for every `cliprender_%` row of the Dolly batch is
**`1C-q2swarUlf4JEUYHVe0Ol88MDSU8FUq`** — the `Dolly Parton` child folder created
under `render.drive_folder_id` (`1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2`,
`drive.normal_clips_source_folder`).

The folder `1FcwJNQ4Ygo4qY9e2MWP9kOGQh3VGRZFL` holds **only text rows**
(`job_<id>:script_json` and `job_<id>:scenes`) for the same job. It is the script
**docs/output** folder: no video is ever supposed to be there, so "clips are
missing from it" is the expected state, not a regression.

| Artifact | Destination |
|---|---|
| Localized MP4 renders (20) | `1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2` → `Dolly Parton` = `1C-q2swarUlf4JEUYHVe0Ol88MDSU8FUq` |
| Script documents (per language) | the run's RESOLVED documents root — `docs.folder_id` of the payload, else the configured default (see the verified live chain below) |
| Script JSON / scenes | `1FcwJNQ4Ygo4qY9e2MWP9kOGQh3VGRZFL` |
| Subtitle artifacts (ASS/SRT) | `drive.youtube_subtitles_root_folder` = `1noSFMK_UeF_Xo-RRZWvH10U7tiL1jPP1` |

### Three DIFFERENT roots are involved (verified on Drive, not inferred)

`PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID` is **not** the documents root a run uses. The
root is resolved from the payload first, and the deployment wires several
Drive roots that end up in different trees. For the
`verify_1clip_10lang.generate.json` run `job_1789755164159357218_dcc492f0`
(SUCCEEDED, 77 s) the parent chains read off the Drive API were:

| Artifact | Verified chain |
|---|---|
| 10 localized clips | `Actors (1ll2RlTa…)` → `job_1789755164159357218_dcc492f0` → `<lang>` → `<clipID>.<lang>.<sha12>.mp4` |
| 10 Google documents | the SAME `Actors → <job> → <lang>` folders (see the fix below) |
| `script.json` / `scenes.json` | `Tmp/Clip (VELOX_DRIVE_SCRIPTS_ROOT = 1ST6FxPu…)` → `<job>` → `<lang>` |

`Actors` is the payload's `output.drive_folder_id`: with no `docs.folder_id`, the
routing context falls back to the payload's Drive folder, so the CLIPS and the
DOCUMENTS share that root while the script JSON lives under
`VELOX_DRIVE_SCRIPTS_ROOT`. `PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID`
(`1J_xUGo…` = `Overlay Chronon`) is the *configured default* every payload root
overrides; it is what a docs-enabled run with no folder of its own falls back to,
and it is not where the run above published.

## Per-language destination

**Per-language separation is BOTH by folder and by filename, INSIDE the
documents tree.** `resolveRenderFolders` resolves

```
<resolved documents root>/<job>/<language>
```

The documents root is `ArtifactRoutingContext.DocsFolderID` (the payload's
`docs.folder_id`, else `PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID`) and `<job>` is the job
whose documents the script phase published. A language's deliverable is its
script document PLUS the clips that script describes, so the clip must publish
into the same per-language folder as its document; deriving the clip folder from
the clips root plus the payload's `drive_subfolder_name` instead is exactly how
the two drifted into unrelated Drive trees, with the clip findable only by
knowing its filename.

The payload's `render.drive_folder_id` / `render.drive_subfolder_name` are
therefore a **FALLBACK**, used only by a run with no documents root at all
(documents disabled, or a hermetic composition). A documents root **without a
job** fails closed: with no job level the clip would land in the folder that
holds every run's folders, silently, visible only as a clip nobody can find.

So the language variants of a clip land in `<documents root>/<job>/it`, `/es`,
`/de`, `/fr` as `<clipID>.<lang>.<sha256-prefix-12>.mp4`. The level is created
through the same `FolderAdmin` cache as the job level, so the concurrent fan-out
and the post-crash recovery path (`UploadRendered`) converge on one folder per
(destination, language) instead of racing to create duplicates; an unresolvable
language folder **fails closed** rather than publishing the render one level up,
where an operator would not find it. An empty language adds no level (there is
no correct name to give it).

Renders published before this change stay in the flat run folder; there is no
migration, and nothing looks them up by folder.

## Verification

```bash
go test ./internal/capabilities/scripts/... -count=1
make verify-agent
```

For the 1 clip / 1 scene / 10 languages scenario specifically:

```bash
cd refactored
go test ./internal/app/wiring/ -run TestLocalizedRenderEnqueuer_OneClipTenLanguages -count=1
go test ./internal/capabilities/scripts/ -run TestVerifyOneClipOneSceneTenLanguages -count=1
./scripts/run_verify_1clip_10lang.sh   # live: needs the GPU lane + VELOX_ADMIN_TOKEN
```

The live runner submits `ops/jobs/verify_1clip_10lang.generate.json` and prints,
per language, the render count, the destination folders, the canonical asset ids
and the Drive links, so the ten languages, their ten folders and their ten
subtitle languages can be read off one screen.

## The document must land BESIDE its clip (fixed)

The clip fan-out published per language into `<documents root>/<job>/<language>`
while the document phase published every language's Google Doc **flat into the
documents root** (`FolderID: routing.DocsFolderID`), naming it `..._<lang>`. The
clip was therefore one tree away from the document describing it.

The document phase now resolves each language's folder through the SAME folder
authority the clip destination uses (the localized render enqueuer owns
`FolderAdmin` and its cache):

* `scripts.DocumentFolderResolver` (optional port) +
  `Runner.SetDocumentFolderResolver`; `Runner.documentFolderFor` fails closed
  when the authority errors instead of publishing one level up;
* the enqueuer adapter implements it as `<root>/<job>` + `<language>` using
  exactly the keys `resolveClipDestination`/`resolveRenderFolders` use, so the
  two lanes converge on ONE folder pair per (root, job, language) instead of
  racing to mint two;
* no resolver wired (hermetic compositions) keeps the historical flat root.

Gates: `TestRunner_DocsEnabled_PublishesEachLanguageIntoItsRunFolder` (two
languages → two folders, each the one the authority returned; bypassing the
resolver fails the test),
`TestRunner_DocsEnabled_FailsClosedWhenTheFolderCannotBeResolved`, and
`TestLocalizedRenderEnqueuer_DocumentLandsInTheLanguageFolderOfItsClips` (the
document folder IS the clip folder the clip lane resolves itself, one create per
level).

## The preset font must render the language it burns (fixed)

`subs-young` / `subs-young-pop` / `subs-young-center` use **Poppins**, and
`assets/fonts/Poppins-Bold.ttf` carries **no Cyrillic code point at all**
(0 of the 222 U+04xx code points `Montserrat-Bold.ttf` has). Burning a Russian
subtitle with it produces an empty glyph run; with the GPU-native text policy
(`output.render.require_gpu=true`) Chronon returns that as an
`UnsupportedCapability` error instead of falling back to its software text path,
so the whole run dies mid-render:

```
[error] frame 283 failed: node 'text' error [UnsupportedCapability]
  draw_packed_text_run_surface: empty glyph vector; fall back to legacy text path
localized render: scene "clip-0" lang "ru" produced 1 failure(s)
```

`script.EnsureSubtitleFontForLanguage(style, language)` (in
`kernel/script/output_spec.go`, next to the preset table it guards) now resolves
the effective subtitle style per language: a font that cannot render the target
script is replaced by the first declared font that can (Montserrat, the canonical
clip font), and the plan stores that effective style. `SubtitleStyleHash` became
the single owner of the ASS style id rule and is derived from the effective
style, so the ASS style id (`vidrush-default-montserrat`) and the materialized
font cannot disagree. A language whose script no shipped font covers fails the
plan instead of the renderer.

Coverage is declared per **font asset**, not per family label, because the file
is what gets rasterized: `SubtitleFontAssetForStyle` projects a style onto
`font-poppins-bold` (only if its font names Poppins) or `font-montserrat-bold`,
mirroring the render-plan mapper's own rule
(`renderinggen/clip_plan_mapper.go::fontAssetID`). That closed the last hole:
coverage used to be keyed by family name and any *unlisted* family was reported
as covered, so `Impact`, `Anton`, `Bebas Neue` and `Roboto` were silently
certified for Cyrillic on the strength of a label the renderer never burns. Now
every name projects onto one of the two shipped files and every projection has a
declared, cmap-verified answer.

Gates: `subtitle_font_coverage_test.go` parses the REAL cmap tables of both
shipped fonts and fails if the declared coverage stops matching the files, plus
the per-language swap table (`ru`/`ru-RU`/`RU` → Montserrat; `it`/`pt-BR`/`de`
keep Poppins/Montserrat as asked; unshipped families keep the caller's label
because they project onto the shipped Montserrat file; caller style never
mutated, because one style is shared by every language of a fan-out).
`TestSubtitleFontProjectionIsTotalAndDeclared` pins that no font name can reach
the renderer without a declared asset, and
`renderinggen/font_assets_test.go::TestFontAssetProjectionMatchesKernelDeclaredCoverage`
compares the two projections directly: if either side changes its rule alone,
the gate fails (verified by mutating the kernel side, which turns the
cross-package test red on `Poppins`).

## Verified live acceptance — 1 clip / 1 scene / 10 languages

`job_1789757354581743069_538db35a` (payload
`ops/jobs/verify_1clip_10lang.generate.json`, `audio.mode=NONE`, subtitles
burned, `require_gpu=true`, preset **`subs-young`** — the Poppins preset that used
to kill the run on `ru`, now auto-swapped to Montserrat for that language):

* **10 renders**, one per language (en, it, de, es, fr, pl, tr, ru, pt-BR, id),
  each in its OWN folder — 10 distinct `drive_folder_id`s, all
  `<root>/<job>/<language>`;
* **10 documents**, one per language, in the same per-language folders;
* **per-language subtitles**: 10 ASS artifacts, 18 cues each, each in its own
  language (`You look incredible.` / `Sei incredibile. Beh,` /
  `Ты выглядишь потрясающе. Ну,` …), all with the Montserrat style — the
  fail-closed gate refuses to burn the source language's cues for a target;
* the run needs the clip's own translated tracks: all ten languages must be
  READY with timed cues (`text-tracks-backfill --only-missing --apply` for the
  translations, then `text-tracks-align-cues --source-lang=en` to give them
  cue windows).

New tests (document_render_projection_test.go):

* two languages with their own certified renders produce two **different**
  document links;
* `localized_render_enqueuer_test.go`
  `TestLocalizedRenderEnqueuer_OneClipTenLanguagesLandInTenPerLanguageFolders`
  is the acceptance gate for the 1 clip / 1 scene / 10 languages scenario: ONE
  clip fanned out over the ten configured languages (source + nine targets, the
  order `renderLanguages` emits) must produce ten renders whose destinations are
  `docs-1/job-1/<lang>` — ten **distinct** folders, one create call per level,
  and each render asking for exactly the language of its own folder. Removing the
  language level makes it fail with the flat-layout message, so the gate is not
  vacuous;
* `manifest_1clip_10lang_runtime_contract_test.go` pins the payload of that
  scenario (`ops/jobs/verify_1clip_10lang.generate.json`) against the production
  builder: one canonical clip id, the `audio.mode=NONE` subtitle lane that fans
  one clip over every language, burned subtitles, and the resolved documents root
  being **different** from the payload's clips folder;
* a language whose fan-out produced nothing keeps the source clip link and never
  borrows another language's render;
* a pre-contract (language-less) render still projects, but never outranks a
  language-specific one;
* the shared scene clip reference is order-independent and holds only the
  source-language render.

## Live acceptance check (after the binary is rebuilt and the service restarted)

For a fresh multilingual run, per language L and per scene:

* the document of L must link the render whose filename is
  `<clipID>.L.<sha12>.mp4` (`media_assets.title` of the corresponding
  `cliprender_%` row);
* **no two languages may publish the same clip link for the same scene** — the
  exact shape of the defect above, and a condition the old projection violated
  for every scene;
* **no two languages may publish into the same folder.**
  `TestLiveDollyPartonMultilingualRuntime` asserts this from the run result:
  every localized render carries its `drive_folder_id`, all clips of one language
  share that language's folder, and a folder shared by two languages fails the
  gate. The names resolved under the documents root answer the same question
  without opening Drive:

  ```
  <PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID>/<job>/<language>/<clipID>.<lang>.<sha12>.mp4
  ```

A quick way to check, without opening Drive:

```sql
SELECT title, drive_file_id, folder_id
FROM media_assets
WHERE id LIKE 'cliprender\_%'
ORDER BY created_at DESC LIMIT 25;
```

Every `<clipID>.<lang>.` prefix must be distinct, the language in each filename
must match the language of the document that links it, and the `folder_id` of two
different languages must differ (the language lives in the folder name,
`<job>/<language>`, not only in the filename).

## The three Drive trees (pinned)

A clip-sourced run writes into exactly three destinations, and the contract is
explicit — a change that moves one tree alone now breaks a test instead of
silently splitting a run's deliverables:

| Tree | Destination | Owner |
| --- | --- | --- |
| 1. clips **and** their script documents | `<documents root>/<job>/<language>` | `resolveRenderFolders` / `ResolveDocumentFolder` |
| 2. subtitle artifacts (ASS) | `<subtitle root>/<clip id>` — one per clip, reused by every language | `resolveRenderFolders` |
| 3. script JSON artifacts (`script.json`, `scenes.json`) | the same root as tree 1 (`DriveConfig.DocumentsFolder() == ScriptsFolder()`) | delivery registry (`DestinationScript` / `DestinationDocument`) |

Gate: `TestLocalizedRenderEnqueuer_ThreeDriveTreesStaySeparate` derives all three
from the same adapter/config the composition root builds. Mutating the subtitle
resolution to nest under the documents root fails it with
`subtitle artifacts resolved INSIDE the run/documents tree`.

## Two robustness defects found while certifying (fixed)

**1. A transient Google Docs 5xx killed a whole run.** One language's document
create returned `googleapi: Error 500: Internal error encountered., backendError`
while every render and every other language's document was already published; the
job was marked `retryable: true` but went to the dead letter with no retry. The
create now runs through `retry.DoWithValue` (3 attempts, 1s→3s backoff, 25%
jitter) with the canonical SDK-exit classification
(`classifyDriveUploadError` → `retry.ClassifyGoogleAPIError` + `retry.WrapTransient`),
which is also what makes every 4xx fail fast.

Gates (`doc_client_create_test.go`): a single 500 is retried and the publish
succeeds on the second attempt; a 400 is never retried (exactly 1 attempt); a
permanently failing 503 exhausts the three attempts and keeps the
`failed to create google doc` context. Forcing `MaxAttempts: 1` turns the first
and third red, so the gate is not vacuous.

**2. One run's publication error failed ANOTHER run.** The overlay publication
pool is process-wide (one enqueuer per process) but the join was global: run A
waited for run B's in-flight uploads and inherited B's first error — observed
live as a run dying on another video's `context canceled`, which the operator's
retry could not fix. Publications are now recorded in a per-run batch keyed by
the kernel run bound to the context, `Wait(ctx)` joins and retires only its own
batch, and a composition that binds no run keeps the historical process-wide
behaviour.

Gate: `TestQueueRenderEnqueuerPublicationJoinIsRunScoped` holds one run's upload
open, fails it, and proves that a concurrent run (a) does not block on it and
(b) is not failed by it, while (c) the failing run still gets its own error and a
later run starts clean. Restoring the shared batch makes the concurrent run's join
block, which is exactly the production symptom.

## Notes

* **The 20 MP4s were never at fault.** They were rendered, uploaded and
  committed correctly; only the link surfaced in the wrong document.
* The media SSOT row's `folder_id` follows the destination, so the
  `cliprender_%` rows of a docs-enabled run carry their per-language folder
  (`<documents root>/<job>/es`), while rows written before this change keep the
  clips-root folder they were published into.
* `media_assets.language` is **empty** for these rows: the language lives in the
  artifact metadata and in the filename, not in that column.
