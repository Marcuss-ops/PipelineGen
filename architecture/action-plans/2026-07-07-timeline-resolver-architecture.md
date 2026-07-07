# TIMELINE-RESOLVER-2026-07-07 — Action Plan

> **Source**: User-provided architectural specification for a centralized
> timeline resolution system in a C++ video rendering/compositing engine.
> **Authoring context**: The current rendering pipeline has temporal logic
> scattered across the renderer, animators, video samplers, and content
> layers — with `if frame` checks, `duration==1` static hacks, and
> `source_frame` calculated in 5+ different places.
>
> This plan codifies a **TimelineResolver-first architecture** where
> temporal decisions are centralized in one resolver, and the renderer,
> animators, and processors receive only pre-resolved time contexts.

> **Core paradigm**:
> ```
> Global time   = frame del video finale
> Local time    = frame dentro una sequence/precomp/clip
> Media time    = frame reale dentro un video/audio sorgente
> ```

> **godlike/06 3-surface lockstep (per CANONICAL.md §1)**:
> 1. `architecture/action-plans/2026-07-07-timeline-resolver-architecture.md` (this file — narrative)
> 2. `architecture/current.yaml#TIMELINE-RESOLVER-2026-07-07` (wave-tracker entry, TBD)
> 3. `CHANGELOG.md` `## Unreleased → ### Documentation` (closure meta-entry)
> 4. `AGENTS.md` §Recent cross-cutting closures (mirror entry)

---

## 1. Honest disclosure (godlike/07 no-fake-availability)

This specification is **language-agnostic architectural guidance** for a C++
video rendering engine. The PipelineGen codebase (Go) does not contain a
video compositor; this plan serves as a **future reference** for when/if
the rendering subsystem is built.

**The 10 sections below are a direct transcription of the user's technical
spec with action-plan structure applied.** No code changes land in
PipelineGen from this plan — it is a pure architectural document.

**Carry-forward preservation**: the 6 pre-existing build issues from
`architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` are NOT
regressions of this plan (documentation-only).

---

## 2. Architecture Overview

### The 3-level time model

| Level | Definition | Resolved by |
|-------|-----------|-------------|
| `global_frame` | Frame of the final output video | Root composition |
| `local_frame` | Frame within a sequence/precomp/clip | `map_sequence_time()` |
| `media_frame` | Frame within a source video/audio file | `resolve_media_frame()` |

### The pipeline

```
Composition
  ↓
TimelineResolver        ← decides WHAT is active & at what time
  ↓
ResolvedScene           ← flat list of layers with TimeContext
  ↓
AssetPreflightResolver  ← decides dependencies
  ↓
RenderGraphBuilder      ← constructs the GPU/render graph
  ↓
Renderer                ← draws (no time logic)
```

---

## 3. Implementation Phases (6)

### FASE A — Tipi base (P0, deadline TBD)

Add foundational types:

```
TimeContext {
    global_frame, parent_frame, local_frame,
    sequence_start, fps, local_seconds, scope_path
}
SequenceSpec {
    from, duration?, trim_before, trim_after?,
    freeze, freeze_at
}
SequenceNode { name, spec, children }
TimelineNode = SequenceNode | LayerNode | MediaNode
TimelineResolver { resolve(Composition, Frame) → ResolvedScene }
ResolvedScene { layers, time_context }
ResolvedLayer { node, time_context, active }
```

**Key invariant**: root implicit sequence — `from=0, duration=composition.duration, local_frame=global_frame`.

### FASE B — Root implicita (P0, deadline TBD)

Every composition gets an implicit root sequence:
- `from = 0`
- `duration = composition.duration`
- `local_frame = global_frame`

This ensures code without explicit sequences continues to work.

### FASE C — Adapter legacy (P1, deadline TBD)

Map old `layer.from / layer.duration` into an implicit `SequenceNode`:
```
SequenceNode {
    name = layer.name + "_legacy_sequence",
    spec = { .from = layer.from, .duration = layer.duration },
    children = { layer }
}
```

This prevents breaking existing content during migration.

### FASE D — Animator local frame (P1, deadline TBD)

Modify all animators:
- **Before**: `sample(anim, Frame frame)` — uses global frame
- **After**: `sample(anim, const AnimationSampleContext& ctx)` → `ctx.local_frame`

**AnimationSampleContext**:
```
AnimationSampleContext {
    .global_frame = time.global_frame,
    .local_frame  = time.local_frame,
    .fps          = time.fps,
    .scope_path   = time.scope_path
};
```

Default: **animations use `local_frame`**, not `global_frame`.

### FASE E — Migra content (P1, deadline TBD)

All new content must use `s.sequence(...)` instead of `if frame` checks:
```
// OLD (da eliminare)
if (ctx.frame >= Frame{30} && ctx.frame < Frame{90}) {
    l.text(...);
}

// NEW (canonical)
s.sequence("title", {.from = Frame{30}, .duration = Frame{60}},
    [](SequenceBuilder& seq) {
        seq.layer("title", [](LayerBuilder& l) {
            l.text(...);
        });
    });
```

### FASE F — Elimina legacy (P2, deadline TBD)

When tests are green, eliminate:
- `Layer.from/duration` nel render graph
- `if frame` nei content
- `duration==1` static path
- `source_frame` calcolato nei video node
- Animator che campiona `global_frame`
- Temporal skip nei processor

---

## 4. Component Details

### 4.1 TimelineResolver

```cpp
class TimelineResolver {
public:
    ResolvedScene resolve(const Composition& comp, Frame global_frame) const;
};
```

The renderer receives only pre-resolved scenes. The resolver owns
ALL temporal decisions:
- Is this layer active?
- Has this sequence started?
- Does this animation use global or local frame?
- At what source frame should this video start?

### 4.2 TimeContext (unified)

```cpp
struct TimeContext {
    Frame global_frame{0};
    Frame parent_frame{0};
    Frame local_frame{0};
    Frame sequence_start{0};
    double fps{30.0};
    double local_seconds{0.0};
    std::string scope_path;     // "root/intro/title"
};
```

### 4.3 Sequence = temporal mapping, not layer

```cpp
struct SequenceSpec {
    Frame from{0};
    std::optional<Frame> duration{};
    Frame trim_before{0};
    std::optional<Frame> trim_after{};
    bool freeze{false};
    Frame freeze_at{0};
};

struct TimeMappingResult {
    bool active{false};
    Frame local_frame{0};
};

TimeMappingResult map_sequence_time(
    const SequenceSpec& spec, Frame parent_frame
) {
    if (parent_frame < spec.from) return {.active = false};
    Frame raw = parent_frame - spec.from;
    if (spec.duration && raw >= *spec.duration) return {.active = false};
    Frame local = raw + spec.trim_before;
    if (spec.trim_after && local >= *spec.trim_after) return {.active = false};
    if (spec.freeze) local = spec.freeze_at;
    return {.active = true, .local_frame = local};
}
```

### 4.4 API Builder (clean)

```cpp
s.sequence("intro", {.from = Frame{0}, .duration = Frame{30}},
    [](SequenceBuilder& seq) {
        seq.layer("logo", [](LayerBuilder& l) {
            l.text("logo", centered_text({.text = "INTRO"}));
        });
    });

s.sequence("title", {.from = Frame{30}, .duration = Frame{60}},
    [](SequenceBuilder& seq) {
        seq.layer("title", [](LayerBuilder& l) {
            l.opacity_anim()
                .key(Frame{0}, 0.0f)
                .key(Frame{20}, 1.0f);
            l.text("title", centered_text({.text = "TITLE"}));
        });
    });
```

Frame mapping example:
- `global_frame = 30` → `local_frame = 0` → opacity starts at 0
- `global_frame = 50` → `local_frame = 20` → opacity = 1.0

### 4.5 Nested Sequences

```cpp
s.sequence("chapter", {.from = Frame{100}},
    [](SequenceBuilder& chapter) {
        chapter.sequence("title", {.from = Frame{20}, .duration = Frame{40}},
            [](SequenceBuilder& title) {
                title.layer("text", [](LayerBuilder& l) {
                    l.text("label", centered_text({.text = "NESTED"}));
                });
            });
    });
```

Nesting resolver:
```cpp
void resolve_sequence(
    const SequenceNode& seq,
    const TimeContext& parent_time,
    ResolvedScene& out
) {
    auto mapped = map_sequence_time(seq.spec, parent_time.local_frame);
    if (!mapped.active) return;

    TimeContext child_time = parent_time;
    child_time.parent_frame = parent_time.local_frame;
    child_time.local_frame = mapped.local_frame;
    child_time.local_seconds = mapped.local_frame.value / parent_time.fps;
    child_time.scope_path += "/" + seq.name;

    for (const auto& child : seq.children)
        resolve_timeline_node(child, child_time, out);
}
```

Frame mapping for nested: `global_frame=120` → `chapter local=20` → `title local=0`.

### 4.6 Media Time (separate resolver)

```cpp
struct MediaTimeSpec {
    Frame trim_before{0};
    Frame trim_after{0};
    double playback_rate{1.0};
    bool freeze{false};
    Frame freeze_at{0};
};

Frame resolve_media_frame(Frame local_frame, const MediaTimeSpec& spec) {
    if (spec.freeze) return spec.freeze_at;
    double source = static_cast<double>(local_frame.value) * spec.playback_rate;
    return spec.trim_before + Frame{static_cast<int>(std::floor(source))};
}
```

Flow: `global_frame → sequence local_frame → media source_frame`.

### 4.7 TemporalAnalysis (static detection)

```cpp
TemporalAnalysis analyze_temporal_dependencies(const TimelineNode& node);

struct TemporalAnalysis {
    bool frame_dependent{false};
    bool local_time_dependent{false};
    bool media_time_dependent{false};
};
```

Cache static only if: `!analysis.frame_dependent`.

---

## 5. Things to Eliminate (7 anti-patterns)

| # | Anti-pattern | Replacement |
|---|-------------|-------------|
| 1 | `if frame` inside content | `s.sequence("name", {.from, .duration}, ...)` |
| 2 | Animations based on `global_frame` | `anim.sample(ctx.time.local_frame)` |
| 3 | `Layer.from` / `Layer.duration` in render graph | `TimelineResolver → ResolvedScene → RenderGraphBuilder` |
| 4 | `duration == 1` as static signal | `TemporalAnalysis::frame_dependent` |
| 5 | Duplicate temporal names (from/start/begin/in_point/offset/duration/length/end/to) | Unified: `from, duration, trim_before, trim_after, local_frame, global_frame, media_frame` |
| 6 | `source_frame` calculated in renderer | `MediaTimeResolver::resolve(local_frame, media_spec)` |
| 7 | Temporal skip in processors (`if (!active_at_frame) return empty`) | Processor assumes: "se mi hai chiamato, devo renderizzare" |

---

## 6. Mandatory Tests (9)

| # | Test | Expected |
|---|------|----------|
| 1 | `global 29 → sequence from 30` | NOT active |
| 2 | `global 30 → local 0` | Active, local_frame=0 |
| 3 | `global 50 → local 20` | Active, local_frame=20 |
| 4 | `sequence duration 60 → global 90` | NOT active |
| 5 | Nested sequence → local frame | Correct nesting (chapter local=20, title local=0) |
| 6 | Anim inside sequence | Starts from local_frame=0 |
| 7 | `trim_before 10` | local_frame=0 → media_frame=10 |
| 8 | `freeze_at 15` | All frames use media_frame=15 |
| 9 | `playback_rate 2.0` | local_frame=10 → media_frame=20 |

---

## 7. Unified Vocabulary

| Term | Meaning |
|------|---------|
| `from` | Start frame of a sequence |
| `duration` | Length of a sequence (optional = infinite) |
| `trim_before` | Frames to skip at start of source media |
| `trim_after` | Frame at which source media ends |
| `local_frame` | Frame within the current sequence |
| `global_frame` | Frame of the final output video |
| `media_frame` | Frame within a source video/audio file |

---

## 8. Honest limitations (godlike/07)

1. **Language mismatch**: this is a C++ specification; PipelineGen is Go.
   This plan is a reference document for a future rendering subsystem.
2. **No C++ codebase exists** in this repo — all implementation phases
   require a new C++ project or integration into an existing compositor.
3. **The 6 pre-existing build issues** carry forward unchanged — NOT
   regressions of this plan.
4. **Deadlines are TBD** because this is an architectural reference,
   not an active wave. Each phase should get its own deadline when
   the rendering subsystem project is initialized.
5. **Tests are specified but not implemented** — they are behavioral
   contracts for future implementors.

---

## 9. Cross-references

- `architecture/current.yaml#TIMELINE-RESOLVER-2026-07-07` (wave-tracker entry, TBD)
- AGENTS.md Pattern 0 — Port abstraction layer
- AGENTS.md Pattern 5 — God-object split discipline
- AGENTS.md Git-Lesson-2 — Direct-to-main workflow
- AGENTS.md Git-Lesson-3 — Co-authored-by trailer discipline
- godlike/06 SSOT (one canonical owner per fact)
- godlike/07 no-fake-availability, typed-error contract, minimum-blast-radius

---

## 10. Lifecycle (audit trail)

- **2026-07-07**: this plan created from user-provided C++ architectural spec.
  Status: `pending` (reference document). No PR shipped.
- **TBD**: FASE A (tipi base) implementation start.
- **TBD**: FASE F (elimina legacy) — final wave closure.

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
(per AGENTS.md Git-Lesson-3 auditability convention)
