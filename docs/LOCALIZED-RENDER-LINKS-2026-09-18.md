# Localized render links and Drive destinations

Date: 2026-09-18 (destination contract updated 2026-10-25)
Scope: `internal/capabilities/scripts`, `internal/app/wiring` (localized render fan-out)
Status: current.

Google Docs publication was retired on 2026-10-25, so the per-language
document-link projection this page originally fixed no longer exists. What
remains — and what this page now documents — is where a localized render lands
and which link surfaces it feeds.

## Destination

`localizedRenderEnqueuerAdapter.resolveRenderFolders` publishes a localized clip
into:

```text
<clips root | output.drive_folder_id>[/<output.drive_subfolder_name>]/<language>/
    <clipID>.<lang>.<sha256-prefix-12>.mp4
```

- The clips root is `cfg.Drive.ClipsFolder()`
  (`drive.normal_clips_source_folder`); a payload-level
  `output.drive_folder_id` overrides it explicitly, and
  `output.drive_subfolder_name` adds one intermediate level (typically the job).
- The language level is created through the same `FolderAdmin` cache as the
  other levels, so the concurrent fan-out and the post-crash recovery path
  (`UploadRendered`) converge on one folder per (destination, language) instead
  of racing to create duplicates. An unresolvable language folder **fails
  closed** rather than publishing the render one level up, where an operator
  would not find it. An empty language adds no level (there is no correct name
  to give it).
- There is no documents tree any more: the clips root is the only destination
  contract, and `PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID` does not exist.

Renders published before the per-language change stay in the flat run folder;
there is no migration, and nothing looks a render up by folder.

## Separate Drive trees

| Artifact | Destination | Owner |
|---|---|---|
| Localized MP4 renders | `<clips root>/<job>/<language>` (or the payload's explicit folder) | `resolveRenderFolders` |
| Subtitle artifacts (ASS/SRT) | `<subtitle root>/<clip id>` — one per clip, reused by every language | `resolveRenderFolders` |
| Script JSON / scenes | the scripts root (`VELOX_DRIVE_SCRIPTS_ROOT`) | delivery registry (`DestinationScript`) |

## Run-result projection

The per-language link surfaces stay language-keyed; the unkeyed ones must be
order-independent:

- `result.LocalizedRenders` carries one entry per (clip, language) with its own
  `drive_link` / `drive_folder_id`, and the final-job payload resolves a scene
  through `certifiedLocalizedRendersForScene(result, sceneID, language)` — a
  render of a different language is never substituted;
- a language-less (pre-contract) render still projects, but never outranks a
  language-specific one;
- the shared `Scene.Clip` reference (`applyLocalizedRenderLinkLocked`) accepts
  only the **source-language** variant, so it does not depend on which language
  finished last.

Gates: `TestLocalizedRenderEnqueuer_LanguageIsAFolderLevelUnderTheClipsRoot`,
`TestLocalizedRenderEnqueuer_PayloadSubfolderKeepsTheClipsDestination`,
`TestLocalizedRenderEnqueuer_EachLanguageGetsItsOwnFolder`,
`TestLocalizedRenderEnqueuer_LanguageFolderFailureFailsClosed` and
`TestLocalizedRenderEnqueuer_RecoveryUploadsIntoTheLanguageFolder`.

## Verification

```bash
go test ./internal/app/wiring/ -run TestLocalizedRenderEnqueuer -count=1
go test ./internal/capabilities/scripts/ -count=1
make verify-agent
```

For the 1 clip / 1 scene / 10 languages scenario specifically:

```bash
go test ./internal/app/wiring/ -run TestLocalizedRenderEnqueuer_OneClipTenLanguages -count=1
go test ./internal/capabilities/scripts/ -run TestVerifyOneClipOneSceneTenLanguages -count=1
./scripts/run_verify_1clip_10lang.sh   # live: needs the GPU lane + VELOX_ADMIN_TOKEN
```

The live runner submits `ops/jobs/verify_1clip_10lang.generate.json` and prints,
per language, the render count, the destination folders, the canonical asset ids
and the Drive links, so the ten languages and their ten folders can be read off
one screen.
