
# Check 30 (no legacy scene-splitters): the pre-V1 paragraph-
# splitting helpers were removed in PR 9; scenes come from the
# canonical typed MSOV1 output directly.
echo "=== Check 30: no legacy scene-splitters (PR 9) ==="
if rg -q 'splitScriptIntoSegments\|sceneCountFromPlan' internal/application/scripts/; then
    echo "FAIL: legacy scene-splitter helper(s) detected in internal/application/scripts/"
    echo "Fix: read scenes from engineResult.Output.SpecScene.Scenes"
    echo "     (validated by PR 6 ValidateAndEnrichSpecScene)."
    exit 1
fi
echo "OK: no splitScriptIntoSegments / sceneCountFromPlan"

# Check 31 (no artificial empty Scene.Text): the canonical MSOV1
# validator (PR 6) requires every scene to carry non-empty text;
# bypassing it via raw struct literals is a regression.
#
# PR 9 (June 2026, gate-tightening pass): the original blanket ban
# on `Text: ""` false-positived legitimate defensive defaults like
# `if sceneText == "" { sceneText = fallback }`. The tightened
# pattern restricts the match to scene-construction contexts:
# struct literals in the postprocessor layer (the path that
# constructs a *scriptpkg.SpecScene / SpecSceneOutput / SceneImage
# / SceneVoiceover literal). Defensive `sceneText == ""` guards
# remain free to use the empty string literal.
echo "=== Check 31: no synthetic empty scene Text (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e '(scene|SpecScene|SpecSceneOutput|SceneImage|SceneVoiceover|ClipScene)\{[^}]*Text:[[:space:]]*""' \
    --glob '!**/*_test.go' \
    internal/application/scripts/ 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: synthetic Text: \"\" detected in scene-construction context:"
    echo "$literals"
    echo "Fix: route scene construction through ValidateAndEnrichSpecScene"
    echo "     (rejects empty Text per PR 6 spec)."
    exit 1
fi
echo "OK: no synthetic Text:\"\" in scene-construction literals"

# Check 32 (no prose OutputFmt in canonical path): post-PR-6,
# the validator rejects OutputFmt=\"prose\" outright. Any
# production-code reference to the value is dead code or a
# regression; documentation comments in tests are excluded via
# the _test.go-with-comment pattern below.
echo "=== Check 32: no prose OutputFmt in canonical path (PR 9 / PR 6) ==="
literals=$(rg -n --type go \
    -e 'OutputFmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e 'output_fmt[[:space:]]*[:=][[:space:]]*"prose"' \
    -e "OutputFmt[[:space:]]*[:=][[:space:]]*'prose'" \
    -e "output_fmt[[:space:]]*[:=][[:space:]]*'prose'" \
    --glob '!**/*_test.go' \
    internal/application/scripts internal/domain/script 2>/dev/null \
    | awk -F: '{ rest = ""; for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i; if (rest ~ /^[[:space:]]*\/\//) next; print }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: OutputFmt \"prose\" detected in production path:"
    echo "$literals"
    exit 1
fi
echo "OK: no OutputFmt \"prose\" surface in canonical path"
# ── Check 33: forbid retention:created_at:mutable SQL tag in jobs (Wave 22 followup, June 2026) ────
# The retention sweeper (lifecycle.go::NewRetentionSweeper) deletes aged-out
# outbox events by `created_at`. The canonical contract: created_at is
# IMMUTABLE — once an event is inserted, the timestamp MUST NOT be
# updated. A mutable created_at leaks indefinitely-aged rows past the
# cutoff (and risks dropping active rows the moment a non-creation write
# touches the column).
#
# The TagWeaver sql-tag annotation `retention:created_at:mutable` flags
# any column-default or column-declaration that allows (or accepts) a
# created_at update. Production SQL MUST NOT carry this tag — the
# canonical schema is `DEFAULT CURRENT_TIMESTAMP` with no `ON UPDATE
# CURRENT_TIMESTAMP` (the MySQL idiom that the project's tag-based
# schema linter catches on review).
#
# Production-side companion to the canonical retention contract. The CI
# gate rg-greps for the annotation in the production jobs package and
# fails the gate when the operator has explicitly opted into fail-closed
# semantics via `eventTimestampIsImmutable=true`. When the env flag is
# unset / false, the gate logs an INFO message and exits 0 — the
# hit-count is observable in every CI run so the rollout can be audited
# before the env flag flips on. A complementary unit test
# (`TestRetentionSweeper_CreatedAtIsImmutable`) is the planned
# read-side enforcement; this gate is the operator-side enforcement.
#
# Allowlist: a future migration file that legitimately needs to mark the
# column as mutable (e.g. a feature toggle, an admin one-shot repair
# that backfills stale timestamps) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: retention-created-at-mutable` on the line
# preceding the sql-tag annotation. The awk pre-pass strips such hits
# from the failing-set via the same 25-line window tolerated by
# Check 5 / Check 8. Per AGENTS.md §8 zero-baseline rule, every new
# allowlist entry requires explicit owner + deadline; the marker is
# the call-site equivalent of an allowlist row.
#
# Pattern anchors:
#   retention:created_at:mutable    — exact literal sql-tag string
#
# Env-gated semantics (per user spec, June 2026):
#   eventTimestampIsImmutable=true   — fail-closed (exit 1 on hits)
#   eventTimestampIsImmutable=other  — pass-through, log INFO (rollout mode)
#   The gate ALWAYS runs the rg-grep regardless of the env flag so the
#   hit count is observable in CI output every run — the env gate only
#   controls whether hits translate into a hard CI failure.
echo "=== Check 33: forbid retention:created_at:mutable in sqlite/jobs ==="
all_hits=$(rg -n --type go \
    -e 'retention:created_at:mutable' \
    --glob '!**/*_test.go' \
    internal/infrastructure/database/sqlite/jobs/ 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*retention-created-at-mutable/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && $2 + 0 >= m + 1 && $2 + 0 <= m + 25) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
hits_count=${all_hits:+$(printf '%s' "$all_hits" | wc -l | awk '{print $1+0}')}
hits_count=${hits_count:-0}
literal_count=${literal_calls:+$(printf '%s' "$literal_calls" | wc -l | awk '{print $1+0}')}
literal_count=${literal_count:-0}
echo "INFO: retention:created_at:mutable scan in internal/infrastructure/database/sqlite/jobs/:"
echo "      total hits: ${hits_count}"
echo "      non-allowlisted hits: ${literal_count}"
if [ -n "$literal_calls" ]; then
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "FAIL: retention:created_at:mutable annotation in production jobs package (eventTimestampIsImmutable=true):"
        echo "$literal_calls"
        echo ""
        echo "Fix: remove the `retention:created_at:mutable` annotation from production SQL"
        echo "or column declarations — the created_at column is canonical IMMUTABLE"
        echo "(DEFAULT CURRENT_TIMESTAMP, no ON UPDATE clause). The retention sweeper"
        echo "depends on this; a mutable created_at leaks active rows past the cutoff"
        echo "and drops active rows the moment a non-creation write touches the column."
        echo ""
        echo "If the annotation is required for a feature flag or admin one-shot repair,"
        echo "prepend the magic marker on the preceding line:"
        echo "    // ARCH-ALLOWLIST: retention-created-at-mutable"
        echo "    // ... ctx -- retention:created_at:mutable"
        exit 1
    else
        echo "INFO: eventTimestampIsImmutable!=true — non-allowlisted hits present but permitted (transitional pass-through):"
        echo "$literal_calls"
        echo ""
        echo "Operator action: when the retention-immutability contract stabilises,"
        echo "flip eventTimestampIsImmutable=true in CI to fail-closed."
    fi
else
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "OK: eventTimestampIsImmutable=true, 0 retention:created_at:mutable hits in production jobs package"
    else
        echo "OK: 0 retention:created_at:mutable hits in production jobs package (eventTimestampIsImmutable not set; gate is informational)"
    fi
fi
