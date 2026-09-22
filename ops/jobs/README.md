# Canonical job manifests

## Dolly Parton clip manifests

`dolly_parton_best_moments_50.generate.json` is the canonical 50-clip run and
`dolly_parton_5clips_preview_en.generate.json` is its 5-clip English preview
(the same five ids appear as scenes 0-4 of the 50-clip manifest, so the preview
is a real prefix and its timing is transferable).

Both are `source.type=clips` payloads, which matters for two reasons:

- explicit clip ids **bypass the ClipSampler** — the eleven quality gates
  (`topic_relevance`, `diversity`, `duration`, `coverage`, …) are evaluated only
  on the `search`/`catalog`/`curate` retrieval path. A clip list that is wrong
  is therefore *used*, not filtered: pick the ids deliberately. The preview
  takes one clip per interview (WIRED intro, `Jolene`, Dollywood, Whitney
  Houston, the bronze statue) so four distinct source videos are exercised;
- `script_params.segments` is capped at the deployed `MaxSegmentsCap` = 50, so
the 50-clip manifest sits exactly on the limit. A 51st scene — intro/outro
included, if written as a segment — is rejected `400 TOO_MANY_SEGMENTS`. The
canonical intro/outro contract is the `item.intro` fixed section, never a
segment, and `source.intro_clip_ids` is a retired poison-pill field.

The two payloads are deliberately `language: "en"`: all 73 indexed Dolly clips
own a READY `en` transcript in `asset_text_tracks`, while only 17 own an `it`
one, and a clip without a track in the requested language makes
`ClipSourceBuilder` materialize (translate) it at runtime. Use `it` only when
that extra work is the thing under test.

The preview additionally carries the render lane contract: watermark
`top_right`, burned subtitles (`subs-young`) and a `blur_source` background
behind the foreground clip. `blur_source` is chosen over an `asset` plate so the
test does not depend on a curated plate being registered with its certified
content hash (a mismatched plate fails closed at asset-resolution time).

Neither payload carries a `_comment` key: the envelope body is decoded with
`DisallowUnknownFields`, so a "documentation" key on a generate payload is a
`400`, not a comment. Notes about a payload belong in this file.

The response body of `POST /api/script/generate` is the async envelope
(`ok`, `job_id`, `status`, `status_url`, `current_stage`); poll
`status_url` (`/api/jobs/{id}/full`) for phase transitions and timing.

`dolly_parton_5clips_wm_dualbg_subsstyle.generate.json` is the visual-contract
variant of that preview: the same five clip ids, but the render block asks for
the FULL stack in one request — a top-right text watermark carrying both a
stroke and a shadow (the same blocks the subtitles use), an `asset` background
plate (`drive-background-05`, one of the two certified plates that are actually
registered in the media SSOT) with `foreground_scale_percent: 82` so the plate
stays visible behind the clip, the `subs-young` burned subtitle preset with its
own stroke/shadow, and an `overlay_background` colour for the overlay canvas.
It is also the audio-prefetch stress payload: `audio.background_music` (bgm1),
two `audio.sound_effects` (whoosh1/whop1) and
`mix_policy: VOICEOVER_DUCKED_CLIP` give `PrefetchAudioAssets` real work
(BGM/SFX resolution PLUS clip-audio materialization) while TTS runs.
The manifest carries `docs.folder_id` and that field — not
`output.render.drive_folder_id` / `drive_subfolder_name` — is what decides
where the clips are published: a localized clip lands in
`<resolved documents root>/<job id>/<language>`, and the clips-root spelling is
read only by a run with NO resolvable documents root. Without `docs.folder_id`
the destination falls back to the deployment default
(`PIPELINEGEN_SCRIPT_DOCS_FOLDER_ID`), which on the reference host points at the
Overlay Chronon folder rather than at the scripts root.

Run ids of record (2026-09-22): `job_1790091295751404561_e73027c5` —
SUCCEEDED, 5/5 renders on the remote `renderinggen-host` worker
(`chronon_vulkan`, `verification_passed: 1`); and
`job_1790092276095209221_a55bdbb5` with an explicit
`docs.folder_id = 1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K`, which published the same
five certified artifacts (reused by render fingerprint, `wall_ms 20408`) into
`<scripts root>/<job id>/en/`. Evidence and the observed Drive trees:
`ops/benchmarks/dolly-wm-dualbg-20260922/`.

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

The curated video plates are registered in the SAME registry:
`internal/capabilities/mediaregistry/editorial_backgrounds.go` binds
the generic `drive-background-01`–`drive-background-06` plates plus the
channel aliases `drive-background-boxe`, `drive-background-crime`,
`drive-background-music`, `drive-background-wwe` and
`drive-background-discovery` to their Drive identity, content hash and
normalized contract (`video-background-v1`). RenderingGen's
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

For human-authored generation payloads, `output.render.background` also accepts
the labels `Boxe`, `Crime`, `Music`, `Wwe` and `Discovery`; the ingress builder
resolves them to the canonical channel aliases before rendering.

## Audio prefetch (P1.1) — cosa aspettarsi e come leggerlo

Ogni run `COMBINED_TIMELINE` con BGM/SFX o `mix_policy=VOICEOVER_DUCKED_CLIP`
risolve gli asset audio **in parallelo a TTS e al fan-out dei render**, prima che
l'audio compile ne abbia bisogno. Il contratto verificato (fix 2026-09-22):

- **fail-soft per asset**: un asset che fallisce NON scarta quelli già risolti;
  l'esito resta parziale e la compile risolve il resto per via sincrona
  attraverso lo stesso adapter (cache hit → fall-through).
- **fail-closed sulla wiring**: una sorgente richiesta ma non collegata (nil)
  fallisce subito, come prima.
- **budget bounded** (`audioPrefetchBudget`, 25 s): un asset lento non può
  ritardare il join del fan-out all'infinito; ciò che è in cache resta.
- **BGM/SFX canonicalizzati**: il payload usa l'alias pubblico (`bgm1`), il
  registry la Drive identity: il prefetch risolve con
  `audio.CanonicalAssetID(alias)` — lo stesso id della compile — e mette in
  cache **entrambe** le chiavi.
- **dedup**: id ripetuti (lo stesso clip in due scene) vengono risolti una volta.

L'esito è nel payload di polling, in `result.result.audio_prefetch`:

```json
{"bgm_requested":1,"sfx_requested":2,"clip_audio_requested":5,
 "bgm_sfx_resolved":3,"clip_audio_ready":5,"duration_ms":21283,"degraded":false,
 "assets":[{"kind":"clip_audio","asset_id":"yt_…","ok":true,"duration_ms":17423}, …]}
```

`degraded: true` + `failures[]` significa "parte dell'I/O è stata pagata in
`audio_compile`": non è un errore, ma è il segnale che il run non ha usato il
prefetch per intero. Con `clip_audio_prepare_ms > 0` in `audio_metrics` si vede
quanto è costata quella parte (0 = tutto servito dalla cache del prefetch).

## Correlazione del lavoro remoto (master → coda → worker)

Il job di render inviato alla coda RenderingGen porta `parent_job_id` = id di
correlazione del run (`pkg/corid`), quindi `GET http://127.0.0.1:8081/jobs/{id}`
permette di risalire dal render remoto al job del master. Senza di esso l'unica
chiave era la plan revision (`yt_<clip>/<lang>/overlay-v3/<hash>`), che identifica
l'artefatto ma non il run che l'ha chiesto.

Per raccogliere l'intera catena (stage/timing del master, decisioni prefetch,
record della coda, log del worker GPU, telemetria, cache locali, destinazioni
Drive) in un unico report:

```bash
scripts/collect_chain_debug.sh <job_id>
# → ops/benchmarks/chain-debug/<job_id>.md (+ raw artifacts accanto)
```

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
