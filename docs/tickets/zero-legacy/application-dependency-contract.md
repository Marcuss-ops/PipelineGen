# PG-020 — Application dependency contract

**Branch:** `codex/pg-020-appdeps-types`

## Checklist A–Z
A. Sync main. B. Create only the ticket branch. C. Inventory generic dependency slots and cleanup callbacks. D. Read actual consumers. E. Define a named route registrar. F. Define the minimum health contract. G. Review readiness ownership. H. Update constructors. I. Update server wiring. J. Update fakes and tests. K. Remove the compatibility cleanup field. L. Use Lifecycle as the only teardown owner. M. Add compile-time assertions. N. Test startup. O. Test shutdown. P. Format. Q. Run focused app and API tests. R. Run all tests. S. Run vet and build. T. Run architecture checks. U. Repeat the inventory. V. Review the diff. W. Update canonical docs only. X. Rebase and retest. Y. Commit and push. Z. Verify history, status and checks.

## Done
- [ ] Dependencies are named and typed.
- [ ] No compensating runtime casts.
- [ ] Lifecycle owns teardown.
- [ ] Tests and gates pass.
