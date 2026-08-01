#!/usr/bin/env bash
# scripts/ci/get-fingerprint_test.sh
#
# Regression test for the pre-push verification fingerprint. It verifies that
# changing an untracked file's content changes the fingerprint, not just the
# untracked path set.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/get-fingerprint.XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$WORK_DIR/scripts/ci" "$WORK_DIR/scripts/hooks" "$WORK_DIR/bin"
cp "$SCRIPT_DIR/get-fingerprint.sh" "$WORK_DIR/scripts/ci/get-fingerprint.sh"
chmod +x "$WORK_DIR/scripts/ci/get-fingerprint.sh"

cat > "$WORK_DIR/bin/go" <<'FAKE_GO'
#!/usr/bin/env bash
printf 'go version go1.25.0 linux/amd64\n'
FAKE_GO
chmod +x "$WORK_DIR/bin/go"
cat > "$WORK_DIR/bin/node" <<'FAKE_NODE'
#!/usr/bin/env bash
printf 'v22.0.0\n'
FAKE_NODE
chmod +x "$WORK_DIR/bin/node"

cd "$WORK_DIR"
export PATH="$WORK_DIR/bin:$PATH"
git init -q -b main
git config user.email fingerprint-test@example.invalid
git config user.name fingerprint-test
printf 'package fixture\n' > fixture.go
git add fixture.go
git commit -q -m baseline
git update-ref refs/remotes/origin/main HEAD

printf 'first content\n' > untracked.txt
first=$(bash scripts/ci/get-fingerprint.sh)
printf 'second content\n' > untracked.txt
second=$(bash scripts/ci/get-fingerprint.sh)

if [[ "$first" == "$second" ]]; then
    echo "❌ get-fingerprint test: untracked content did not invalidate fingerprint" >&2
    exit 1
fi

echo "✅ get-fingerprint regression test passed"
