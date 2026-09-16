# Canonical job manifests

`matt_damon_20_clips_profiling.generate.json` is the canonical source of
truth for the Matt Damon 20-clip profiling job. Future submissions and
Google Docs payload sections must be derived from this manifest, not from
the legacy root-level `matt_damon.generate.json` or from a filename-only
`body_file` reference.

The contract is permanent:

- 20 main clip segments, plus one `intro` and one `outro` segment;
- intro and outro have `target_words: 0` and never invoke LLM generation;
- audio uses `COMBINED_TIMELINE`: the remote assembler receives one published
  certified final-audio master and never receives one voiceover file per scene;
- the manifest is the complete `script.generate` item payload;
- a rerun changes only the idempotency key (and keeps the same structure);
- the complete payload remains an internal job/provenance surface and is not
  rendered into the human-facing Google Doc.

## Sound effects

The canonical catalog is `internal/capabilities/mediaregistry/editorial_catalog.go`.
`sfx_catalog.json` and `bgm_catalog.json` are human-readable projections. Select
an effect by canonical alias in the manifest payload, for example:

```json
{
  "asset_id": "whop1",
  "scene_id": "scene-0",
  "anchor": "end",
  "offset_ms": 0
}
```

`whop1` through `whop6` are resolved/displayed in the final remote payload
with their Drive ID and `velox-drive://` URL. `whoosh1` through `whoosh3` are
bound the same way, so the `random_whoosh` directive always expands to an alias
that resolves; `whoosh4`..`whoosh9` remain declared wire vocabulary but are
unbound and are deliberately excluded from the directive's family. The catalog
is only metadata; an effect is mixed when it is explicitly present in
`audio.sound_effects`.

Background music uses `audio.background_music` and the canonical names in the
shared registry, for example `asset_id: "bgm1"` or `asset_id: "bgm3"`.
The six BGM links are permanently classified as `bgm1`–`bgm6`. The separate
six Whoop links are permanently classified as `whop1`–`whop6`; the two groups
must not be mixed when selecting assets.

## Backgrounds

The six curated video plates are registered in the SAME registry:
`internal/capabilities/mediaregistry/editorial_backgrounds.go` binds
`drive-background-01`–`drive-background-06` to their Drive identity, content
hash and normalized contract (`video-background-v1`). RenderingGen's
`assets/backgrounds/manifest.json` is the projection of that catalog, not a
second source of truth, and `editorial_projection_test.go` fails the build if
the two diverge.

A background is addressable in two distinct payload fields, and the two are NOT
interchangeable: `output.render.background` is the plate behind the final clip
(`mode: asset|blur_source|none`), while `overlay_background` is the full-canvas
backdrop of the Chronon overlay render (`kind: color|image|video`). They have
different roles and are deliberately not unified, but both resolve through the
same registry.

### Registering a plate (the contract)

A plate must be registered in the media SSOT **under its canonical id**
(`media_assets.id = 'drive-background-0N'`) with the **certified normalized
bytes**: video-only (`-an`), 1920x1080, 30 fps, 15 s, matching the `sha256` in
the registry. The original supplied Drive file is a different artifact — it
carries an ACC audio stream — so uploading it under a plate id would put a
second audio source beneath the master voiceover/BGM.

The canonical writer is `go run ./cmd/admin register-editorial-assets`
(`wiring/media.EnsureEditorialAssets`), which derives the id, the certified
hash and the Drive identity from the registry and is idempotent on
`media_assets.id`. Do not hand-write the row: an `INSERT` can register
arbitrary bytes and bypass the identity check below.

That rule is enforced, not merely documented:
`mediaregistry.ValidateEditorialBackgroundIdentity` is checked by the
`clip.render` PostgreSQL asset resolver, so a plate registered with the wrong
content hash **fails closed at asset-resolution time** instead of rendering the
wrong artifact. `classic1` and other legacy plates are deliberately outside the
curated set and are unaffected.

## Selection policy

`editing_assets_policy.yaml` is a projection of
`mediaregistry.DefaultEditingAssetsPolicy()`. It answers "which asset does a job
pick when it does not name one?" for backgrounds, BGM and transition SFX,
together with the canonical mix defaults. It is a POLICY, not a catalog: add or
retire an asset in the catalog, change pool membership/defaults here. Selection
is deterministic (SHA-256 over a caller seed) so a rerun is byte-identical, and
a pool naming an unknown alias or an asset of the wrong family fails closed.

The opt-in integration seam is
`scriptgeneration.ApplyEditingAssetPolicy`: it fills a clip background only when
the request left it blank (an explicit mode, including `none`, is preserved)
and a BGM layer only when the request declared none AND is in
`COMBINED_TIMELINE`. It never overrides a caller selection and is not called
implicitly; a transition SFX is not auto-placed because its position depends on
the scene timeline, which does not exist at request-build time.

## Projection drift gates

Every `*_catalog.json` / policy / manifest copy in this directory and in
RenderingGen is checked against the Go registry by
`internal/capabilities/mediaregistry/editorial_projection_test.go`. A copy that
diverges from the registry is a build failure, so none of them can silently
become an independent source of truth again.

On top of that, `editorial_catalog_gate_test.go` scans the repository for files
that declare editorial aliases together with a Drive identity (or an
`editing_assets` policy root) and fails unless the file is registered with the
drift gate that keeps it a projection. Adding a new editorial catalog anywhere
without a gate is a build failure, not something a reviewer has to notice.
