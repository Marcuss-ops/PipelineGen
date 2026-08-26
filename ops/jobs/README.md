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
- the Google Doc must show this complete payload inline as `Remote Job Payload JSON`.

## Sound effects

The permanent catalog is `sfx_catalog.json`. Select an effect by alias in the
manifest payload, for example:

```json
{
  "asset_id": "whop1",
  "scene_id": "scene-0",
  "anchor": "end",
  "offset_ms": 0
}
```

`whop1` through `whop6` are resolved/displayed in the final remote payload
with their Drive ID and `velox-drive://` URL. The catalog is only metadata;
an effect is mixed when it is explicitly present in `audio.sound_effects`.

Background music uses `audio.background_music` and the canonical names in
`bgm_catalog.json`, for example `asset_id: "bgm1"` or `asset_id: "bgm3"`.
The six latest links contain four existing Whoop SFX (`whoop1`–`whoop4`) and
two BGM files (`bgm1`, `bgm3`); they remain classified by their actual type.
