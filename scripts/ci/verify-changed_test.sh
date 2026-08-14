#!/usr/bin/env bash
# scripts/ci/verify-changed_test.sh
#
# Regression test for verify-changed.sh. Uses a temporary Git repository so
# staged, unstaged, and untracked fixtures cannot mutate the developer tree.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SCRIPT_DIR/verify-changed.sh"
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/verify-changed.XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT

fail() {
    echo "❌ verify-changed test: $*" >&2
    exit 1
}

mkdir -p "$WORK_DIR/scripts/ci" "$WORK_DIR/bin"
cp "$SCRIPT" "$WORK_DIR/scripts/ci/verify-changed.sh"
chmod +x "$WORK_DIR/scripts/ci/verify-changed.sh"
cat > "$WORK_DIR/Makefile" <<'MAKEFILE'
GO ?= go
.PHONY: verify-changed
verify-changed:
	@GO="$(GO)" bash scripts/ci/verify-changed.sh
MAKEFILE

cd "$WORK_DIR"
git init -q -b main
git config user.email verify-changed-test@example.invalid
git config user.name verify-changed-test

mkdir -p trackedpkg stagedpkg
printf 'package trackedpkg\n' > trackedpkg/tracked.go
printf 'package stagedpkg\n' > stagedpkg/staged.go
git add Makefile scripts/ci/verify-changed.sh trackedpkg/tracked.go stagedpkg/staged.go
git commit -q -m baseline
git update-ref refs/remotes/origin/main HEAD

# Keep one tracked Go file unstaged, add one staged Go file, and add one
# untracked Go file. The final path intentionally contains spaces to verify
# NUL-safe collection and array iteration.
printf 'package trackedpkg\n\n// unstaged\n' > trackedpkg/tracked.go
mkdir -p "untracked package"
printf 'package untrackedpackage\n' > "untracked package/untracked.go"
printf 'package stagedpkg\n\n// staged\n' > stagedpkg/staged.go
git add stagedpkg/staged.go

FAKE_GO="$WORK_DIR/bin/configured-go"
LOG="$WORK_DIR/go-args.log"
cat > "$FAKE_GO" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" >> "$VERIFY_CHANGED_GO_LOG"
FAKE_GO
chmod +x "$FAKE_GO"

VERIFY_CHANGED_GO_LOG="$LOG" make verify-changed GO="$FAKE_GO" > "$WORK_DIR/output.log"

mapfile -d '' -t actual_args < "$LOG"
expected_args=(test ./stagedpkg test ./trackedpkg test './untracked package')
if [[ "${#actual_args[@]}" -ne "${#expected_args[@]}" ]]; then
    echo "Expected ${#expected_args[@]} configured GO arguments, got ${#actual_args[@]}." >&2
    exit 1
fi
for i in "${!expected_args[@]}"; do
    if [[ "${actual_args[$i]}" != "${expected_args[$i]}" ]]; then
        echo "Configured GO argument $i mismatch:" >&2
        printf '  expected: <%s>\n  actual:   <%s>\n' "${expected_args[$i]}" "${actual_args[$i]}" >&2
        exit 1
    fi
done

if ! grep -q 'configured-go' "$WORK_DIR/output.log"; then
    fail "output did not identify the configured GO binary"
fi

# --- Phase 2: a changed shell script gets a cheap bash -n syntax check ---
git reset -q --hard HEAD
rm -rf "untracked package"
mkdir -p scripts/tools
printf '#!/usr/bin/env bash\necho probe-ok\n' > scripts/tools/probe.sh

: > "$LOG"
VERIFY_CHANGED_GO_LOG="$LOG" bash "$SCRIPT" > "$WORK_DIR/output2.log" 2>&1
if ! grep -q 'bash -n syntax check' "$WORK_DIR/output2.log"; then
    fail "phase 2: shell-script change did not run the syntax check"
fi
if [ -s "$LOG" ]; then
    fail "phase 2: unexpected GO invocations for a shell-only change"
fi

# --- Phase 3: a shell syntax error fails closed ---
printf '#!/usr/bin/env bash\nif then fi\n' > scripts/tools/probe.sh
: > "$LOG"
if VERIFY_CHANGED_GO_LOG="$LOG" bash "$SCRIPT" > "$WORK_DIR/output3.log" 2>&1; then
    fail "phase 3: verify-changed should fail closed on a shell syntax error"
fi

# --- Phase 4: make/** changes trigger only native Node probe + architecture ---
git reset -q --hard HEAD
rm -rf scripts/tools
mkdir -p make bin
cat > "$WORK_DIR/bin/make" <<'FAKE_MAKE'
#!/usr/bin/env bash
printf '%s\0' "$@" >> "${VERIFY_CHANGED_MAKE_LOG:?}"
FAKE_MAKE
chmod +x "$WORK_DIR/bin/make"
printf '# placeholder\n' > make/verify.mk

: > "$LOG"
MAKE_LOG="$WORK_DIR/make-args.log"
: > "$MAKE_LOG"
PATH="$WORK_DIR/bin:$PATH" GO="$FAKE_GO" VERIFY_CHANGED_GO_LOG="$LOG" VERIFY_CHANGED_MAKE_LOG="$MAKE_LOG" \
    bash "$SCRIPT" > "$WORK_DIR/output4.log" 2>&1
if ! grep -q 'native Node probe + architecture gates' "$WORK_DIR/output4.log"; then
    fail "phase 4: make/** change did not trigger the core-toolchain branch"
fi
if [ ! -s "$MAKE_LOG" ]; then
    fail "phase 4: make was not invoked for a make/** change"
fi
mapfile -d '' -t make_args < "$MAKE_LOG"
for want in verify-node-native verify-architecture; do
    found=0
    for arg in "${make_args[@]}"; do
        [ "$arg" = "$want" ] && found=1
    done
    if [ "$found" -ne 1 ]; then
        fail "phase 4: make was not invoked with $want"
    fi
done
for banned in verify-node verify-node-tests; do
    for arg in "${make_args[@]}"; do
        if [ "$arg" = "$banned" ]; then
            fail "phase 4: make was invoked with $banned; the agent loop must not run the full Node suite"
        fi
    done
done
if [ -s "$LOG" ]; then
    fail "phase 4: unexpected GO invocations for a make-only change"
fi

# --- Phase 5: a changed Python file gets a cheap py_compile syntax check ---
git reset -q --hard HEAD
rm -rf make bin scripts/tools
mkdir -p scripts/tools
printf 'def hello():\n    return "hi"\n' > scripts/tools/probe.py

: > "$LOG"
VERIFY_CHANGED_GO_LOG="$LOG" bash "$SCRIPT" > "$WORK_DIR/output5.log" 2>&1
if ! grep -q 'py_compile syntax check' "$WORK_DIR/output5.log"; then
    fail "phase 5: python change did not run the syntax check"
fi
if [ -s "$LOG" ]; then
    fail "phase 5: unexpected GO invocations for a python-only change"
fi

# --- Phase 6: a Python syntax error fails closed ---
printf 'def broken(:\n    pass\n' > scripts/tools/probe.py
: > "$LOG"
if VERIFY_CHANGED_GO_LOG="$LOG" bash "$SCRIPT" > "$WORK_DIR/output6.log" 2>&1; then
    fail "phase 6: verify-changed should fail closed on a python syntax error"
fi

# --- Phase 7: core changes must NOT swallow changed Go package tests ---
git reset -q --hard HEAD
rm -rf scripts/tools make bin
mkdir -p make bin trackedpkg2
cat > "$FAKE_GO" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\0' "$@" >> "$VERIFY_CHANGED_GO_LOG"
FAKE_GO
chmod +x "$FAKE_GO"
cat > "$WORK_DIR/bin/make" <<'FAKE_MAKE'
#!/usr/bin/env bash
printf '%s\0' "$@" >> "${VERIFY_CHANGED_MAKE_LOG:?}"
FAKE_MAKE
chmod +x "$WORK_DIR/bin/make"
printf '# placeholder\n' > make/verify.mk
printf 'package trackedpkg2\n' > trackedpkg2/tracked2.go

: > "$LOG"
MAKE_LOG="$WORK_DIR/make-args2.log"
: > "$MAKE_LOG"
PATH="$WORK_DIR/bin:$PATH" GO="$FAKE_GO" VERIFY_CHANGED_GO_LOG="$LOG" VERIFY_CHANGED_MAKE_LOG="$MAKE_LOG" \
    bash "$SCRIPT" > "$WORK_DIR/output7.log" 2>&1
if ! grep -q 'native Node probe + architecture gates' "$WORK_DIR/output7.log"; then
    fail "phase 7: core change did not trigger the core-toolchain branch"
fi
mapfile -d '' -t make_args < "$MAKE_LOG"
if [[ " ${make_args[*]} " != *" verify-node-native "* ]] || [[ " ${make_args[*]} " != *" verify-architecture "* ]]; then
    fail "phase 7: make was not invoked with the core scope"
fi
mapfile -d '' -t go_args < "$LOG"
if [[ " ${go_args[*]} " != *" ./trackedpkg2 "* ]]; then
    fail "phase 7: changed Go package tests were swallowed by the core branch"
fi

echo "✅ verify-changed regression test passed"
