package outbox

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// parseAndValidateRequest parses the v1 envelope payload and performs
// the strict v1 envelope validation. Each missing/mismatched field is
// TERMINAL — retrying won't bring the field into existence. Validation
// failures return typed terminal errors (outboxevents.NewTerminalError)
// so the pool's IsTerminal classifier dead-letters them immediately
// rather than burning max_attempts in a repair loop.
//
// This function is the SOLE canonical entry point for envelope
// parsing in the outbox indexing path. The Handle method in
// indexing_handle.go delegates here; future readers (a
// future v2 envelope, a different outbox event type) should
// route through this function to inherit the same fail-closed
// terminal-classification contract.
//
// Pure code-motion: the function body was extracted verbatim
// from the original Handle method's first 7 validation blocks
// (parse + schema_version + event_id + asset_id + source_version +
// idempotency_key + operation). The terminal classification is
// preserved EXACTLY — the same typed outboxevents.NewTerminalError
// wrap, the same Warn-level log lines, the same zap fields.
func parseAndValidateRequest(payloadJSON string, evt outboxevents.Event, log *zap.Logger) (*indexRequestV1, error) {
	if log == nil {
		log = zap.NewNop()
	}

	var p indexRequestV1
	if jerr := json.Unmarshal([]byte(payloadJSON), &p); jerr != nil {
		log.Warn("asset.index.requested payload parse failed (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.Int("attempt", evt.AttemptCount),
			zap.Error(jerr),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested payload parse: %w", jerr),
		)
	}

	// index_revision is the canonical supersede fingerprint (godlike/06:
	// content_sha256 vs index_revision). Legacy events that only carry the
	// source_version alias fall back to it so the supersede gate keeps
	// comparing the SAME semantic (indexable-snapshot revision), never the
	// byte hash.
	if p.IndexRevision == "" {
		p.IndexRevision = p.SourceVersion
	}

	// Strict v1 envelope validation. Each missing/mismatched field is
	// TERMINAL — retrying won't bring the field into existence.
	if p.SchemaVersion != IndexRequestSchemaVersion {
		log.Warn("asset.index.requested schema_version mismatch (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
			zap.String("got_version", p.SchemaVersion),
			zap.String("want_version", IndexRequestSchemaVersion),
		)
		return nil, outboxevents.NewTerminalError(fmt.Errorf(
			"asset.index.requested: schema_version mismatch (terminal — got %q, want %q)",
			p.SchemaVersion, IndexRequestSchemaVersion,
		))
	}
	if p.EventID == "" {
		log.Warn("asset.index.requested: missing event_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: event_id is required (terminal)"),
		)
	}
	if p.AssetID == "" {
		log.Warn("asset.index.requested: empty asset_id (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: empty asset_id (terminal — retry cannot conjure an id)"),
		)
	}
	if p.IndexRevision == "" {
		// Empty index_revision (and its legacy source_version alias) is
		// the canonical supersede ambiguity signal — we cannot verify the
		// event is current, so retrying won't fix it. Producers MUST send
		// the indexable-snapshot revision. Terminal so producers upgrade.
		log.Warn("asset.index.requested: missing index_revision (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: index_revision (or legacy source_version) is required for the supersede gate (terminal — retry cannot conjure a fingerprint)"),
		)
	}
	if p.IdempotencyKey == "" {
		log.Warn("asset.index.requested: missing idempotency_key (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("aggregate_id", evt.AggregateID),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: idempotency_key is required (terminal)"),
		)
	}
	if p.Operation != "" && p.Operation != IndexRequestOperationUPSERT {
		log.Warn("asset.index.requested: unsupported operation (terminal)",
			zap.Int64("event_id", evt.ID),
			zap.String("operation", p.Operation),
		)
		return nil, outboxevents.NewTerminalError(
			fmt.Errorf("asset.index.requested: unsupported operation %q (terminal — only %q is supported in v1)", p.Operation, IndexRequestOperationUPSERT),
		)
	}

	return &p, nil
}
