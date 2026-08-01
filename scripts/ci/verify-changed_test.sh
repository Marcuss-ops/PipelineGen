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

echo "✅ verify-changed regression test passed"
