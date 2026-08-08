#!/usr/bin/env bash
set -euo pipefail
cd /home/pierone/Pyt/Pipeline Gen

echo "=== POST-BAD-COMMIT-STATE ==="
git log -3 --oneline
git status --short
echo

echo "=== STEP-1: RESET-COMMIT-AND-INDEX (mixed) ==="
git reset HEAD~1 --mixed
echo "---POST-RESET-LOG---"
git log -3 --oneline
echo "---POST-RESET-STATUS---"
git status --short

echo "=== STEP-2: STAGE-EXACTLY-MY-4-CLIPS-FILES ==="
git add internal/api/assets/clips/handler.go \
        internal/api/assets/clips/ops.go \
        internal/api/assets/clips/clip_ops_handlers.go \
        internal/api/assets/clips/ingest.go
echo "---STAGED-DIFF-STAT---"
git diff --cached --stat
echo "---STAGED-STATUS-FILTER---"
git status --short | grep '^.[AM]' || echo "NONE STAGED"

echo "=== STEP-3: RE-COMMIT-WITH-SPLIT2-MSG ==="
git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' \
    commit -F /tmp/split2-commit-msg.txt

echo "=== STEP-4: VERIFY-COMMIT ==="
git log -1 --format='%h %s%n%(trailers)'

echo "=== STEP-5: STASH-FOREIGN-WIP ==="
git stash push --include-untracked -m 'split2-pre-push-foreign-wip' -- \
    internal/app/build_process_qdrant.go \
    internal/app/build_media_processor.go \
    internal/app/build_qdrant_runtime.go \
    internal/infrastructure/database/sqlite/scripts/repository_adapter.go \
    internal/application/scripts/ports/repository.go \
    scripts/ci-architectural-checks.sh \
    scripts/ci/architecture/checks/43_db_chain_outside_infra.sh \
    internal/infrastructure/database/sqlite/scripts/repository_adapter.go \
    internal/api/assets/clips/bulk_upload_transport.go \
    internal/application/clips/bulk_upload_scanner.go 2>&1 || echo "stash partial"

echo "---POST-STASH-LIST---"
git stash list
echo "---POST-STASH-STATUS---"
git status --short || true

echo "=== STEP-6: FETCH + REBASE + PUSH ==="
git fetch origin
echo "---PRE-REBASE-LOG---"
git log --oneline -3
git rebase origin/main || (echo "REBASE CONFLICT"; git rebase --abort; git status --short; exit 1)
echo "---POST-REBASE-LOG---"
git log --oneline -3
git push origin main
echo "---POST-PUSH-REMOTE-HEAD---"
git ls-remote origin main | head -1

echo "=== STEP-7: FINAL-VERIFY ==="
git status --short
git log -1 --format='%h %s%n%(trailers)'

echo "=== STEP-8: POP-STASH ==="
git stash pop 2>&1 || echo "stash pop had conflicts"
echo "---POST-POP-LIST---"
git stash list
echo "---POST-POP-STATUS---"
git status --short || true

echo "=== DONE ==="
