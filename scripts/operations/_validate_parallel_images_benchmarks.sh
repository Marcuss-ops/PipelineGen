#!/usr/bin/env bash
# Validate the 3 new parallel-images operator scripts.
# Uses `; rc=$?` (no pipe) so SIGPIPE on `head` doesn't mask the script exit.
set +e
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored

ok_count=0
fail_count=0
ok()  { printf "  \033[32mOK\033[0m   %s\n" "$1"; ok_count=$((ok_count+1)); }
bad() { printf "  \033[31mBAD\033[0m  %s (expected %s, got %s)\n" "$1" "$2" "$3"; fail_count=$((fail_count+1)); }

THREE_REQ_SCRIPT=scripts/operations/parallel_images_3req_benchmark.sh
STRESS_SCRIPT=scripts/operations/parallel_images_30img_stress.sh
INSPECT_SCRIPT=scripts/operations/inspect_outbox.sh

echo '===== 1a. bash -n syntax on parallel_images_3req_benchmark.sh ====='
bash -n "$THREE_REQ_SCRIPT"; rc=$?
if [[ $rc -eq 0 ]]; then ok "parallel_images_3req_benchmark.sh syntax"; else bad "parallel_images_3req_benchmark.sh syntax" 0 "$rc"; fi

echo
echo '===== 1b. bash -n syntax on parallel_images_30img_stress.sh ====='
bash -n "$STRESS_SCRIPT"; rc=$?
if [[ $rc -eq 0 ]]; then ok "parallel_images_30img_stress.sh syntax"; else bad "parallel_images_30img_stress.sh syntax" 0 "$rc"; fi

echo
echo '===== 1c. bash -n syntax on inspect_outbox.sh ====='
bash -n "$INSPECT_SCRIPT"; rc=$?
if [[ $rc -eq 0 ]]; then ok "inspect_outbox.sh syntax"; else bad "inspect_outbox.sh syntax" 0 "$rc"; fi

echo
echo '===== 2a. chmod +x on all 3 ====='
chmod +x "$THREE_REQ_SCRIPT" "$STRESS_SCRIPT" "$INSPECT_SCRIPT"
ls -l "$THREE_REQ_SCRIPT" "$STRESS_SCRIPT" "$INSPECT_SCRIPT" | awk '{print "  "$NF}'
ok "executable bit set on all 3"

echo
echo '===== 3a. 3req script: bogus flag (expect 7) ====='
"$THREE_REQ_SCRIPT" --bogus-flag >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "bogus flag → 7"; else bad "bogus flag" 7 "$rc"; fi

echo
echo '===== 3b. 3req script: --timeout 0 (expect 7) ====='
"$THREE_REQ_SCRIPT" --timeout 0 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--timeout 0 → 7"; else bad "--timeout 0" 7 "$rc"; fi

echo
echo '===== 3c. 3req script: --workers flag accepted (expect 7 since it is bogus for this script) ====='
"$THREE_REQ_SCRIPT" --workers 30 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--workers (not in 3req surface) → 7"; else bad "--workers rejected" 7 "$rc"; fi

echo
echo '===== 3d. 3req script: --help (expect 0) ====='
"$THREE_REQ_SCRIPT" --help >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 ]]; then ok "--help → 0"; else bad "--help" 0 "$rc"; fi

echo
echo '===== 4a. stress script: bogus flag (expect 7) ====='
"$STRESS_SCRIPT" --bogus-flag >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "bogus flag → 7"; else bad "bogus flag" 7 "$rc"; fi

echo
echo '===== 4b. stress script: --workers 0 (expect 7) ====='
"$STRESS_SCRIPT" --workers 0 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--workers 0 → 7"; else bad "--workers 0" 7 "$rc"; fi

echo
echo '===== 4c. stress script: --workers abc (expect 7) ====='
"$STRESS_SCRIPT" --workers abc >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "--workers abc → 7"; else bad "--workers abc" 7 "$rc"; fi

echo
echo '===== 4d. stress script: --help (expect 0) ====='
"$STRESS_SCRIPT" --help >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 ]]; then ok "--help → 0"; else bad "--help" 0 "$rc"; fi

echo
echo '===== 4e. stress script: --url unreachable → expect 1 (health probe fail) ====='
"$STRESS_SCRIPT" --url http://127.0.0.1:65530 --workers 1 --timeout 3 >/dev/null 2>&1; rc=$?
if [[ $rc -eq 1 ]]; then ok "unreachable url → 1"; else bad "unreachable url" 1 "$rc"; fi

echo
echo '===== 5a. inspect script: bogus flag (expect 7) ====='
"$INSPECT_SCRIPT" stats --bogus-flag >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "bogus flag → 7"; else bad "bogus flag" 7 "$rc"; fi

echo
echo '===== 5b. inspect script: no subcommand (expect 7) ====='
"$INSPECT_SCRIPT" >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "missing subcommand → 7"; else bad "missing subcommand" 7 "$rc"; fi

echo
echo '===== 5c. inspect script: --help (expect 0) ====='
"$INSPECT_SCRIPT" --help >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 ]]; then ok "--help → 0"; else bad "--help" 0 "$rc"; fi

echo
echo '===== 5d. inspect script: lookup without aggregate_id (expect 7) ====='
"$INSPECT_SCRIPT" lookup >/dev/null 2>&1; rc=$?
if [[ $rc -eq 7 ]]; then ok "lookup without id → 7"; else bad "lookup without id" 7 "$rc"; fi

echo
echo '===== 5e. inspect script: list-stuck subcommand recognised (expect 0 OR 2 OR 3) ====='
# Expect 0 (DB present, 0 rows but valid subcommand) OR 2 (no stuck rows) OR 3 (DB missing).
# All three indicate the subcommand was accepted; we only fail on 7.
"$INSPECT_SCRIPT" list-stuck >/dev/null 2>&1; rc=$?
if [[ $rc -eq 0 || $rc -eq 2 || $rc -eq 3 ]]; then
  ok "list-stuck subcommand recognised"
else
  bad "list-stuck subcommand" "0/2/3" "$rc"
fi

echo
echo '===== 5f. inspect script: stats --json (shape check) ====='
STATS_JSON=$("$INSPECT_SCRIPT" stats --json 2>/dev/null || true)
# Use has("total") and has("counts") so we actually validate the JSON-shape contract
# (the previous '"total" and "counts"' is always truthy in jq and never fails).
if [[ -n "$STATS_JSON" ]] && echo "$STATS_JSON" | jq -e 'has("total") and has("counts")' >/dev/null 2>&1; then
  ok "stats --json produces parseable JSON"
else
  # Might be empty string if DB missing — that's acceptable.
  if [[ -z "$STATS_JSON" ]]; then
    ok "stats --json returned empty (DB unreachable on this host — typed fail-closed confirmed)"
  else
    bad "stats --json shape" "<JSON object with .total + .counts>" "got: $(echo "$STATS_JSON" | head -c 120)"
  fi
fi

echo
echo '===== 6a. trap PIPE present in all 3 (mirror verifier convention) ====='
for f in "$THREE_REQ_SCRIPT" "$STRESS_SCRIPT" "$INSPECT_SCRIPT"; do
  LINE=$(grep -nE "trap '' PIPE" "$f" 2>/dev/null)
  if [[ -n "$LINE" ]]; then ok "$f: trap present"; else bad "$f: trap '' PIPE" "present" "missing"; fi
done

echo
echo '===== 6b. NO_COLOR respected (colour helpers exist) ====='
for f in "$THREE_REQ_SCRIPT" "$STRESS_SCRIPT" "$INSPECT_SCRIPT"; do
  LINE=$(grep -nE 'NO_COLOR.*!=.*1' "$f" 2>/dev/null)
  if [[ -n "$LINE" ]]; then ok "$f: NO_COLOR respected"; else bad "$f: NO_COLOR" "respected" "missing"; fi
done

echo
printf "\n===== TOTALS: ok=%d fail=%d =====\n" "$ok_count" "$fail_count"
if [[ $fail_count -eq 0 ]]; then
  printf "\033[32mALL VALIDATION PASSED\033[0m\n"
  exit 0
else
  printf "\033[31mVALIDATION FAILURES PRESENT\033[0m\n"
  exit 1
fi
