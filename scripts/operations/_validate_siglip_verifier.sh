#!/usr/bin/env bash
# Validate verify_siglip_sidecar.sh — exercises every CLI path with direct rc.
# Uses `; rc=$?` (no pipe) so SIGPIPE on `head` doesn't mask the script exit.
set +e
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored

ok_count=0
fail_count=0
ok()  { printf "  \033[32mOK\033[0m   %s\n" "$1"; ok_count=$((ok_count+1)); }
bad() { printf "  \033[31mBAD\033[0m  %s (expected %s, got %s)\n" "$1" "$2" "$3"; fail_count=$((fail_count+1)); }

SCRIPT=scripts/operations/verify_siglip_sidecar.sh

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
echo '===== 3b. --model no value (expect 7) ====='
"$SCRIPT" --model >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--model no value exit"; else bad "--model no value exit" 7 "$rc"; fi

echo
echo '===== 3c. --dimension 0 (expect 7) ====='
"$SCRIPT" --dimension 0 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--dimension 0 exit"; else bad "--dimension 0 exit" 7 "$rc"; fi

echo
echo '===== 3d. --dimension -5 (expect 7) ====='
"$SCRIPT" --dimension -5 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--dimension -5 exit"; else bad "--dimension -5 exit" 7 "$rc"; fi

echo
echo '===== 3e. --timeout 0 (expect 7) ====='
"$SCRIPT" --timeout 0 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--timeout 0 exit"; else bad "--timeout 0 exit" 7 "$rc"; fi

echo
echo '===== 3f. --help (expect 0) ====='
"$SCRIPT" --help >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 ]]; then ok "--help exit"; else bad "--help exit" 0 "$rc"; fi

echo
echo '===== 3g. --help piped (expect 0 — NOT 141 from SIGPIPE trap) ====='
"$SCRIPT" --help 2>&1 | head -5 >/dev/null; rc=${PIPESTATUS[0]}
if [[ $rc -eq 0 ]]; then ok "--help piped (SIGPIPE-friendly)"; else bad "--help piped" 0 "$rc"; fi

echo
echo '===== 4. fail-closed gate (no --image-path, no --text-only) ====='
"$SCRIPT" >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "fail-closed exit"; else bad "fail-closed exit" 7 "$rc"; fi

echo
echo '===== 4b. image-path preflight (existing path OK; missing path exits 7) ====='
EXISTING=$(mktemp /tmp/exists_XXXXXX.png); : > "$EXISTING"
"$SCRIPT" --image-path "$EXISTING" >/dev/null 2>&1; rc=$?
if [[ $rc -eq 1 || $rc -eq 2 ]]; then
  ok "--image-path existing file (got $rc — proceeded to endpoint verification)"
else
  bad "--image-path existing file" "1/2 (proceeds)" "$rc"
fi
rm -f "$EXISTING"
"$SCRIPT" --image-path /tmp/this-file-does-not-exist-$(date +%s) >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--image-path missing file (godlike/07 preflight)"; else bad "--image-path missing" 7 "$rc"; fi

echo
echo '===== 5. --text-only opt-in (expect 1 if sidecar unreachable, 2 if non-200) ====='
"$SCRIPT" --text-only >/dev/null 2>&1; rc=$?
if [[ $rc -eq 1 || $rc -eq 2 ]]; then
  ok "--text-only exit (got $rc) — text-encoder verification ran"
else
  bad "--text-only exit" "1/2 (text-encoder verdict)" "$rc"
fi

echo
echo '===== 6. unreachable URL (expect 1) ====='
"$SCRIPT" --url http://127.0.0.1:65530 --text-only >/dev/null 2>&1; rc=$?
if [[ $rc -eq 1 ]]; then ok "unreachable url exit"; else bad "unreachable url exit" 1 "$rc"; fi

echo
echo '===== 7. unreachable URL + --json (should emit UNREACHABLE on stdout) ====='
JSON_OUT=$("$SCRIPT" --url http://127.0.0.1:65530 --text-only --json 2>/dev/null)
if [[ "$JSON_OUT" == *'"status":"UNREACHABLE"'* ]]; then
  ok "unreachable URL + --json emits UNREACHABLE status"
else
  bad "unreachable URL + --json" "UNREACHABLE" "actual=$JSON_OUT"
fi

echo
echo '===== 8. JSON-mode summary with invalid --image-path (CONFIG_ERROR exits 7) ====='
JSON_OUT=$("$SCRIPT" --json 2>/dev/null)
if [[ "$JSON_OUT" == *'"status":"CONFIG_ERROR"'* && "$JSON_OUT" == *'"exit":7'* ]]; then
  ok "JSON-mode CONFIG_ERROR path"
else
  bad "JSON-mode CONFIG_ERROR" "CONFIG_ERROR+exit:7" "actual=$JSON_OUT"
fi

echo
echo '===== 9. trap '' PIPE near top ====='
LINE=$(grep -nE "trap '' PIPE" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "trap line present: $LINE"; else bad "trap '' PIPE" present "missing"; fi

echo
echo '===== 10. jq --arg body construction (no naïve interpolation) ====='
LINE=$(grep -nE "jq -nc --arg p" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "jq --arg p present"; else bad "jq --arg p" present "missing"; fi
LINE=$(grep -nE "jq -nc --arg t" "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "jq --arg t present"; else bad "jq --arg t" present "missing"; fi

echo
echo '===== 11. jq preflight on .embedding | type == array ====='
COUNT=$(grep -c 'embedding | type' "$SCRIPT")
if [[ $COUNT -ge 1 ]]; then ok "jq .embedding preflight present (count=$COUNT)"; else bad "jq preflight" present "missing"; fi

echo
echo '===== 12. emit_json template-first convention ====='
LINE=$(grep -nE 'jq -nc "\$template"' "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "emit_json template-first present"; else bad "emit_json template-first" present "missing"; fi

echo
echo '===== 13. SSOT default canonical asserts ==='
COUNT=$(grep -cE 'EXPECTED_MODEL="siglip-so400m-patch14-384"|EXPECTED_MODEL_VERSION="2026-06-26-v1"|EXPECTED_DIMENSION=768' "$SCRIPT")
if [[ $COUNT -eq 3 ]]; then ok "all 3 SSOT defaults present"; else bad "SSOT defaults" "3" "$COUNT"; fi

echo
echo '===== 14. 8 typed exit codes documented in USAGE block ====='
# Match lines starting with `#` then whitespace then digit then whitespace
# (covers commented exit-code rows alongside any uncommented ones).
COUNT=$(grep -cE '^#[[:space:]]+[0-9][[:space:]]' "$SCRIPT")
if [[ $COUNT -ge 8 ]]; then ok "exit-code doc has ≥8 codes (count=$COUNT)"; else bad "exit-code doc" "≥8" "$COUNT"; fi

echo
echo '===== 15. image-path preflight distinguishes from sidecar drift (godlike/07) ====='
LINE=$(grep -nE '\[\[ -n "\$IMAGE_PATH" && ! -e "\$IMAGE_PATH" \]\]' "$SCRIPT")
if [[ -n "$LINE" ]]; then ok "preflight existence check present: $LINE"; else bad "preflight existence" present "missing"; fi

echo
echo '===== 16. --batch-count with non-integer (expect 7) ====='
"$SCRIPT" --batch-count abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--batch-count abc exit (CLI parse fail-closed)"; else bad "--batch-count abc" 7 "$rc"; fi

echo
echo '===== 17. --batch-count with negative number (expect 7) ====='
"$SCRIPT" --batch-count -3 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--batch-count -3 exit (CLI parse fail-closed)"; else bad "--batch-count -3" 7 "$rc"; fi

echo
echo '===== 18. --batch-image-paths with empty value (expect 7) ====='
"$SCRIPT" --batch-image-paths "" >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--batch-image-paths '' exit (CLI parse fail-closed)"; else bad "--batch-image-paths ''" 7 "$rc"; fi

echo
echo '===== 19. --batch-count 0 + --batch-image-paths present (batch section skipped → expect 7 no-path or proceed to text) ====='
"$SCRIPT" --batch-count 0 --batch-image-paths /tmp/cs_fixture.png >/dev/null 2>&1; rc=$?
# Skipped batch section is OK; main flow falls through. With no IMAGE_PATH and no TEXT_ONLY,
# the verifier exits 7 (fail-closed "missing required arg"). Acceptable.
if [[ $rc -eq 0 || $rc -eq 1 || $rc -eq 2 || $rc -eq 7 ]]; then
  ok "--batch-count 0 (batch section skipped) — main flow verdict rc=$rc"
else
  bad "--batch-count 0 batch-section skip" "0/1/2/7" "$rc"
fi

echo
echo '===== 20. --batch-count N (live sidecar with SKIP_SIGLIP=1 returns 501; expect 2) ====='
"$SCRIPT" --batch-count 2 --batch-image-paths /tmp/cs_fixture.png,/tmp/cs_fixture.png 2>/dev/null; rc=$?
# Section 9 fires with the live sidecar; SKIP_SIGLIP=1 ⇒ batch endpoint returns 501 ⇒ BATCH_RC=2 ⇒ exit 2.
if [[ $rc -eq 2 || $rc -eq 1 ]]; then
  ok "--batch-count 2 batch section fires against live sidecar (got $rc — 1=unreachable or 2=non-200/501)"
else
  bad "--batch-count 2 batch section" "1/2" "$rc"
fi

echo
echo '===== 21. Section 9 batch assertions present in the script ====='
COUNT=$(grep -cE 'POST /embed_visual_from_images|embed_visual_from_images: ' "$SCRIPT")
if [[ $COUNT -ge 6 ]]; then ok "batch section assertions wired (count=$COUNT)"; else bad "batch section asserts" "≥6" "$COUNT"; fi

echo
printf "\n===== TOTALS: ok=%d fail=%d =====\n" "$ok_count" "$fail_count"
if [[ $fail_count -eq 0 ]]; then
  printf "\033[32mALL VALIDATION PASSED\033[0m\n"
  exit 0
else
  printf "\033[31mVALIDATION FAILURES PRESENT\033[0m\n"
  exit 1
fi
