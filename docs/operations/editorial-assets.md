# Editorial assets runbook — backgrounds, BGM, SFX

One registry owns every editorial asset: a stable public alias bound to exactly
one Drive identity, resolved through the same media SSOT the renderer reads.

| Domain | Canonical ids | Catalog (the SSOT) |
| --- | --- | --- |
| Video backgrounds | `drive-background-01`..`-06` | `internal/capabilities/mediaregistry/editorial_backgrounds.go` |
| Background music | `bgm1`..`bgm6` | `internal/capabilities/mediaregistry/editorial_catalog.go` |
| Transition SFX | `whop1`..`whop6`, `whoosh1`..`whoosh3` | `internal/capabilities/mediaregistry/editorial_catalog.go` |

Everything else that describes these assets is a **projection**, checked against
the registry by
`internal/capabilities/mediaregistry/editorial_projection_test.go`:

- `ops/jobs/bgm_catalog.json`, `ops/jobs/sfx_catalog.json`
- `ops/jobs/editing_assets_policy.yaml`
- `RenderingGen/assets/backgrounds/manifest.json`

Drift fails the build. Do not treat any of them as a second source of truth.

A repository-wide gate (`editorial_catalog_gate_test.go`) additionally fails if
any *new* file declares editorial aliases together with a Drive identity (or an
`editing_assets` policy root) without being registered against a drift gate.

## The `random_whoosh` directive

`random_whoosh` is a resolver directive, not an asset id. The resolver expands
it to one of `whoosh1`..`whoosh3`, which the catalog binds to a Drive identity,
so the selected cue resolves through the media registry. `whoosh4`..`whoosh9`
stay declared wire vocabulary (an existing payload keeps its built-in
attenuation) but are excluded from the family on purpose: selecting one would
emit an id nothing can resolve. `TestEveryWhooshFamilyAliasIsBoundByTheCatalog`
fails in both directions if the family and the catalog ever diverge again.

## Resolution chains (they are NOT the same)

**Backgrounds** are addressed by their alias **as the registry asset id**:

```
payload background.asset_id = "drive-background-03"
  → ClipRenderPGAssetResolver.ResolveAsset("drive-background-03")
  → media_assets.id = 'drive-background-03'
```

**BGM / SFX** are addressed by an alias that maps to the **Drive identity**,
which is the registry asset id:

```
payload audio.background_music[].asset_id = "bgm3"
  → audio.CanonicalAssetID("bgm3") = "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq"
  → media_assets.id = '1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq'
```

An alias the catalog does not bind (for example `whoosh5`) passes through
unchanged, so the registry lookup is the fail-closed gate — there is no silent
rewrite onto a different asset.

## Registering a background plate

1. Upload the **normalized** plate bytes: video-only (`-an`), 1920x1080,
   30 fps, 15 s, zero audio streams.
2. Register it in the PostgreSQL media SSOT under **`media_assets.id` = the
   alias** (`drive-background-03`), with `media_type = video` and the `sha256`
   declared in the registry.
3. Do **not** register the original supplied Drive file. It carries an AAC
   stream; using it would put a second audio source beneath the master
   voiceover/BGM. That mistake is caught, not merely documented:
   `mediaregistry.ValidateEditorialBackgroundIdentity` runs inside
   `ClipRenderPGAssetResolver.ResolveAsset` and fails closed with
   `clip.render.asset_resolve.background_identity_mismatch`.

`classic1` and other legacy plates are deliberately outside the curated set and
are not subject to that check.

## Selecting an asset a job did not name

`ops/jobs/editing_assets_policy.yaml` is a projection of
`mediaregistry.DefaultEditingAssetsPolicy()`: the pools plus the canonical BGM
gain/loop/duck and SFX gain defaults. Selection is deterministic (SHA-256 over
a caller seed), and an empty pool, an unknown alias, a wrong-family asset or a
positive gain fails closed.

Two consumption points exist today:

- **Remote assembler** — the policy is published in the sealed payload under
  `remote_render.editing_assets_policy`, next to `background_catalog`,
  `background_music_catalog` and `sound_effect_catalog`. This is additive: it
  changes no generated selection.
- **Google Doc** — the same policy is projected into the human document as the
  `Editing Assets Policy JSON` block. Build parity is enforced, not assumed: the
  doc block and the payload are produced by the SAME builder
  (`remoteEditingAssetsPolicy`), and
  `TestDocumentEditingAssetsPolicyHasBuildParityWithTheRemotePayload` fails if
  the two documents ever differ.
- **`scriptgeneration.ApplyEditingAssetPolicy`** — the in-process seam. It fills
  only blank selections (an explicit `mode: none` is preserved), injects BGM
  only in `COMBINED_TIMELINE`, and is **not called implicitly** by anything.

## Verification

```bash
# registry + projection drift gates + policy determinism
go test ./internal/capabilities/mediaregistry/... -count=1

# read-boundary fail-closed check for plates
go test ./internal/capabilities/cliprender/adapters/... -count=1

# published catalogs/policy in the remote payload + the Google Doc block
go test ./internal/capabilities/scripts/ -run 'Remote|DocumentEditingAssetsPolicy' -count=1

# the "no ungated editorial catalog" repository scan
# (allow ~5s: it walks the repo)
go test ./internal/capabilities/mediaregistry/ -run CatalogProjection -count=1

# whole-repo architecture gates
go run ./cmd/archcheck --strict
```

Live checks (require Drive + PostgreSQL) — confirm each id above is present in
`media_assets`, then render a job and confirm its BGM/SFX reach the renderer and
its plate appears in the output.
