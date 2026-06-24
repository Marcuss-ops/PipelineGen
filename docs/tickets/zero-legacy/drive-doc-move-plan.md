# PG-021 — Drive document move as a one-time operation

**Branch:** `codex/pg-021-drive-doc-migration`

## Checklist A–Z
A. Sync main. B. Create only the ticket branch. C. Find the startup document move. D. Read existing Drive ports. E. Extract an application use case. F. Define source and target inputs. G. Make dry-run the default. H. Require an explicit execute flag. I. Support pagination. J. Make moves idempotent. K. Report found, moved, skipped and failed. L. Add an admin command. M. Remove the startup call. N. Test partial failure. O. Test a second run with zero work. P. Format. Q. Run admin, application and Drive tests. R. Run all tests. S. Run vet and build. T. Run architecture checks. U. Confirm startup has no historical data move. V. Review diff. W. Update the runbook. X. Rebase and retest. Y. Commit and push. Z. Verify remote checks.

## Done
- [ ] Startup performs no historical move.
- [ ] Dry-run and execute are tested.
- [ ] Report is deterministic.
- [ ] Re-run is idempotent.
