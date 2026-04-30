package artlist

// File refactored into modules:
// - run_record.go: Database operations for run records (schema, CRUD)
// - run_management.go: Run lifecycle (StartRunTag, executeRunTag, jobToResponse)
// - run_helpers.go: Utility functions (normalization, dedup keys, clip skip logic)

// Dead code removed:
// - Duplicate scanRunRecord (lines 379-390 from original)
// - Duplicate lastProcessedAtForTerm (already in stats_service.go)
// - Unused code blocks
