# Makefile - invocation entry only.
#
# Per AGENTS.md max_lines_per_file: 1000 plus the P2 Manutenibilita
# directive (July 2026), the canonical build chain is split into 7
# thematic includes under make/. The root holds ONLY include make/*.mk
# plus the default all: build target. Per-bucket targets, comments,
# and recipes live in their include.
#
# HONOUR-RULE (binding, July 2026) for git push: scripts/hooks/pre-push
# invokes make verify-main as the fail-closed pre-push gate. The
# verify-main target itself lives in make/verify.mk. A RED verify-main
# BLOCKS the push atomically. DO NOT bypass via git push --no-verify
# on NORMAL pushes (canonical exception: CI emergencies only, paired
# with a fixup! commit plus git rebase --autosquash once the red gate is
# fixed). The split-refactor preserves this contract byte-equivalent.

include make/build.mk
include make/test.mk
include make/verify.mk
include make/artlist.mk
include make/live.mk
include make/docker.mk
include make/operations.mk

all: build
