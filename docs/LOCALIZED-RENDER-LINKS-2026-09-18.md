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
| Script documents (per language) | `PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID` = `1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS` |
| Script JSON / scenes | `1FcwJNQ4Ygo4qY9e2MWP9kOGQh3VGRZFL` |
| Subtitle artifacts (ASS/SRT) | `drive.youtube_subtitles_root_folder` = `1noSFMK_UeF_Xo-RRZWvH10U7tiL1jPP1` |

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

## Notes

* **The 20 MP4s were never at fault.** They were rendered, uploaded and
  committed correctly; only the link surfaced in the wrong document.
* The media SSOT row's `folder_id` follows the destination, so the
  `cliprender_%` rows of a docs-enabled run carry their per-language folder
  (`<documents root>/<job>/es`), while rows written before this change keep the
  clips-root folder they were published into.
* `media_assets.language` is **empty** for these rows: the language lives in the
  artifact metadata and in the filename, not in that column.
