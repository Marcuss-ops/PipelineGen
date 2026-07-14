# Agent Execution Playbook

This playbook defines how autonomous agents interact with the PipelineGen codebase.

## Preparation

- Read AGENTS.md and the canonical architecture docs before making changes.
- Gather context from the codebase using file pickers, code searchers, and direct reads.
- Identify the composition root and the relevant ports before introducing new dependencies.

## Scope

Agents operate only within the scope of the user's request. They do not add unrequested features.

## Forbidden additions

- Do not add action plans, closure journals, evidence dumps, archived snapshots, or duplicate source-of-truth documents to the working tree.
- Do not commit credentials, tokens, cookies, or private keys.

## Testing

- Run targeted tests for the area being changed.
- Run `make verify-main` before pushing.
- Use race detection for concurrency-sensitive changes.

## Migration method

- Prefer expand, backfill, cutover, contract for compatibility changes.
- Update golden files and allowlists explicitly when contracts change.

## Final verification

- Confirm `make verify-main` passes.
- Review the git diff for unintended changes.
- Confirm the remote contains the intended commit.

## Documentation

- Keep docs current.
- Git history is the archive.
