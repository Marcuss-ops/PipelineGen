package jobs

import (
	"encoding/json"
	"fmt"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Codec defines a typed interface for encoding/decoding job payloads and
// results. Each job type should implement this interface to eliminate
// manual map[string]any conversions scattered across handlers.
//
// The JobType() returns a plain string — not models.JobType — so codecs
// are not coupled to the legacy model types.
type Codec[T any, R any] interface {
	JobType() string
	EncodePayload(req T) map[string]any
	DecodePayload(raw json.RawMessage) (T, error)
	EncodeResult(resp R) map[string]any
	DecodeResult(raw json.RawMessage) (R, error)
}

// TypedCodec is a generic helper using JSON marshal/unmarshal for
// simple codecs. Works for any type with json tags.
type TypedCodec[T any, R any] struct {
	jobType string
}

func NewTypedCodec[T any, R any](jobType string) *TypedCodec[T, R] {
	return &TypedCodec[T, R]{jobType: jobType}
}

func (c *TypedCodec[T, R]) JobType() string { return c.jobType }

func (c *TypedCodec[T, R]) EncodePayload(req T) map[string]any {
	data, err := json.Marshal(req)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func (c *TypedCodec[T, R]) DecodePayload(raw json.RawMessage) (T, error) {
	var req T
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("failed to decode %T payload: %w", req, err)
	}
	return req, nil
}

func (c *TypedCodec[T, R]) EncodeResult(resp R) map[string]any {
	data, err := json.Marshal(resp)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func (c *TypedCodec[T, R]) DecodeResult(raw json.RawMessage) (R, error) {
	var resp R
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("failed to decode %T result: %w", resp, err)
	}
	return resp, nil
}
