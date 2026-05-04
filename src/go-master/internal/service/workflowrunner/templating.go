package workflowrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var placeholderRegex = regexp.MustCompile(`\{\{\s*([^}]+)\s*\}\}`)

// renderPayload replaces template placeholders in the payload
func renderPayload(payload map[string]interface{}, wf *Workflow, state *WorkflowState) (map[string]interface{}, error) {
	if len(payload) == 0 {
		return payload, nil
	}

	// Convert payload to JSON string for easy replacement
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	jsonStr := string(jsonBytes)

	// Replace all placeholders
	rendered := placeholderRegex.ReplaceAllStringFunc(jsonStr, func(match string) string {
		// Extract the expression inside {{ }}
		expr := strings.TrimSpace(match[2 : len(match)-2]) // remove {{ and }}

		value, err := evaluateExpression(expr, wf, state)
		if err != nil {
			// Return the original placeholder if evaluation fails
			return match
		}

		// Convert value to JSON representation
		switch v := value.(type) {
		case string:
			return strconv.Quote(v)
		case int, int64, float64, bool:
			return fmt.Sprintf("%v", v)
		default:
			// For complex types, marshal to JSON
			b, err := json.Marshal(v)
			if err != nil {
				return match
			}
			return string(b)
		}
	})

	// Unmarshal back to map
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(rendered), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rendered payload: %w", err)
	}

	return result, nil
}

// evaluateExpression evaluates a template expression
func evaluateExpression(expr string, wf *Workflow, state *WorkflowState) (interface{}, error) {
	parts := strings.Split(expr, ".")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty expression")
	}

	switch parts[0] {
	case "workflow":
		return evaluateWorkflowAccess(parts[1:], wf)
	case "steps":
		return evaluateStepsAccess(parts[1:], state)
	case "state":
		return evaluateStateAccess(parts[1:], state)
	default:
		// Maybe it's a direct value
		return nil, fmt.Errorf("unknown expression root: %s", parts[0])
	}
}

func evaluateWorkflowAccess(parts []string, wf *Workflow) (interface{}, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("nothing after 'workflow'")
	}

	switch parts[0] {
	case "defaults":
		if wf.Defaults == nil {
			return nil, fmt.Errorf("workflow defaults not set")
		}
		return getNestedValue(wf.Defaults, parts[1:])
	case "name":
		return wf.Name, nil
	default:
		return nil, fmt.Errorf("unknown workflow field: %s", parts[0])
	}
}

func evaluateStepsAccess(parts []string, state *WorkflowState) (interface{}, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("nothing after 'steps'")
	}

	stepID := parts[0]
	output, ok := state.StepOutputs[stepID]
	if !ok {
		return nil, fmt.Errorf("step output not found: %s", stepID)
	}

	if len(parts) == 1 {
		// Return the whole output as map
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		if err := enc.Encode(output); err != nil {
			return nil, err
		}
		var m map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			return nil, err
		}
		return m, nil
	}

	// Access specific field from output
	switch parts[1] {
	case "folder_id":
		return output.FolderID, nil
	case "folder_path":
		return output.FolderPath, nil
	case "drive_links":
		return output.DriveLinks, nil
	case "local_paths":
		return output.LocalPaths, nil
	case "file_hashes":
		return output.FileHashes, nil
	case "status":
		return output.Status, nil
	case "raw":
		if len(parts) > 2 {
			if output.Raw == nil {
				return nil, fmt.Errorf("step %s raw is nil", stepID)
			}
			return getNestedValue(output.Raw, parts[2:])
		}
		return output.Raw, nil
	default:
		return nil, fmt.Errorf("unknown step output field: %s", parts[1])
	}
}

func evaluateStateAccess(parts []string, state *WorkflowState) (interface{}, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("nothing after 'state'")
	}

	switch parts[0] {
	case "workflow_id":
		return state.WorkflowID, nil
	case "status":
		return state.Status, nil
	case "current_step":
		return state.CurrentStep, nil
	default:
		return nil, fmt.Errorf("unknown state field: %s", parts[0])
	}
}

func getNestedValue(m map[string]interface{}, keys []string) (interface{}, error) {
	current := interface{}(m)
	for _, key := range keys {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[key]
			if !ok {
				return nil, fmt.Errorf("key not found: %s", key)
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access nested key on non-map")
		}
	}
	return current, nil
}
