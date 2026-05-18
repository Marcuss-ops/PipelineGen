package llmjson

import (
	"encoding/json"
	"fmt"
)

type Mode int

const (
	ModeObject Mode = iota
	ModeArray
	ModeFlexible
)

// DecodeObject extracts and decodes a JSON object from raw LLM output.
func DecodeObject[T any](raw string) (T, error) {
	var result T
	cleaned := StripCodeFence(raw)
	jsonStr := ExtractObject(cleaned)
	if jsonStr == "" {
		return result, fmt.Errorf("no JSON object found in output")
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return result, fmt.Errorf("failed to decode JSON object: %w", err)
	}
	return result, nil
}

// DecodeArray extracts and decodes a JSON array from raw LLM output.
func DecodeArray[T any](raw string) (T, error) {
	var result T
	cleaned := StripCodeFence(raw)
	jsonStr := ExtractArray(cleaned)
	if jsonStr == "" {
		return result, fmt.Errorf("no JSON array found in output")
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return result, fmt.Errorf("failed to decode JSON array: %w", err)
	}
	return result, nil
}

// DecodeFlexible tries to decode as object first, then array.
func DecodeFlexible[T any](raw string, mode Mode) (T, error) {
	switch mode {
	case ModeObject:
		return DecodeObject[T](raw)
	case ModeArray:
		return DecodeArray[T](raw)
	default:
		result, err := DecodeObject[T](raw)
		if err == nil {
			return result, nil
		}
		return DecodeArray[T](raw)
	}
}
