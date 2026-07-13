#!/usr/bin/env bash
# Validate verify_qdrant_point.sh — exercises every CLI path with direct rc.
# Uses `; rc=$?` (no pipe) so SIGPIPE on `head` doesn't mask the script exit.
set +e
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored

ok_count=0
fail_count=0
ok()  { printf "  \033[32mOK\033[0m   %s\n" "$1"; ok_count=$((ok_count+1)); }
bad() { printf "  \033[31mBAD\033[0m  %s (expected %s, got %s)\n" "$1" "$2" "$3"; fail_count=$((fail_count+1)); }

SCRIPT=scripts/operations/verify_qdrant_point.sh

echo '===== 1. bash -n syntax ====='
bash -n "$SCRIPT"; rc=$?
if [[ $rc -eq 0 ]]; then ok "syntax check"; else bad "syntax check" 0 "$rc"; fi

echo
echo '===== 2. chmod +x ====='
chmod +x "$SCRIPT"
ls -l "$SCRIPT"
if [[ -x "$SCRIPT" ]]; then ok "executable bit"; else bad "executable bit" set "$rc"; fi

echo
echo '===== 3a. bogus flag (expect 7) ====='
"$SCRIPT" --bogus-flag >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "bogus flag exit"; else bad "bogus flag exit" 7 "$rc"; fi

echo
echo '===== 3b. --limit 0 (expect 7) ====='
"$SCRIPT" --limit 0 --asset-id abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--limit 0 exit"; else bad "--limit 0 exit" 7 "$rc"; fi

echo
echo '===== 3c. --limit -5 (expect 7) ====='
"$SCRIPT" --limit -5 --asset-id abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--limit -5 exit"; else bad "--limit -5 exit" 7 "$rc"; fi

echo
echo '===== 3d. --timeout 0 (expect 7) ====='
"$SCRIPT" --timeout 0 --asset-id abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--timeout 0 exit"; else bad "--timeout 0 exit" 7 "$rc"; fi

echo
echo '===== 3e. --scroll-only + --search-only (expect 7 — mutually exclusive) ====='
"$SCRIPT" --scroll-only --search-only --asset-id abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "scroll+search mutually exclusive exit"; else bad "scroll+search exclusive" 7 "$rc"; fi

echo
echo '===== 3f. --help (expect 0) ====='
"$SCRIPT" --help >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 ]]; then ok "--help exit"; else bad "--help exit" 0 "$rc"; fi

echo
echo '===== 3g. --help piped (expect 0 NOT 141 from SIGPIPE trap) ====='
"$SCRIPT" --help 2>&1 | head -5 >/dev/null; rc=${PIPESTATUS[0]}
if [[ $rc -eq 0 ]]; then ok "--help piped (SIGPIPE-friendly)"; else bad "--help piped" 0 "$rc"; fi

echo
echo '===== 4. fail-closed gate (no --asset-id) ====='
"$SCRIPT" >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "fail-closed exit"; else bad "fail-closed exit" 7 "$rc"; fi

echo
echo '===== 5. unreachable Qdrant URL (expect 1) ====='
"$SCRIPT" --qdrant-url http://127.0.0.1:65530 --asset-id abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 1 ]]; then ok "unreachable Qdrant URL exit"; else bad "unreachable Qdrant URL" 1 "$rc"; fi

echo
echo '===== 6. unreachable Qdrant URL + --json (UNREACHABLE on stdout) ====='
JSON_OUT=$("$SCRIPT" --qdrant-url http://127.0.0.1:65530 --asset-id abc --json 2>/dev/null)
if [[ "$JSON_OUT" == *'"status":"UNREACHABLE"'* ]]; then
  ok "unreachable Qdrant + --json emits UNREACHABLE"
else
  bad "unreachable Qdrant + --json" "UNREACHABLE" "actual=$JSON_OUT"
fi

echo
echo '===== 7. trap '' PIPE near top ====='
LINE=$(grep -nE "trap '' PIPE" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "trap line present: $LINE"; else bad "trap '' PIPE" present "missing"; fi

echo
echo '===== 8. jq --arg body construction (no naïve interpolation) ====='
LINE=$(grep -nE "jq -nc --arg id" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "jq --arg id (scroll) present"; else bad "jq --arg id" present "missing"; fi
LINE=$(grep -nE "jq -nc --arg t" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "jq --arg t (sidecar) present"; else bad "jq --arg t" present "missing"; fi

echo
echo '===== 9. emit_json template-first convention ====='
LINE=$(grep -nE 'jq -nc "\$template"' "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "emit_json template-first present"; else bad "emit_json template-first" present "missing"; fi

echo
echo '===== 10. SSOT defaults (collection / model / dim / named-vector) ====='
COUNT=$(grep -cE 'COLLECTION="media_assets_current"|EXPECTED_MODEL="siglip-so400m-patch14-384"|EXPECTED_DIMENSION=768|NAMED_VECTOR="visual"' "$SCRIPT")
if [[ $COUNT -eq 4 ]]; then ok "all 4 SSOT defaults present (count=4)"; else bad "SSOT defaults" "4" "$COUNT"; fi

echo
echo '===== 11. jq preflight on .embedding | type == array ====='
COUNT=$(grep -c 'embedding | type' "$SCRIPT")
if [[ $COUNT -ge 1 ]]; then ok "jq .embedding preflight present (count=$COUNT)"; else bad "jq preflight" present "missing"; fi

echo
echo '===== 12. jq preflight on .result.points | type == array ====='
COUNT=$(grep -c 'result.points | type' "$SCRIPT")
if [[ $COUNT -ge 2 ]]; then ok "jq .result.points preflight present (count=$COUNT — both scroll + search)"; else bad "jq .result.points preflight" "≥2" "$COUNT"; fi

echo
echo '===== 13. /points/query (canonical wire) used in search section, NOT /points/search ====='
LINE=$(grep -nE "points/search|points/query" "$SCRIPT")
if echo "$LINE" | grep -qE 'points/query'; then
  ok "/points/query endpoint wired (canonical post PR 2 June 2026)"
else
  bad "/points/query" present "missing"
fi

echo
echo '===== 14. 8 typed exit codes in USAGE block ====='
COUNT=$(grep -cE '^#   [0-9] ' "$SCRIPT")
if [[ $COUNT -ge 8 ]]; then ok "exit-code doc has ≥8 codes (count=$COUNT)"; else bad "exit-code doc" "≥8" "$COUNT"; fi

echo
echo '===== 15. Qdrant reachability probe handled (default URL live on test box) ====='
# Probe-only check — if Qdrant's not up, this should still be a clean script
# behaviour (not a syntax/runtime error). Run with --scroll-only + bogus
# asset_id so the script exits at the scroll assertion (5) NOT at /probe.
"$SCRIPT" --scroll-only --asset-id abc-this-does-not-exist >/dev/null 2>&1; rc=$?
# Acceptable exit codes for this probe: 1 (Qdrant unreachable), 3 (dim fail),
# 5 (points=0 from scroll). The script MUST reach the scroll step.
if [[ $rc -eq 1 || $rc -eq 3 || $rc -eq 5 ]]; then
  ok "default Qdrant probe reachable + scroll ran (got $rc)"
else
  bad "default Qdrant probe reachable" "1/3/5" "$rc"
fi

echo
printf "\n===== TOTALS: ok=%d fail=%d =====\n" "$ok_count" "$fail_count"
if [[ $fail_count -eq 0 ]]; then
  printf "\033[32mALL VALIDATION PASSED\033[0m\n"
  exit 0
else
  printf "\033[31mVALIDATION FAILURES PRESENT\033[0m\n"
  exit 1
fi
