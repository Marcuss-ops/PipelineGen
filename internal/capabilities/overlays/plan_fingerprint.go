package overlays

// Plan fingerprint (P1.4, Go mirror of the Rust `overlay_fingerprint`
// module): deterministic FNV-1a 64 over canonical JSON (object keys sorted
// recursively, arrays ordered, no whitespace). Dependency-free; the
// fingerprint only needs stability for render-skip cache decisions, not
// cryptographic strength.
//
// Volatile keys (timing_state, rendered_at_ms, attempt_id) are stripped by
// FingerprintPlan when stripVolatile is true (default): wall-clock state
// must never change the cache identity of an identical plan. Cross-language
// vectors in TestPlanFingerprint_CrossLanguageVectors pin Rust/Go parity.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// VolatilePlanKeys are stripped before hashing when requested.
var VolatilePlanKeys = []string{"timing_state", "rendered_at_ms", "attempt_id"}

func fnv1a64Hex(data []byte) string {
	hash := uint64(fnvOffset64)
	for _, b := range data {
		hash ^= uint64(b)
		hash *= fnvPrime64
	}
	return fmt.Sprintf("%016x", hash)
}

// canonicalJSON renders value with object keys sorted recursively.
func canonicalJSON(value any) ([]byte, error) {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, normalized); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeJSONValue(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if typed {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(typed.String())
	case float64:
		out, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(out)
	case string:
		out, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(out)
	case []any:
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyJSON, err := json.Marshal(key)
			if err != nil {
				return err
			}
			buf.Write(keyJSON)
			buf.WriteByte(':')
			if err := writeCanonical(buf, typed[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		out, err := json.Marshal(typed)
		if err != nil {
			return err
		}
		buf.Write(out)
	}
	return nil
}

func stripVolatileKeys(value map[string]any) {
	for _, key := range VolatilePlanKeys {
		delete(value, key)
	}
	for _, child := range value {
		stripVolatileValue(child)
	}
}

func stripVolatileValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		stripVolatileKeys(typed)
	case []any:
		for _, item := range typed {
			stripVolatileValue(item)
		}
	}
}

// FingerprintPlan hashes a plan map. When stripVolatile is true the volatile
// keys are removed (deeply) before hashing so identical plans with different
// timing state share one cache entry.
func FingerprintPlan(plan map[string]any, stripVolatile bool) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("plan fingerprint requires a plan")
	}
	working := plan
	if stripVolatile {
		raw, err := json.Marshal(plan)
		if err != nil {
			return "", err
		}
		var copy map[string]any
		if err := json.Unmarshal(raw, &copy); err != nil {
			return "", err
		}
		stripVolatileKeys(copy)
		working = copy
	}
	canonical, err := canonicalJSON(working)
	if err != nil {
		return "", err
	}
	return fnv1a64Hex(canonical), nil
}

// FingerprintPlanJSON hashes raw plan JSON (fails closed on invalid JSON).
func FingerprintPlanJSON(raw []byte, stripVolatile bool) (string, error) {
	var plan map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		return "", fmt.Errorf("plan fingerprint: invalid plan JSON: %w", err)
	}
	return FingerprintPlan(plan, stripVolatile)
}
