# Editorial composition and animation groups

This page separates **what the composition means** from **how its members
animate**. A composition is an editorial unit; an animation group is its shared
timeline and choreography. The request-level opt-in is explicit, so adding a
planner sidecar does not silently change ordinary renders.

## Activation

Micro-summary compositions are off by default. Add this item-level setting to
`POST /api/script/generate` to pass BrainRust's grounded summary sidecar to
RenderingGen:

```json
{
  "version": 3,
  "items": [{
    "source": { "type": "text", "topic": "..." },
    "enable_editorial_compositions": true
  }]
}
```

The setting is carried through the generation request and sealed overlay plan.
It changes the render fingerprint and is applied to every localized overlay
plan for that item. When omitted or `false`, the sidecar stays internal and is
not sent to the renderer. The current renderer opt-in supports the
`summary`/`progressive` member only; any other sidecar member is rejected
instead of being silently dropped.

## Animation groups available in RenderingGen

| Group ID | What the group represents | When members appear | Use it for |
| --- | --- | --- | --- |
| `sequence_v1` | One scene made from coordinated roles: image, phrase, caption, date, or metric. | Members enter in declared order, separated by `stagger_frames`; each holds to its declared end offset or the group end. Motion selection stays stable per role. | A photo with a headline and date/metric, or another mixed editorial beat that should read as one event. |
| `progressive_reveal` | A compact list of connected phrases, such as a local micro-summary. | Phrase entrances must be strictly increasing. Each phrase remains visible after entering and holds through the shared group end. | Build a short takeaway one line at a time without replacing the previous line. |

These are different choreographies: `sequence_v1` staggers unlike layer roles;
`progressive_reveal` accumulates phrase members. RenderingGen's runtime catalog
is the authority for supported roles, motion pools, and deterministic motion
selection. BrainRust owns the semantic choice and timing; it does not choose
physical layout or render assets.

## Composition intents and current activation surface

| Intent | What it represents | Display window | Activation today |
| --- | --- | --- | --- |
| Micro-summary | Two or three nearby, related, source-grounded phrases. | Phrase points enter at their certified source times and hold together to the summary group's end. | `enable_editorial_compositions: true`; otherwise preserved only for audit. |
| Chapter opening | A scene title/card that introduces the next section. | At the start of its scene, using the chapter policy's duration. | `chapter_overlay` (`auto`, `explicit`, or `chapter_only`) is a separate request policy. |
| Chart | A metric series or comparison with typed periods and values. | At the planner-certified scene/series window. | `enable_charts: true`, subject to `disable_chart_overlays` and chart validation. |
| Entity image + date/caption | One entity visual with attached supporting text. | Together over the parent image's window. | The current standalone/entity-card paths work; safe-area attachment lowering is still pending. |
| Persistent background | The previous image/background retained beneath new content. | From its source member through the declared composition window. | Contract and Chronon primitives exist; composition-wide activation is not wired to the normal request path yet. |
| Map photo callout | A verified place image connected to a geographic pin. | Follows the pin's camera samples during the map window. | Contract and camera-aware geometry exist; active map-layer emission is still pending. |

“Activation today” describes a selectable path, not an automatic editorial
choice. The renderer accepts only capabilities explicitly present in the
semantic plan, and missing/unsupported payloads fail closed.

## Timing and responsibility

- BrainRust selects the related content and certifies scene membership, source
  evidence, and timing.
- PipelineGen carries the plan and applies request-level opt-ins; it does not
  infer new editorial meaning.
- RenderingGen validates group capabilities and lowers supported groups to
  physical layers and motion tracks.
- Chronon renders those layers. A group does not imply an intermediate video
  encode/decode or a separate job.

The shared implementation checklist is
[`EDITORIAL-COMPOSITION-WAVE.md`](../tickets/EDITORIAL-COMPOSITION-WAVE.md).
