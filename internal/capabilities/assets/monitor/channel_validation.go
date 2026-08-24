// Package monitor — channel_validation.go: pre-check channel config
// validation (Keywords / SemanticKeywords JSON sanity).
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - validateChannelConfig: validates a channel's JSON-encoded fields.
//   - validateJSONArray: checks that a string is a valid JSON array.
//
// Blocco 3c (July 2026): pre-fix, malformed JSON was silently treated
// as keyword-less and the channel ran the full video scan without
// any filter — a small misconfiguration in one column could
// amplify processing 100× per cycle.
package assets

import (
	"encoding/json"
	"fmt"
	"strings"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
)

// validateChannelConfig checks that the channel's JSON-encoded fields
// (Keywords, SemanticKeywords) are syntactically valid before the
// channel enters the check queue. Malformed JSON is classified as
// a hard config error: the channel is skipped for this cycle, and
// MarkChecked(Success=false) fires so exponential backoff applies.
func (m *ChannelMonitor) validateChannelConfig(ch channels.Channel) error {
	if ch.Keywords != "" {
		if err := validateJSONArray(ch.Keywords, "Keywords"); err != nil {
			return err
		}
	}
	if ch.SemanticKeywords != "" {
		if err := validateJSONArray(ch.SemanticKeywords, "SemanticKeywords"); err != nil {
			return err
		}
	}
	return nil
}

// validateJSONArray checks that s is a valid JSON array. Returns nil
// for empty string or "[]" (valid empty array).
func validateJSONArray(s, fieldName string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return fmt.Errorf("channel config %s is not a valid JSON array: %w", fieldName, err)
	}
	return nil
}
