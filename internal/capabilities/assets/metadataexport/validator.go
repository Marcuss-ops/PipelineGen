// Package metadataexport — v1 envelope validation.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the validate
// logic was previously a method on MetadataExportHandler
// (the former outbox metadata export implementation).
// After split, validation lives in its own file because the handler
// needs to call it BEFORE any side-effecting port invocation
// (resolver or writer); having a separate file ensures future
// validation additions (e.g. new include sections, new format
// providers) land in one reviewable place rather than scattering
// through handler.go.
package assets

import (
	"fmt"
)

// Validate enforces the v1 envelope invariants:
//
//  1. schema_version MUST match the canonical literal. Mismatch is
//     ErrMetadataTerminal — producers upgrade instead of waiting for
//     retries to fix a missing field.
//
//  2. Either asset_ids (non-empty) OR job_id (non-empty) must scope
//     the export. Both-empty is terminal.
//
//  3. format MUST be in the {json, jsonl, csv} allowlist; empty or
//     unknown is terminal.
//
//  4. destination.provider MUST be in the {filesystem, drive} allowlist;
//     empty or unknown is terminal.
//
//  5. include sections MUST be in the strict allowlist; one bad section
//     rejects the whole envelope (no silent truncation).
//
// Nil receivers / empty requests are accepted as long as the explicit
// checks below pass — the handler's Handle() entry points rejects
// truly malformed payloads via json.Unmarshal first.
func Validate(req *MetadataExportRequest) error {
	if req == nil {
		return fmt.Errorf("%w: request is nil", ErrMetadataTerminal)
	}
	if req.SchemaVersion != metadataExportSchemaVersion {
		return fmt.Errorf("%w: schema_version mismatch (got %q, want %q)", ErrMetadataTerminal, req.SchemaVersion, metadataExportSchemaVersion)
	}
	if len(req.AssetIDs) == 0 && req.JobID == "" {
		return fmt.Errorf("%w: at least one of asset_ids[] or job_id is required", ErrMetadataTerminal)
	}
	switch req.Format {
	case FormatJSON, FormatJSONL, FormatCSV:
		// OK
	case "":
		// Empty format is terminal — don't accept "no format". Today's
		// handler refuses rather than guessing; producers upgrade.
		return fmt.Errorf("%w: format is required (json|jsonl|csv)", ErrMetadataTerminal)
	default:
		return fmt.Errorf("%w: format=%q is not in allowlist (json|jsonl|csv)", ErrMetadataTerminal, req.Format)
	}
	switch req.Destination.Provider {
	case DestinationFilesystem, DestinationDrive:
		// OK
	case "":
		return fmt.Errorf("%w: destination.provider is required (filesystem|drive)", ErrMetadataTerminal)
	default:
		return fmt.Errorf("%w: destination.provider=%q is not in allowlist (filesystem|drive)", ErrMetadataTerminal, req.Destination.Provider)
	}
	if bad := invalidSections(req.Include); len(bad) > 0 {
		return fmt.Errorf("%w: include contains non-allowlist values: %v (allowed: technical|provenance|timeline|entities|delivery)", ErrMetadataTerminal, bad)
	}
	return nil
}

// invalidSections returns the difference between the request's
// include list and the canonical allowlist. Returns nil when the
// request is fully allowlist-valid.
//
// Package-private — only Validate calls it. Future allowlist additions
// (new IncludeXxx constants + new case in the switch) land atomically
// so the divergence is impossible.
func invalidSections(req []string) []string {
allow:
	for _, s := range req {
		switch s {
		case IncludeTechnical, IncludeProvenance, IncludeTimeline, IncludeEntities, IncludeDelivery:
			continue allow
		}
		// First mismatch — collect the rest so the warning message
		// is exhaustive on the producer side.
		out := []string{s}
		for _, t := range req[1:] {
			if t == "" {
				continue
			}
			switch t {
			case IncludeTechnical, IncludeProvenance, IncludeTimeline, IncludeEntities, IncludeDelivery:
				continue
			}
			out = append(out, t)
		}
		return out
	}
	return nil
}
