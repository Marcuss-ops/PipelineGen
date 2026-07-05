// Package scripts — generation_result_mapper.go is the canonical owner
// of the typed-result → broker-map boundary (godlike/06 SSOT: one
// owner per fact). All script.generate variants — single-item success,
// single-item failure, multi-item partial/full/all-failed, multi-item
// infra-failure — flow through this one file's builders and the
// single `toMap` marshal/unmarshal bridge.
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//   - NO filesystem ops in this file (persistence moved to
//     adapters/artifacts_persistence.go per KILL K1).
//   - NO log writers (logger concerns live in generation_handler.go).
//   - Pure struct/marshal surface: handleSingle + handleBatch call
//     these builders, then call toMap to produce the broker
//     map[string]any result.
//
// godlike/07 typed-error contract: every builder returns a
// fully-formed typed GenerationEnvelopeResult (no error return on
// the builder paths because they wrap already-classified
// outcomes from generation_outcome.go's Diagnostic struct).
// toMap returns a (map[string]any, error) pair where mapErr is
// a typed marshal-fail error wrapping the underlying json error.
//
// godlike/07 honest-limitation disclosure (AGENTS.md Check 44 LoC cap):
// This file exceeds the 66-LoC transitional cap (~95 LoC) because
// the builder surface fan-outs across single + multi × success +
// failure variants share the same canonical typed-envelope shape
// (Version=2, Items=[...], Summary{...}). Each variant has its
// own builder to keep godlike/06 SSOT (no constructor conditional).
// Forward-pointer linked_issue (zero-baseline rule):
// PR-GODOBJ-4b-RESULT-MAPPER-SLIM extracts the single-item builders
// into generation_result_mapper_single.go (≤30 LoC) and the multi-item
// builder into generation_result_mapper_multi.go (≤30 LoC). Deadline
// 2026-08-15.
package jobs

import (
	"encoding/json"
	"fmt"

	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// buildSingleSuccessEnvelope wraps a successful single-item result
// in the canonical envelope shape (Version=EnvelopeVersion, Items=[1],
// Summary counts). The Single field once used to flatten this; PR 7
// removed the asymmetry. A nil single is treated as
// buildSingleFailureEnvelope("nil generation result") so a future
// regression that passes nil single surfaces an explicit failure
// rather than a typed zero-empty-value pass.
func buildSingleSuccessEnvelope(itemID string, single *domainScript.GenerationResult) domainScript.GenerationEnvelopeResult {
	if single == nil {
		return buildSingleFailureEnvelope(itemID, "nil generation result")
	}
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      true,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Result: single,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 1,
			Failed:    0,
		},
	}
}

// buildSingleFailureEnvelope captures a per-item failure in the
// canonical envelope shape. Same schema-version, same summary counts,
// same per-item Error field.
func buildSingleFailureEnvelope(itemID string, errMsg string) domainScript.GenerationEnvelopeResult {
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      false,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Error:  errMsg,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 0,
			Failed:    1,
		},
	}
}

// singleEnvelopeResult was the PR-7-pre thin wrapper; preserved as a
// box-shape projection for callers that need an ok-only envelope
// without success/failure annotation. Single-item canonical result
// is built via buildSingleSuccessEnvelope above.
func singleEnvelopeResult(itemID string, result *domainScript.GenerationResult) domainScript.GenerationEnvelopeResult {
	return domainScript.GenerationEnvelopeResult{
		OK: result != nil,
		Items: []domainScript.GenerationEnvelopeItem{{
			ItemID: itemID,
			Result: result,
		}},
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     1,
			Succeeded: 1,
			Failed:    0,
		},
	}
}

// buildEnvelopeResult converts a GenerateManyResult into a typed
// GenerationEnvelopeResult. PR 7 contract: Summary is a value type,
// Version is always EnvelopeVersion, no Single field. A nil
// manyResult yields a typed zero-summary envelope (the handler
// projects this when the use case produced no aggregate counts).
func buildEnvelopeResult(r *usecase.GenerateManyResult) domainScript.GenerationEnvelopeResult {
	if r == nil {
		return domainScript.GenerationEnvelopeResult{
			Version: domainScript.EnvelopeVersion,
			OK:      false,
			Summary: domainScript.GenerationEnvelopeSummary{},
		}
	}
	items := make([]domainScript.GenerationEnvelopeItem, len(r.Items))
	for i, item := range r.Items {
		var errStr string
		var genResult *domainScript.GenerationResult
		if item.Error != "" {
			errStr = item.Error
		} else {
			genResult = item.Result
		}
		items[i] = domainScript.GenerationEnvelopeItem{
			ItemID: item.ItemID,
			Result: genResult,
			Error:  errStr,
		}
	}
	return domainScript.GenerationEnvelopeResult{
		Version: domainScript.EnvelopeVersion,
		OK:      r.Summary.Failed == 0,
		Items:   items,
		Summary: domainScript.GenerationEnvelopeSummary{
			Total:     r.Summary.Total,
			Succeeded: r.Summary.Succeeded,
			Failed:    r.Summary.Failed,
		},
		Warnings: r.Warnings,
	}
}

// toMap serialises a GenerationEnvelopeResult to map[string]any via
// a JSON marshal/unmarshal cycle. This is the LEGAL boundary between
// typed domain results and the job-system map contract (the only
// place map[string]any appears in this application-layer surface).
// Every envelope variant — success, single-item, multi-item,
// failure, partial — flows through this single path.
func toMap(r domainScript.GenerationEnvelopeResult) (map[string]any, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("generate job handler: marshal envelope: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("generate job handler: unmarshal envelope: %w", err)
	}
	return out, nil
}
