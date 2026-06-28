// api_errors.go — marker file for the Qdrant API error surface.
//
// PR3 mechanical split (June 2026): the user-stated file spec
// referenced "QdrantError/ErrorResponse" as the canonical wire-level
// error types. The Qdrant package's existing canonical names are
// `APIError` (the wire-level error DTO) and the lower-case sentinel
// errors (`ErrSchemaIncompatible`, `ErrCollectionNotFound`,
// `ErrAliasNotFound`, `ErrVectorDimensionMismatch`, `ErrNaNOrInf`,
// `ErrEmptyVector`, `ErrChannelUnavailable`, `ErrAliasSwitchNotReady`,
// `ErrSparseRequired`) — both shapes live in errors.go (the PR1
// typer-error rebuild). PR3 does NOT move them: errors.go was already
// split out from the qdrant package's types.go PR1-era home and the
// PR1 docstrings (lines 1-40 of errors.go) wire every consumer
// (jobs.Service retry-decision, dr.RestoreService, verifier.go) to
// the typed-error surface there.
//
// Why this file remains a marker, not a content home:
//
//   - Moving APIError + the 9 sentinel errors out of errors.go into
//     api_errors.go would not honour "zero behaviour change, muovi
//     solo tipi" — it would re-merge two files that PR1 deliberately
//     separated (errors.go was extracted specifically so the wire-
//     level error DTO could grow without re-shuffling types.go).
//   - SchemaDiff + DimensionDiff + DistanceDiff (the schema-error
//     artefacts consumed by ErrSchemaIncompatible from errors.go)
//     live in collection_types.go alongside CollectionInfo (the
//     schema-validation consumer family), which is the closer fit
//     than "with the error types".
//   - The wire-level helpers (classifyRetryability, readAPIBody,
//     newAPIErrorFromResponse, IsRetryable, isPermanent, maxAPIBodyBytes
//     constant) tied to APIError stay in errors.go because they are
//     only ever called by parseErrorWith in client_errors.go (post-
//     PR2 relocation).
//
// Future shape that DOES belong in this file:
//
//   - If a future canonical "ErrorResponse" goes from being an inline
//     struct (inside client_dr.go's snapshot decoders, etc.) to a
//     wire-level DTO that lives in the qdrant package surface, it
//     goes here. The naming "ErrorResponse" was inferred by the
//     user-stated spec; today every Qdrant error response is decoded
//     inline as part of its parent operation's RPC envelope, so no
//     shared ErrorResponse shape exists.
//
// This file parallels clips_statistics.go's marker in PR1 and
// client_snapshots.go's marker in PR2 — a godoc-only file pointing
// at where the actual types live.
package qdrant
