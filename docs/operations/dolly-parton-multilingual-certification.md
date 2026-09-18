# Dolly Parton multilingual 5x5 certification

## Goal

One `POST /api/script/generate` run consumes the five canonical Dolly Parton
clips and produces one certified Chronon/RenderingGen MP4 per language:

- source: `en`
- targets: `it`, `es`, `de`, `fr`
- localized target matrix: **5 clips × 4 languages = 20 renders**
- full delivery count: **25 renders** when the five English source-clip artifacts are included alongside the 20 localized variants
- subtitles: translated timed tracks, burned into the MP4
- audio: original clip audio for the subtitle-only lane
- delivery: Drive plus the PostgreSQL media SSOT

The canonical clip fixture is
`tests/fixtures/clip_batch_vLRjqTIiMjc/payload.json`. Its five source clip IDs
are derived by `tests/e2e/dolly_parton_multilingual_runtime_test.go`; do not copy
IDs into a second fixture.

## Operator command

From `refactored/`:

```bash
scripts/certify_dolly_multilingual.sh
```

This always runs the hermetic clip, request, subtitle-language, and localized
fan-out contracts. It does not contact external services.

For the live lane, start PipelineGen with its PostgreSQL/media/Drive wiring and
a live RenderingGen/Chronon GPU worker, then run:

```bash
PIPELINEGEN_DOLLY_PARTON_LIVE=1 \
VELOX_ADMIN_TOKEN='...' \
DOLLY_PARTON_LANGUAGES=it,es,de,fr \
VELOX_API_BASE_URL=http://127.0.0.1:8000 \
scripts/certify_dolly_multilingual.sh
```

The live test is deliberately opt-in. A skipped live test is not a PASS for
Drive or GPU delivery.

## Acceptance matrix

| Criterion | Repository gate |
|---|---|
| Five canonical clips, deterministic IDs, duration and Drive routing | `TestDollyPartonClipBatch*` |
| Source language is English and targets are explicit | `TestDollyPartonMultilingualRequestContract` |
| Burned subtitles and GPU-required render | same request contract + cliprender contracts |
| Cue-by-cue translation preserves source timing | `internal/capabilities/assets/texttracks` CueTranslator tests |
| Missing target track never falls back to English | `TestLocalizedRenderEnqueuer_RejectsMissingRequestedSubtitleLanguage` |
| Normal SourceClips audio-NONE multilingual fan-out | `TestRunner_SourceClipsAudioNone_LocalizedRenderFanout` |
| Streaming starts downstream work before global generation completes | `TestRunner_LocalizedRenderFanout_RenderStartsBeforeNextSceneReady` |
| Complete render matrix cannot report success partially | `Runner.completeRun` expected/successful/failed gate |
| Canonical localized render contract and content-addressed reuse | localization plan/fingerprint/reuse tests, including replay cache-hit certification |
| Certified output facts, Chronon backend, Drive links and media SSOT | `internal/capabilities/cliprender/certify_test.go` for the local facts gate; live Dolly runtime test for real GPU/Drive/PostgreSQL evidence |

## Live evidence required for final PASS

The live result must show, for every requested target language and clip:

- requested language equals produced artifact language;
- target subtitle track is READY and timed;
- `asset_id` is `cliprender_<sha256-prefix>`;
- non-empty SHA-256, positive duration and Drive link;
- Chronon GPU backend and certified render/encode metrics;
- no localized render failures;
- the result matrix is exactly 20 unique `(clip_id, language)` cells for the four targets;
- PostgreSQL media asset row and Drive publication exist.

The source-language EN artifacts are the existing five canonical clip assets. A
full 25-artifact release therefore consists of those five source assets plus the
20 localized target renders; EN is not repeated as a translation target.

## Local verification evidence

The complete repository-side gate is:

```bash
scripts/certify_dolly_multilingual.sh
go test ./internal/capabilities/localization/... \\
  ./internal/capabilities/cliprender/... \\
  ./internal/capabilities/scripts/... \\
  ./internal/app/wiring ./tests/e2e -count=1
```

Both commands must pass before any live run is attempted. The local suite
covers the exact render contract, certified output facts, subtitle timing,
multilingual fan-out, fail-closed matrix completion, and deterministic replay
cache hits.

A second identical invocation must reuse the same content-addressed render
fingerprints and must not create duplicate GPU renders or Drive objects. The
current live test validates the complete target matrix and per-language
artifact persistence/identity; cache-hit counts should be recorded from the
RenderingGen/localization cache metrics when the live stack is available.
