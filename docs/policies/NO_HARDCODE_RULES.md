# No Hardcode Rules

Rule set for this repo:

1. Do not hardcode Drive IDs, folder paths, or root mappings in runtime code.
2. Do not hardcode JSON catalog paths as a data source when SQL is the source of truth.
3. Do not hardcode topic-specific fallback content in production code.
4. Use config, SQL, or generated discovery data for runtime decisions.
5. Test fixtures may live in `testdata/`, but runtime code must not depend on fixture literals.
6. If a constant is unavoidable, document why it is stable and where it is configured.
