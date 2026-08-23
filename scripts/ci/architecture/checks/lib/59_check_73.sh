#!/usr/bin/env bash
# 59_check_73.sh — sourced by scripts/ci-architectural-checks.sh (dispatcher).
#
# ── Check 73: hardcoded media-parameter gate (against 30fps residuals & encoder drift) ──
# Wave 5 (August 2026): the assembly-ready video contract is 1920×1080 @ 24/1.
# Every renderer must derive FPS, codec, pixel format, and encoder policy from
# the canonical AssemblyReadyVideoContract (or its Go-side SSOT —
# internal/platform/config/video.go::{CanonicalVideoProfile,EncoderPolicy}).
#
# This gate prevents four classes of regression:
#
#   (a) Hardcoded FPS: 30 in Go struct literals outside the authorized
#       SSOT. The canonical FPS is 24; 30 leaked historically from
#       provider presets (artlist, youtube, stock, shorts) and was
#       eliminated in Wave 5. Any re-introduction of `FPS: 30` (case-
#       insensitive) in a Go or YAML source file that is NOT one of the
#       authorized owners fails this gate.
#
#   (b) Hardcoded encoder names (libx264 / h264_nvenc) outside the
#       EncoderPolicy boundary. These must only appear in:
#       internal/platform/config/video.go (the canonical encoder policy)
#       and Rust encoder-boundary files (pipelinegen-muscles/src/). Any
#       other appearance is a SSOT regression.
#
#   (c) Hardcoded -pix_fmt / -vsync / -g flags in Go source — these
#       encode-media flags must be consumed from the contract struct, not
#       hardcoded as ffmpeg flag strings. Excluding Rust (which owns the
#       actual ffmpeg invocation).
#
#   (d) Hardcoded yuv420p pixel format in Go source outside the
#       authorized SSOT. The canonical pixel format is defined once in
#       the video contract.
#
# Authorized SSOT owners:
#   - internal/platform/config/video.go        (CanonicalVideoProfile + EncoderPolicy)
#   - pkg/defaults/video.go                    (canonical default values)
#   - internal/application/mediaexec/types.go  (media exec canonical profile)
#   - internal/capabilities/cliprender/        (clip render contract)
#   - rust/pipelinegen-muscles/src/            (Rust encoder boundary)
#   - internal/platform/config/video_canonical_test.go (canonical-profile tests)
#   - tests/fixtures/                          (negative-example fixtures)
#   - RenderingGen/                            (peer rendering subsystem — its own contract)
#   - Chronon3d/                               (peer rendering subsystem — its own contract)
#   - scripts/ci/architecture/checks/lib/      (this gate itself)
#
# Scope: internal/, cmd/, pkg/, config/, rust/  production paths.
# Negative examples: if a future negative-EXAMPLE fixture needs a 30fps
# literal, place it in tests/fixtures/ which is excluded.

echo "=== Check 73: hardcoded media-parameter gate (30fps / encoder / pix_fmt) ==="

# ── Pattern (a): FPS: 30 in Go struct literals ──────────────────────────
fps30_go=$(rg -n --type go \
    -e '[Ff][Pp][Ss]\s*:\s*30\b' \
    --glob '!**/*_test.go' \
    --glob '!internal/platform/config/video.go' \
    --glob '!internal/platform/config/video_canonical_test.go' \
    --glob '!pkg/defaults/video.go' \
    --glob '!internal/capabilities/cliprender/*' \
    --glob '!internal/application/mediaexec/types.go' \
    --glob '!tests/fixtures/*' \
    --glob '!RenderingGen/*' \
    --glob '!Chronon3d/*' \
    --glob '!scripts/ci/architecture/checks/lib/*' \
    internal/ cmd/ pkg/ config/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' || true)

# ── Pattern (b): fps: 30 in YAML configs outside golden fixtures ────────
fps30_yaml=$(rg -n --type yaml \
    -e 'fps:\s*30\b' \
    --glob '!RenderingGen/*' \
    --glob '!Chronon3d/*' \
    --glob '!tests/fixtures/*' \
    config/ 2>/dev/null || true)

# ── Pattern (c): libx264 / h264_nvenc hardcoded outside EncoderPolicy ──
encoder_hardcoded=$(rg -n --type go \
    -e '"(libx264|h264_nvenc)"' \
    --glob '!**/*_test.go' \
    --glob '!internal/platform/config/video.go' \
    --glob '!internal/platform/config/video_canonical_test.go' \
    --glob '!internal/application/mediaexec/types.go' \
    --glob '!tests/fixtures/*' \
    --glob '!RenderingGen/*' \
    --glob '!Chronon3d/*' \
    --glob '!scripts/ci/architecture/checks/lib/*' \
    internal/ cmd/ pkg/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' || true)

# ── Pattern (d): -pix_fmt / -vsync / -g flags in Go source ──────────────
ffmpeg_flags=$(rg -n --type go \
    -e '"-pix_fmt[[:space:]]' \
    -e '"-vsync[[:space:]]' \
    -e '"-g[[:space:]]' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/*' \
    --glob '!RenderingGen/*' \
    --glob '!Chronon3d/*' \
    --glob '!scripts/ci/architecture/checks/lib/*' \
    internal/ cmd/ pkg/ 2>/dev/null || true)

# ── Pattern (e): yuv420p hardcoded outside PixelFormat SSOT ─────────────
yuv420p_hardcoded=$(rg -n --type go \
    -e 'yuv420p' \
    --glob '!**/*_test.go' \
    --glob '!internal/platform/config/video.go' \
    --glob '!internal/platform/config/video_canonical_test.go' \
    --glob '!pkg/defaults/video.go' \
    --glob '!internal/application/mediaexec/types.go' \
    --glob '!internal/capabilities/cliprender/*' \
    --glob '!tests/fixtures/*' \
    --glob '!RenderingGen/*' \
    --glob '!Chronon3d/*' \
    --glob '!scripts/ci/architecture/checks/lib/*' \
    internal/ cmd/ pkg/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' || true)


# ── Gate: aggregate all hits and fail on any ────────────────────────────
violations=""
if [ -n "$fps30_go" ]; then
    violations="${violations}FPS_30_GO:\n${fps30_go}\n\n"
fi
if [ -n "$fps30_yaml" ]; then
    violations="${violations}FPS_30_YAML:\n${fps30_yaml}\n\n"
fi
if [ -n "$encoder_hardcoded" ]; then
    violations="${violations}ENCODER_HARDCODED:\n${encoder_hardcoded}\n\n"
fi
if [ -n "$ffmpeg_flags" ]; then
    violations="${violations}FFMPEG_FLAGS:\n${ffmpeg_flags}\n\n"
fi
if [ -n "$yuv420p_hardcoded" ]; then
    violations="${violations}YUV420P_HARDCODED:\n${yuv420p_hardcoded}\n\n"
fi

if [ -n "$violations" ]; then
    echo "FAIL: hardcoded media parameters detected outside authorized SSOT owners:"
    printf '%b\n' "$violations" | sed 's/^/  /'
    echo ""
    echo "Fix: route every FPS, codec, pixel-format, and encoder-policy value through:"
    echo "  - VideoContract (AssemblyReadyVideoContract)"
    echo "  - internal/platform/config/video.go::{CanonicalVideoProfile,EncoderPolicy}"
    echo "  - pkg/defaults/video.go::DefaultVideoConfig"
    echo ""
    echo "The canonical assembly-ready FPS is 24 (not 30). Authorized owners only:"
    echo "  - video.go (platform config SSOT)"
    echo "  - pkg/defaults/video.go (canonical defaults)"
    echo "  - cliprender/ (clip contract)"
    echo "  - mediaexec/types.go (media exec profile)"
    echo "  - pipelinegen-muscles/src/ (Rust encoder boundary)"
    echo "  - RenderingGen/ + Chronon3d/ (peer subsystems)"
    exit 1
fi

echo "Check 73: 0 hardcoded media parameters (FPS:30, encoder, pix_fmt, yuv420p)"