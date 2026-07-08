# Script Legacy → V2 Mapping Audit (2026-08-08)

> **Status:** AUDIT-ONLY (no code changes). Decision document supporting the
> retirement-decision trail for the 410-Gone routes. Companion to
> `architecture/reports/legacy-route-usage-2026-06-28.md` (pre-Commit-2
> inventory) and `internal/api/script/handler_legacy_deprecation.go` (the
> canonical 410 contract owner post-Commit-2).

**Generated:** 2026-08-08 (post-Commit-2 canonical SHA `3fe456b88`)
**Owner capability:** `internal/api/script`
**godlike/06 SSOT surface (one canonical owner per fact):** this file is
the canonical SSOT for the v2 wire-shape supersession verdict; the
canonical 410 wire shape lives in `handler_legacy_deprecation.go`; the
canonical v2 types live in `internal/domain/script/{generation_envelope,
source_spec, output_spec, downstream}.go`.
**Related PRs:**
- `PR-script-legacy-contract` (Jul 2026, canonical SHA `461b71a4`) —
  retired the 2 routes to 410-Gone + wired observability counters.
- `PR-LEGACY-RETIRE-F2` (Aug 2026, canonical SHA `3fe456b88`) — physically
  deleted the 4 legacy request types (the structs referenced in this
  audit) + 2 test files + the `warnIgnoredLegacyFields` helper. The
  legacy structs were retrieved from git history `3fe456b88~1` for this
  audit.

---

## §0 Background

The 2 legacy routes `POST /api/script/generate-from-clips` and
`POST /api/script/generate-with-images` are now permanently retired to
the canonical 410-Gone contract (`PR-script-legacy-contract` Jul 2026;
the handlers stay in `handler_legacy_{from_clips,with_images}.go` as
410-emitters). The pre-410 request types (the structs that previously
parsed the request body) were physically deleted in `PR-LEGACY-RETIRE-F2`
(Aug 2026) because they had zero live callers post-410-flip.

This audit answers the question: **does the canonical v2 wire shape
(`GenerationEnvelopeV2` + `GenerationItemV2` + `SourceSpec` +
`ScriptSpec` + `OutputSpec`) preserve all the semantics that the legacy
types used to carry?** If any legacy field has semantics NOT captured
by v2, the 410 retirement would silently drop capability for any
caller that was relying on the legacy semantics — and the
`POST /api/script/generate` migration path would need a documented
forward-compat story.

**Verdict up-front** (§5 for full reasoning): **the v2 wire shape
captures all the legacy semantics that mattered in practice**; the
4 fields that have no v2 mapping (see §4.1) are either v1-era
conveniences that v2 properly retired (deliberate contract
strengthening) or had no caller-side use cases post-FASE-2.1. **No live
caller is broken by the 410 retirement.**

---

## §1 Legacy types — pre-Commit-2 surface

Both structs were physically deleted in `PR-LEGACY-RETIRE-F2` (commit
`3fe456b88`). The verbatim content below was retrieved from git history
`3fe456b88~1:internal/api/script/handler_legacy_{from_clips,with_images}.go`.

### 1.1 `LegacyGenerateFromClipsRequest` — 36 fields + `LegacyClipInput` sub-struct

| # | Field | Type | JSON tag |
|---|-------|------|----------|
| 1 | `Topic` | `string` | `topic` |
| 2 | `SourceText` | `string` | `source_text` |
| 3 | `Title` | `string` | `title` |
| 4 | `Language` | `string` | `language` |
| 5 | `Tone` | `string` | `tone` |
| 6 | `Model` | `string` | `model` |
| 7 | `Style` | `string` | `style` |
| 8 | `ClipIDs` | `[]string` | `clip_ids` |
| 9 | `Clips` | `[]LegacyClipInput` | `clips` |
| 10 | `IntroClipIDs` | `[]string` | `intro_clip_ids` |
| 11 | `IntroClips` | `[]string` | `intro_clips` |
| 12 | `NumClips` | `int` | `num_clips` |
| 13 | `TargetWords` | `int` | `target_words` |
| 14 | `Duration` | `int` | `duration` |
| 15 | `SegmentWords` | `int` | `segment_words` |
| 16 | `SegmentTopics` | `[]string` | `segment_topics` |
| 17 | `SaveToDB` | `bool` | `save_to_db` |
| 18 | `ForceRefresh` | `bool` | `force_refresh` |
| 19 | `GenerateSceneImages` | `bool` | `generate_scene_images` |
| 20 | `GenerateVoiceover` | `bool` | `generate_voiceover` |
| 21 | `GenerateDocument` | `bool` | `generate_document` |
| 22 | `GenerateDoc` | `bool` | `generate_doc` |
| 23 | `ExtractEntities` | `bool` | `extract_entities` |
| 24 | `GenerateMetadata` | `bool` | `generate_metadata` |
| 25 | `DriveFolderID` | `string` | `drive_folder_id` |
| 26 | `EnableSceneImages` | `bool` | `enable_scene_images,omitempty` |
| 27 | `SentencesPerImage` | `int` | `sentences_per_image,omitempty` |
| 28 | `MinQualityScore` | `float64` | `min_quality_score,omitempty` |
| 29 | `StyleInstructions` | `string` | `style_instructions` |
| 30 | `Guidelines` | `string` | `guidelines` |
| 31 | `CustomPrompt` | `string` | `custom_prompt` |
| 32 | `SystemPrompt` | `string` | `system_prompt` |
| 33 | `VoiceoverGroup` | `string` | `voiceover_group` |
| 34 | `VoiceoverFolderID` | `string` | `voiceover_folder_id` |
| 35 | `TranscriptPolicy` | `string` | `transcript_policy` |
| 36 | `PromptVersion` | `string` | `prompt_version` |

`LegacyClipInput` sub-struct (used by field #9 `Clips`):
| Field | Type | JSON tag |
|-------|------|----------|
| `ClipID` | `string` | `clip_id` |
| `Title` | `string` | `title,omitempty` |
| `URL` | `string` | `url,omitempty` |

### 1.2 `LegacyGenerateWithImagesRequest` — 22 fields

| # | Field | Type | JSON tag |
|---|-------|------|----------|
| 1 | `Topic` | `string` | `topic` |
| 2 | `SourceText` | `string` | `source_text` |
| 3 | `Title` | `string` | `title` |
| 4 | `Language` | `string` | `language` |
| 5 | `Tone` | `string` | `tone` |
| 6 | `Model` | `string` | `model` |
| 7 | `Style` | `string` | `style` |
| 8 | `ClipIDs` | `[]string` | `clip_ids` |
| 9 | `NumClips` | `int` | `num_clips` |
| 10 | `TargetWords` | `int` | `target_words` |
| 11 | `Duration` | `int` | `duration` |
| 12 | `SegmentWords` | `int` | `segment_words` |
| 13 | `SegmentTopics` | `[]string` | `segment_topics` |
| 14 | `SaveToDB` | `bool` | `save_to_db` |
| 15 | `ForceRefresh` | `bool` | `force_refresh` |
| 16 | `DriveFolderID` | `string` | `drive_folder_id` |
| 17 | `GenerateDocument` | `*bool` | `generate_document,omitempty` |
| 18 | `StyleInstructions` | `string` | `style_instructions` |
| 19 | `VoiceoverGroup` | `string` | `voiceover_group` |
| 20 | `VoiceoverFolderID` | `string` | `voiceover_folder_id` |
| 21 | `TranscriptPolicy` | `string` | `transcript_policy` |
| 22 | `PromptVersion` | `string` | `prompt_version` |

**Notable from-clips vs with-images differences** (36 vs 22 fields):
- `with-images` is a strict subset of `from-clips` (no new fields)
- `from-clips` adds: `Clips[]LegacyClipInput` + `IntroClipIDs` + `IntroClips` + `GenerateSceneImages` + `GenerateVoiceover` + `GenerateDoc` + `ExtractEntities` + `GenerateMetadata` + `EnableSceneImages` + `SentencesPerImage` + `MinQualityScore` + `CustomPrompt` + `SystemPrompt` (14 additional fields)
- `from-clips` `GenerateDocument` is `bool`; `with-images` is `*bool` (semantic divergence — same name, different nullability)
- `from-clips` has both `GenerateDocument` AND `GenerateDoc` (likely a typo / backward-compat shim)
- `from-clips` has both `GenerateSceneImages` AND `EnableSceneImages` (likely a typo / backward-compat shim)

---

## §2 Canonical V2 types — live on `origin/main` post-Commit-2

### 2.1 `GenerationItemV2` — identity (10 fields)

| Field | Type | JSON tag |
|-------|------|----------|
| `ID` | `string` | `id,omitempty` |
| `Title` | `string` | `title,omitempty` |
| `Language` | `string` | `language,omitempty` |
| `Tone` | `string` | `tone,omitempty` |
| `Style` | `string` | `style,omitempty` |
| `Model` | `string` | `model,omitempty` |
| `Source` | `SourceSpec` | `source` |
| `ScriptParams` | `ScriptSpec` | `script_params,omitempty` |
| `Output` | `OutputSpec` | `output,omitempty` |

### 2.2 `SourceSpec` — source side (15 fields, discriminated by `Type`)

| Field | Type | JSON tag |
|-------|------|----------|
| `Type` | `SourceType` | `type` |
| `Topic` | `string` | `topic,omitempty` |
| `SourceText` | `string` | `source_text,omitempty` |
| `Guidelines` | `string` | `guidelines,omitempty` |
| `ClipIDs` | `[]string` | `clip_ids,omitempty` |
| `IntroClipIDs` | `[]string` | `intro_clip_ids,omitempty` |
| `NumClips` | `int` | `num_clips,omitempty` |
| `Query` | `string` | `query,omitempty` |
| `MaxClips` | `int` | `max_clips,omitempty` |
| `MinCoverage` | `float64` | `min_coverage,omitempty` |
| `MinQualityScore` | `*float64` | `min_quality_score,omitempty` |
| `MinTranscriptWords` | `*int` | `min_transcript_words,omitempty` |
| `TranscriptPolicy` | `string` | `transcript_policy,omitempty` |
| `OrderingStrategy` | `string` | `ordering_strategy,omitempty` |
| `ForceRefresh` | `bool` | `force_refresh,omitempty` |
| `Search` | `bool` | `search,omitempty` |
| `AllowTextOnly` | `bool` | `allow_text_only,omitempty` |
| `SourceFilter` | `string` | `source_filter,omitempty` |
| `MediaTypeFilter` | `string` | `media_type_filter,omitempty` |

`SourceType` enum: `text` | `clips` | `catalog` | `search` | `curate`.

### 2.3 `ScriptSpec` — script generation behavior (16 fields)

| Field | Type | JSON tag |
|-------|------|----------|
| `TargetWords` | `int` | `target_words,omitempty` |
| `Duration` | `int` | `duration,omitempty` |
| `MinWords` | `int` | `min_words,omitempty` |
| `SegmentWords` | `int` | `segment_words,omitempty` |
| `SegmentTopics` | `[]string` | `segment_topics,omitempty` |
| `SentencesPerImage` | `int` | `sentences_per_image,omitempty` |
| `ImagesPerScene` | `int` | `images_per_scene,omitempty` |
| `Style` | `string` | `style,omitempty` |
| `Guidelines` | `string` | `guidelines,omitempty` |
| `TranscriptPolicy` | `string` | `transcript_policy,omitempty` |
| `OrderingStrategy` | `string` | `ordering_strategy,omitempty` |
| `PromptVersion` | `string` | `prompt_version,omitempty` |
| `EditorPromptVersion` | `string` | `editor_prompt_version,omitempty` |
| `QAPromptVersion` | `string` | `qa_prompt_version,omitempty` |
| `ForceRefresh` | `bool` | `force_refresh,omitempty` |
| `UseMemory` | `bool` | `use_memory,omitempty` |

### 2.4 `OutputSpec` — post-generation artifacts (14 fields)

| Field | Type | JSON tag |
|-------|------|----------|
| `ExtractEntities` | `bool` | `extract_entities,omitempty` |
| `GenerateMetadata` | `bool` | `generate_metadata,omitempty` |
| `GenerateVoiceover` | `bool` | `generate_voiceover,omitempty` (deprecated, no-op — see §4.3) |
| `GenerateSceneImages` | `bool` | `generate_scene_images,omitempty` (deprecated, no-op) |
| `GenerateDocument` | `bool` | `generate_document,omitempty` |
| `SaveToDB` | `bool` | `save_to_db,omitempty` |
| `GenerateTimeline` | `bool` | `generate_timeline,omitempty` |
| `VoiceoverGroup` | `string` | `voiceover_group,omitempty` |
| `VoiceoverFolderID` | `string` | `voiceover_folder_id,omitempty` |
| `DriveFolderID` | `string` | `drive_folder_id,omitempty` |
| `MaxChars` | `int` | `max_chars,omitempty` |
| `OutputFmt` | `string` | `output_fmt,omitempty` |
| `Languages` | `[]string` | `languages,omitempty` |

### 2.5 `GenerationEnvelopeV2` — top-level (4 fields)

| Field | Type | JSON tag |
|-------|------|----------|
| `Version` | `int` | `version` |
| `Preset` | `Preset` | `preset` |
| `CorrelationID` | `string` | `correlation_id,omitempty` |
| `Items` | `[]GenerationItemV2` | `items` |

---

## §3 Field-by-field mapping table

**Legend:**
- `✓ MAPPED` — v2 has a direct equivalent; semantics preserved 1:1
- `⚠ PARTIAL` — v2 has an equivalent but semantics changed (split / pointer / deprecated)
- `✗ NOT-MAPPED` — no v2 equivalent; the field is retired
- `🔄 MERGED` — legacy had duplicate fields; v2 collapsed to one

### 3.1 `LegacyGenerateFromClipsRequest` (36 fields)

| # | Legacy field | V2 mapping | Status | Notes |
|---|--------------|-----------|--------|-------|
| 1 | `Topic` | `SourceSpec.Topic` (when `Type=text`) | ✓ | Both set contemporaneously IS supported — see §4.3.2. |
| 2 | `SourceText` | `SourceSpec.SourceText` (when `Type=text`) | ✓ | See §4.3.2. |
| 3 | `Title` | `GenerationItemV2.Title` | ✓ | Same wire shape. |
| 4 | `Language` | `GenerationItemV2.Language` | ✓ | Plus `SourceResolutionContext.Language` (resolution-time duplicate). |
| 5 | `Tone` | `GenerationItemV2.Tone` | ✓ | Plus `SourceResolutionContext.Tone`. |
| 6 | `Model` | `GenerationItemV2.Model` | ✓ | Plus `SourceResolutionContext.Model`. |
| 7 | `Style` | `GenerationItemV2.Style` | ⚠ | v2 distinguishes item-level (`GenerationItemV2.Style`) from source-level (`SourceSpec.Guidelines`) and script-level (`ScriptSpec.Style`). Legacy had one ambiguous `Style` field. |
| 8 | `ClipIDs` | `SourceSpec.ClipIDs` (when `Type=clips`) | ✓ | Same wire shape. |
| 9 | `Clips []LegacyClipInput{ID,Title,URL}` | (no direct mapping) | ✗ | See §4.1.1 — the Title/URL pre-specification was a v1 convenience; v2 forces lookup-time resolution via `ClipEvidence.ClipNames` + `ClipEvidence.DriveLinks`. |
| 10 | `IntroClipIDs` | `SourceSpec.IntroClipIDs` | ✓ | Same wire shape. |
| 11 | `IntroClips []string` | (no direct mapping) | ✗ | See §4.1.2 — v1 leftover; v2 only has `IntroClipIDs`. |
| 12 | `NumClips` | `SourceSpec.NumClips` (when `Type=clips`) | ⚠ | v2 has it in BOTH `SourceSpec.NumClips` AND `SourceResolutionContext.NumClips` (resolution-time duplicate). |
| 13 | `TargetWords` | `ScriptSpec.TargetWords` | ⚠ | Plus `SourceResolutionContext.TargetWords`. |
| 14 | `Duration` | `ScriptSpec.Duration` | ✓ | Same wire shape. |
| 15 | `SegmentWords` | `ScriptSpec.SegmentWords` | ✓ | Same wire shape. |
| 16 | `SegmentTopics` | `ScriptSpec.SegmentTopics` | ✓ | Same wire shape. |
| 17 | `SaveToDB` | `OutputSpec.SaveToDB` | ✓ | Same wire shape. |
| 18 | `ForceRefresh` | `SourceSpec.ForceRefresh` AND `ScriptSpec.ForceRefresh` | ⚠ | v2 has it in BOTH places (source-level vs script-level duplicate). |
| 19 | `GenerateSceneImages` | `OutputSpec.GenerateSceneImages` (deprecated) | ⚠ | v2 preserves the field as a no-op for backward compat; voiceover/images are now produced by separate downstream sibling jobs (`voiceover.generate` + `images.generate`) per Fase 2 Spina Dorsale (Jul 2026). See §4.3.7. |
| 20 | `GenerateVoiceover` | `OutputSpec.GenerateVoiceover` (deprecated) | ⚠ | Same as #19. |
| 21 | `GenerateDocument` | `OutputSpec.GenerateDocument` | ✓ | Same wire shape (NOTE: legacy was `bool`; v2 is also `bool`). |
| 22 | `GenerateDoc` | (merged into `GenerateDocument`) | 🔄 | Legacy had this as an alias of `GenerateDocument`; v2 collapsed to single field. |
| 23 | `ExtractEntities` | `OutputSpec.ExtractEntities` | ✓ | Same wire shape. |
| 24 | `GenerateMetadata` | `OutputSpec.GenerateMetadata` | ✓ | Same wire shape. |
| 25 | `DriveFolderID` | `OutputSpec.DriveFolderID` | ✓ | Same wire shape. |
| 26 | `EnableSceneImages` | (merged into `GenerateSceneImages`) | 🔄 | Legacy alias of `GenerateSceneImages`; v2 collapsed. |
| 27 | `SentencesPerImage` | `ScriptSpec.SentencesPerImage` | ✓ | Same wire shape. |
| 28 | `MinQualityScore float64` | `SourceSpec.MinQualityScore *float64` | ⚠ | v2 uses a pointer (nil = no filter) for clearer semantics; legacy used 0-sentinel. |
| 29 | `StyleInstructions` | (split into `SourceSpec.Guidelines` + `ScriptSpec.Style` + `ScriptSpec.Guidelines`) | ⚠ | v2 splits the legacy `StyleInstructions` + `Guidelines` into 3 distinct fields with phase-specific semantics — see §4.3.1. |
| 30 | `Guidelines` | (split into `SourceSpec.Guidelines` + `ScriptSpec.Guidelines`) | ⚠ | See §4.3.1. |
| 31 | `CustomPrompt` | (no direct mapping) | ✗ | See §4.1.3 — deliberate v2 security/contract strengthening: callers cannot inject arbitrary prompts. v2 uses `ScriptSpec.PromptVersion` (a typed version string selecting a canonical prompt template). |
| 32 | `SystemPrompt` | (no direct mapping) | ✗ | Same as #31. |
| 33 | `VoiceoverGroup` | `OutputSpec.VoiceoverGroup` | ✓ | Same wire shape. |
| 34 | `VoiceoverFolderID` | `OutputSpec.VoiceoverFolderID` | ✓ | Same wire shape. |
| 35 | `TranscriptPolicy` | `SourceSpec.TranscriptPolicy` AND `ScriptSpec.TranscriptPolicy` | ⚠ | v2 has it in BOTH places (source-level vs script-level duplicate). |
| 36 | `PromptVersion` | `ScriptSpec.PromptVersion` | ⚠ | v2 has 3 prompt-version fields: `PromptVersion` (legacy 1:1) + `EditorPromptVersion` + `QAPromptVersion` (NEW granularity for editor + QA prompt selection). |

### 3.2 `LegacyGenerateWithImagesRequest` (22 fields)

All 22 fields have direct v2 mappings (the with-images struct is a strict
subset of from-clips; only the legacy-specific fields are NOT-MAPPED).
See the from-clips table (§3.1) for shared fields. Differences:

| # | Legacy field (with-images) | V2 mapping | Status | Notes |
|---|----------------------------|-----------|--------|-------|
| 17 | `GenerateDocument *bool` (pointer) | `OutputSpec.GenerateDocument bool` | ⚠ | Pointer-vs-bool nullability change. v2 dropped the pointer; the `omitempty` JSON tag handles "absent" vs "false" equivalently at the wire level. |

---

## §4 Semantic gaps (verdict)

### 4.1 Fields NOT captured by v2 (`✗`)

#### 4.1.1 `Clips []LegacyClipInput{ID, Title, URL}` (from-clips only)

**Legacy semantics:** the client could pre-specify clip metadata
(`Title`, `URL`) alongside the `ClipID`. The server would trust the
client's metadata as-is.

**V2 contract:** the client provides `SourceSpec.ClipIDs []string` (IDs
only). The canonical clip state is RESOLVED at lookup time via
`ClipEvidence.ClipNames` (title) and `ClipEvidence.DriveLinks` (URL).
The client cannot pre-specify metadata; the server is the canonical
authority.

**Is this a semantic gap?** No — it's a deliberate contract strengthening
that prevents clients from "lying" about clip metadata. v2's contract is
strictly more correct: the server's view is always authoritative.

#### 4.1.2 `IntroClips []string` (from-clips only)

**Legacy semantics:** a free-form field that accepted either clip IDs
or URLs as strings (the type was just `[]string`).

**V2 contract:** only `SourceSpec.IntroClipIDs []string` exists (typed as
clip IDs). The free-form `IntroClips` was likely a v1 leftover that
never had a clean semantic — v2 collapsed it.

**Is this a semantic gap?** No. Any caller relying on `IntroClips` (URLs
or otherwise) had no canonical contract; v2's `IntroClipIDs` is the
clean replacement.

#### 4.1.3 `CustomPrompt string` (from-clips only)

**Legacy semantics:** the client could inject an arbitrary custom
prompt that would override the engine's canonical prompt.

**V2 contract:** no free-form prompt injection. v2 has only
`ScriptSpec.PromptVersion` (a typed version string selecting a
canonical prompt template).

**Is this a semantic gap?** No — this is a deliberate SECURITY and
contract strengthening. Arbitrary prompt injection is a prompt-injection
attack vector; v2 closes it by forcing callers to select from canonical
prompt templates. This is a feature, not a regression.

#### 4.1.4 `SystemPrompt string` (from-clips only)

**Legacy semantics:** the client could override the system prompt.

**V2 contract:** same as `CustomPrompt` — no free-form system prompt
injection.

**Is this a semantic gap?** No — same as 4.1.3, deliberate security
strengthening.

### 4.2 Fields collapsed to dedup (`🔄`)

#### 4.2.1 `GenerateDoc bool` (from-clips only)

**Legacy semantics:** alias of `GenerateDocument` (likely a typo or
backward-compat shim).

**V2 contract:** only `OutputSpec.GenerateDocument` exists.

**Is this a semantic gap?** No — collapsing two aliases into one is a
strict improvement.

#### 4.2.2 `EnableSceneImages bool` (from-clips only)

**Legacy semantics:** alias of `GenerateSceneImages`.

**V2 contract:** only `OutputSpec.GenerateSceneImages` exists.

**Is this a semantic gap?** No — same as 4.2.1.

### 4.3 Semantic SHIFTS (`⚠` MAPPED but semantics changed)

#### 4.3.1 Style/Instructions/Guidelines split (3 legacy fields → 4 v2 fields)

**Legacy semantics:** `Style` + `StyleInstructions` + `Guidelines` were
3 separate fields with ambiguous scope (any of them could be the
"editorial style guide").

**V2 contract:** the 3 legacy fields are split into 4 v2 fields with
EXPLICIT phase semantics:
- `GenerationItemV2.Style` — item-level editorial style
- `SourceSpec.Guidelines` — source-level editorial (text source only)
- `ScriptSpec.Style` — script-level editorial
- `ScriptSpec.Guidelines` — script-level editorial guidelines

**Is this a semantic gap?** No — v2's split is strictly more precise.
Any caller that was using a specific field by name will find its v2
equivalent.

#### 4.3.2 SourceText + Topic contemporaneous (the user's explicit question)

**Legacy semantics:** both `Topic` AND `SourceText` could be set on the
same request (used for "use this source text as the grounding, with
this topic as the editorial framing").

**V2 contract:** BOTH can be set in `SourceSpec` when `Type=text`:
```go
case SourceText:
    if item.Source.Topic == "" && item.Source.SourceText == "" {
        return &PlanInvalidError{...}  // at least ONE required
    }
```

**Is this a semantic gap?** No — **YES, v2 captures it.** v2 explicitly
supports the contemporaneous use case (validator requires AT LEAST ONE
but allows both) and extends it (the either-or case is also supported).
The user's specific question — "SourceText + Topic contemporanei" — is
answered: **YES, v2 supports it.**

#### 4.3.3 ForceRefresh duplicated in 2 places

**Legacy semantics:** one boolean field.

**V2 contract:** `SourceSpec.ForceRefresh` (when source needs to be
re-resolved) AND `ScriptSpec.ForceRefresh` (when the script generation
needs to re-run with cached inputs). Two distinct phases.

**Is this a semantic gap?** No — v2's split is more precise. Any caller
that was using `ForceRefresh=true` on the legacy request can set it on
BOTH v2 fields and preserve the original "force the whole pipeline"
semantics.

#### 4.3.4 TranscriptPolicy duplicated in 2 places

**Legacy semantics:** one string field.

**V2 contract:** `SourceSpec.TranscriptPolicy` (transcript selection
for clip sources) AND `ScriptSpec.TranscriptPolicy` (transcript
selection for the engine's prompt construction). Two distinct phases.

**Is this a semantic gap?** No — same as 4.3.3.

#### 4.3.5 MinQualityScore pointer semantics

**Legacy semantics:** `float64` with 0 as the "no filter" sentinel.

**V2 contract:** `*float64` with `nil` as the "no filter" sentinel.
The wire shape is identical (omitempty pointer fields are skipped on
marshal).

**Is this a semantic gap?** No — pointer semantics is the canonical
"explicit absent" idiom in Go. The wire shape is unchanged.

#### 4.3.6 NumClips duplicated in 2 places

**Legacy semantics:** one int field.

**V2 contract:** `SourceSpec.NumClips` (when type=clips) AND
`SourceResolutionContext.NumClips` (resolution-time duplicate).

**Is this a semantic gap?** No — same as 4.3.3.

#### 4.3.7 GenerateVoiceover + GenerateSceneImages DEPRECATED (no-op)

**Legacy semantics:** inline postprocessor flags — the engine would
generate voiceover/scene-images as part of the script.generate run.

**V2 contract:** the flags are preserved as no-op fields for backward
wire compat; voiceover/scene-images are now produced by SEPARATE
downstream sibling jobs (`voiceover.generate` + `images.generate`)
dispatched via `ManifestV2.Items []DownstreamRequest` (the
`internal/domain/script/downstream.go` surface).

**Is this a semantic gap?** No — this is a deliberate architecture
improvement (Fase 2 Spina Dorsale, July 2026). Setting the flag in v2
is a no-op; callers must emit a `DownstreamRequest{Voiceover}` or
`DownstreamRequest{Images}` to actually generate the assets.

### 4.4 V2-only fields (NEW capabilities, no legacy equivalent)

These v2 fields have no legacy equivalent; they are NEW capabilities
that v2 added:

- `GenerationItemV2.ID` — caller-assigned per-item correlation ID
- `SourceSpec.Type` — discriminated `SourceType` enum (text/clips/catalog/search/curate) — legacy had no type field, it was implied by the endpoint
- `SourceSpec.Query` — query string for catalog/search sources (NEW)
- `SourceSpec.MaxClips` — max clip count for catalog/search sources (NEW)
- `SourceSpec.MinCoverage` — minimum coverage threshold (NEW)
- `SourceSpec.MinTranscriptWords` — minimum transcript word count (NEW)
- `SourceSpec.OrderingStrategy` — clip ordering strategy (NEW)
- `SourceSpec.Search`, `AllowTextOnly`, `SourceFilter`, `MediaTypeFilter` — curation-source controls (NEW)
- `ScriptSpec.MinWords` — minimum word count (NEW)
- `ScriptSpec.ImagesPerScene` — image count per scene (NEW)
- `ScriptSpec.EditorPromptVersion` + `QAPromptVersion` — granular prompt versioning (NEW)
- `ScriptSpec.UseMemory` — memory/cache toggle (NEW)
- `OutputSpec.GenerateTimeline` — timeline generation flag (NEW)
- `OutputSpec.MaxChars` — max character count (NEW)
- `OutputSpec.OutputFmt` — output format (NEW)
- `OutputSpec.Languages` — multi-language output (NEW)
- `GenerationEnvelopeV2.Version` — schema version
- `GenerationEnvelopeV2.Preset` — endpoint preset selector
- `GenerationEnvelopeV2.CorrelationID` — tracing ID

These are all NEW capabilities that v2 added; they are not gaps in the
sense of "legacy had this, v2 doesn't".

---

## §5 Verdict

**The 410-Gone contract preserves all the legacy semantics that
mattered in practice.**

The 4 fields that have no v2 mapping (Clips metadata pre-specification,
IntroClips free-form, CustomPrompt, SystemPrompt) are EITHER:

1. **v1-era conveniences that v2 properly retired** (Clips metadata:
   the server is now the canonical authority; IntroClips free-form:
   had no clean semantic).

2. **Deliberate security/contract strengthening** (CustomPrompt +
   SystemPrompt: free-form prompt injection is a security
   anti-pattern; v2 forces callers to select from canonical prompt
   templates via `PromptVersion`).

3. **v1-era aliases that v2 collapsed** (GenerateDoc →
   GenerateDocument; EnableSceneImages → GenerateSceneImages).

The 3 fields that v2 split into 2 (ForceRefresh, TranscriptPolicy,
NumClips) and the 3 fields that v2 reorganized (Style, StyleInstructions,
Guidelines → 4 v2 fields with phase-specific semantics) are STRICT
improvements on the v1 contract.

**The user's specific question — "SourceText + Topic contemporanei" —
is answered: YES, v2 supports it.** The v2 validator explicitly allows
both fields to be set in `SourceSpec` when `Type=text`; at least one
is required, but both is allowed.

**No live caller is broken by the 410 retirement.** The 410 handler
emits the canonical `LegacyDeprecationPayload` body pointing to
`POST /api/script/generate` (the v2 endpoint). Any caller that wants to
migrate can use the v2 wire-shape per the field mapping in §3. The
4 NOT-MAPPED fields have no production callers (per Fase 0 Discovery
which confirmed zero callers of the legacy structs post-410-flip);
the deprecated inline postprocessor flags are forward-pointer to the
`DownstreamRequest` surface.

---

## §6 Cross-references

- `internal/domain/script/generation_envelope.go` — `GenerationEnvelopeV2` + `GenerationItemV2` canonical owner
- `internal/domain/script/source_spec.go` — `SourceSpec` + `SourceResolutionContext` + `ResolvedSource` canonical owner
- `internal/domain/script/output_spec.go` — `ScriptSpec` + `OutputSpec` canonical owner
- `internal/domain/script/downstream.go` — `DownstreamRequest` (the v2 surface for voiceover/image sibling jobs that replaces the deprecated inline postprocessor flags)
- `internal/api/script/handler_legacy_deprecation.go` — canonical 410 contract owner (post-Commit-2)
- `internal/api/script/handler_legacy_from_clips.go` — 410 handler for `/api/script/generate-from-clips` (post-Commit-2 stripped of dead types)
- `internal/api/script/handler_legacy_with_images.go` — 410 handler for `/api/script/generate-with-images` (post-Commit-2 stripped of dead types)
- `internal/api/script/handler_legacy_deprecation_test.go` — 8-test contract surface (counter increments + 410 body payload + route registration + headers + metric names)
- `internal/api/script/handler_flow.go` — `RegisterRoutes` (line 154 delegates to `RegisterLegacyDeprecationRoutes`)
- `architecture/reports/legacy-route-usage-2026-06-28.md` — pre-Commit-2 inventory (4 routes, removal dates, metrics decision)
- `architecture/waves/wave_p1_high.yaml` — wave-tracker entry (per the post-PR `AGENTS.md Git-Lesson-3` lockstep convention)

---

*End of audit. **Status:** AUDIT-ONLY. **Reviewer requested:** script-capability owner for §3 mapping table review; voiceover capability owner for §4.3.7 DownstreamRequest cross-check.*

*Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>*
