package job

import (
	"bytes"
	"encoding/json"
)

// ParentLink identifies the canonical job and observability run that spawned a
// child job. It is carried in the existing job payload so remote claimers can
// recover the relationship without depending on process-local memory.
type ParentLink struct {
	ParentJobID string `json:"parent_job_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
}

// ParentLinkFromPayload extracts the optional parent linkage from a JSON job
// payload. Malformed or non-object payloads are treated as unlinked.
func ParentLinkFromPayload(payload json.RawMessage) ParentLink {
	if len(bytes.TrimSpace(payload)) == 0 {
		return ParentLink{}
	}
	var link ParentLink
	if err := json.Unmarshal(payload, &link); err != nil {
		return ParentLink{}
	}
	return link
}

// InjectParentLink preserves an object payload while adding the canonical
// parent identifiers. Non-object payloads are returned unchanged.
func InjectParentLink(payload []byte, link ParentLink) []byte {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return payload
	}
	if link.ParentJobID != "" {
		if value, err := json.Marshal(link.ParentJobID); err == nil {
			object["parent_job_id"] = value
		}
	}
	if link.ParentRunID != "" {
		if value, err := json.Marshal(link.ParentRunID); err == nil {
			object["parent_run_id"] = value
		}
	}
	result, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return result
}
