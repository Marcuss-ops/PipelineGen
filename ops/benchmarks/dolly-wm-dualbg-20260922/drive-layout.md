# Where the 5 rendered clips actually landed (job_1790091295751404561_e73027c5)

Observed on Drive (live API, 2026-09-22):

```
Il mio Drive
└── Media                        (1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4)
    └── Asset Thumbnail          (1Ui83Bp9du7EFkROX6qdq3S0G-_sT5MmP)
        └── Overlay Chronon      (1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS)   ← resolved documents root
            └── job_1790091295751404561_e73027c5 (1IkM0UumZ1UpAWynj9e6c9OYL3VVZRjkW)
                └── en           (1ufNQYfVBChXYOoeXInTqlp-8BivJIwmZ)
                    ├── yt_Bc9gTqiljLA_0_36_v1.en.36104b99c19e.mp4      10 222 896 B  15:35:25Z
                    ├── yt_tnoMGevqWAM_1841_1903_v1.en.df350121ff9f.mp4 17 338 958 B
                    ├── yt_pfaIAdqvlig_457_509_v1.en.9059e48c46b8.mp4   17 320 633 B
                    ├── yt_pfaIAdqvlig_929_980_v1.en.6cfcc44640ef.mp4   18 441 799 B
                    └── yt_vLRjqTIiMjc_211_256_v1.en.99d17daecc3e.mp4   12 572 698 B
```

The sizes are byte-identical to the local artifacts
(`data/tmp/localization/<clip>.en.mp4`), so these ARE this run's renders.

## Why not `output.render.drive_folder_id` + `drive_subfolder_name`

The payload asked for
`1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K/dolly-5clips-wm-dualbg-20260922`; that folder
was never created. The destination is decided by
`localizedRenderEnqueuerAdapter.resolveClipDestination`
(`internal/app/wiring/localized_render_request.go`):

- when a **documents root resolves**, a localized clip publishes into
  `<docs root>/<job id>/<language>` — the layout that keeps a clip beside the
  script it was rendered from;
- `render.drive_folder_id` / `render.drive_subfolder_name` are read **only** by
  `clipsRootDestination`, i.e. by a run with NO resolvable documents root at all.

The documents root resolves here because
`ResolveScriptDocsFolderID(enabled, callerFolderID, configuredDefault)`
(`internal/kernel/script/docs.go`) only fails closed on `enabled && empty`:
with `docs.enabled: false` and no `docs.folder_id` it still returns the
CONFIGURED DEFAULT. On this host the running master process carries

```
PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID=1J_xUGo_bchzXDIGqSX04CU44c_Dm3SxS   # Overlay Chronon
VELOX_DRIVE_SCRIPTS_ROOT=1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K
VELOX_DRIVE_SCRIPTS_GENERATE=1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K
```

so the resolved documents root is the Overlay Chronon folder — hence the tree
above. The repo's own `.env` carries
`PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID=1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`, i.e. the
deployed value and the checked-in value disagree.

To pin the destination from the payload, set `docs.folder_id` explicitly (an
explicit caller folder always wins): the clips then land in
`<docs.folder_id>/<job id>/<language>`.

## Re-run with an explicit `docs.folder_id` (job_1790092276095209221_a55bdbb5)

The manifest now carries `docs.folder_id = 1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`
(the payload's explicit caller folder), and the same five clips landed exactly
where the routing contract predicts:

```
Il mio Drive
└── Media
    └── Tmp
        └── Clip                      (1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K)  ← docs.folder_id
            └── job_1790092276095209221_a55bdbb5 (1H0n57b-tsQ2DRTkTkzbY28ahlFuagp7J)  15:51:23Z
                ├── en                (1-23kdf4GcfZF3Xx8_8RFI5TaAlxul7hu)  15:51:24Z
                │   ├── yt_Bc9gTqiljLA_0_36_v1.en.36104b99c19e.mp4      10 222 896 B  15:51:27Z
                │   ├── yt_tnoMGevqWAM_1841_1903_v1.en.df350121ff9f.mp4 17 338 958 B  15:51:33Z
                │   ├── yt_pfaIAdqvlig_457_509_v1.en.9059e48c46b8.mp4   17 320 633 B  15:51:33Z
                │   ├── yt_pfaIAdqvlig_929_980_v1.en.6cfcc44640ef.mp4   18 441 799 B  15:51:40Z
                │   └── yt_vLRjqTIiMjc_211_256_v1.en.99d17daecc3e.mp4   12 572 698 B  15:51:39Z
                └── it                (1VvltvmpPg20EJo8gtFy1NVpTNXsUPgqr)  15:52:02Z
                    └── script.json / scenes.json
```

`<docs folder>/<job id>/<language>` is exactly `resolveClipDestination`'s
contract; no `drive_subfolder_name` level exists in this layout (that field is
only read by the clips-root fallback). The five render assets were reused by
fingerprint (`cliprender_36104b99…`, `…df350121…`, `…9059e48c…`, `…6cfcc446…`,
`…99d17dae…` — identical ids to the first run), `render_metrics
successful: 5 / failed: 0`, `wall_ms 20408`: the second run re-published the
certified artifacts instead of re-rendering them.

## Second observation (both runs)

Both runs created a sibling **`it`** document folder although the item declares
`language: "en"` and `docs.enabled: false`:

| run | folder | files |
|---|---|---|
| `job_1790091295751404561_e73027c5` | `1ST6FxPu…/job_…/it/` (15:36:37Z) | `script.json` 20 290 B, `scenes.json` 15 182 B |
| `job_1790092276095209221_a55bdbb5` | `1ST6FxPu…/job_…/it/` (15:52:02Z) | `script.json` 20 006 B, `scenes.json` 15 040 B |

Reproducible, not a one-off: the document projection is written under a
language folder that is neither the item's `language` (`en`) nor gated by
`docs.enabled: false`, while the renders of the same run go under `en/`.
Candidate language source: `config.yaml`'s `media.multilingual.languages` list,
which declares `it` FIRST (then `en`, `pl`, `ru`, `de`, `es`, `pt-BR`).
