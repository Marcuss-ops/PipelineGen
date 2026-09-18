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

## Drive folder map (verified against the media SSOT)

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

**Per-language separation is now BOTH by folder and by filename.**
`resolveRenderFolders` resolves

```
<clips root | payload drive_folder_id>[/<run subfolder>]/<language>
```

so the four variants of a clip land in `<run folder>/it`, `/es`, `/de`, `/fr` as
`<clipID>.<lang>.<sha256-prefix-12>.mp4`. The level is created through the same
`FolderAdmin` cache as the other levels, so the concurrent fan-out and the
post-crash recovery path (`UploadRendered`) converge on one folder per
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

New tests (document_render_projection_test.go):

* two languages with their own certified renders produce two **different**
  document links;
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
  for every scene.

A quick way to check, without opening Drive:

```sql
SELECT title, drive_file_id, folder_id
FROM media_assets
WHERE id LIKE 'cliprender\_%'
ORDER BY created_at DESC LIMIT 25;
```

Every `<clipID>.<lang>.` prefix must be distinct, and the language in each
filename must match the language of the document that links it.

## Notes

* **The 20 MP4s were never at fault.** They were rendered, uploaded and
  committed correctly; only the link surfaced in the wrong document.
* The media SSOT row's `folder_id` follows the destination, so the new
  `cliprender_%` rows of a run carry their per-language folder
  (`…/Dolly Parton/es`), while older rows keep the flat run folder.
* `media_assets.language` is **empty** for these rows: the language lives in the
  artifact metadata and in the filename, not in that column.
