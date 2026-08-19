# Built-in audio catalog

The generation payload can select the local audio catalog without exposing
Drive IDs or filesystem paths. The admin indexer downloads the catalog into
the configured local media directory; the resolver then uses the local path
and does not download files during rendering.

```json
{
  "audio": {
    "mode": "COMBINED_TIMELINE",
    "mix_policy": "voiceover_with_ducked_clip",
    "background_music": [
      { "asset_id": "bgm1", "end": "video_end" }
    ],
    "sound_effects": [
      { "asset_id": "whoosh3", "at_ms": 1200 },
      { "asset_id": "random_whoosh", "scene_id": "scene_2", "anchor": "end", "offset_ms": -250 }
    ]
  }
}
```

Available selectors are `whoop1`–`whoop4`, `whoosh1`–`whoosh9`, and
`bgm1`–`bgm3`. `random_whoosh` deterministically chooses one of the nine
whoosh cues for each event, keeping retries reproducible.

Built-in BGM defaults to looped playback at `-30 dB`. Built-in whoop/whoosh
effects default to one-shot playback at `-30 dB`. Supplying `gain_db` or the
BGM `loop` field overrides the defaults.

Populate or refresh the local cache with:

```text
pipelinegen admin index-provided-sound-effects
pipelinegen admin download-sound-effects
```

The Rust renderer receives the already resolved, local asset paths and only
performs the final timeline mix; looping and random selection stay outside
the hot render path.
