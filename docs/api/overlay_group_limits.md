# `overlay_group_limits`

Per-group cap on rendered overlays for a `script.generate` item. It sits at the
item level, next to `max_map_overlays`, `max_image_overlays` and
`max_phrase_overlays`.

## Keys

| Key                  | Overlays counted                                              |
| -------------------- | ------------------------------------------------------------- |
| `maps`               | Location map overlays                                         |
| `short_phrases`      | Short grounded phrases (short-phrase motion recipes)          |
| `long_phrases`       | All other grounded phrases                                    |
| `entities_with_text` | Entity cards that carry text                                  |
| `images`             | Image overlays (scene-context and entity-bound imagery)       |

Each overlay belongs to exactly one group. Any other key is rejected with
`overlay_group_limits has unsupported group "<key>"`.

## Values

- **Absent key**: the group keeps the full certified behaviour.
- **`0`**: the group is disabled and all of its overlays are dropped.
- **`N > 0`**: at most `N` overlays of that group are kept, in plan order.
  For `maps`, `images` and phrases this can raise the budget above the
  certified defaults.
- **Negative value**: rejected.

## Example

```json
{
  "id": "example-item",
  "script": "…",
  "max_map_overlays": 2,
  "overlay_group_limits": {
    "maps": 2,
    "short_phrases": 5,
    "long_phrases": 0,
    "entities_with_text": 0,
    "images": 5
  }
}
```

In this example, long phrases and entity text cards are disabled, and the
other groups are capped at the given values.
