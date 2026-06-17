package jobs

import (
	"encoding/json"
	"fmt"

	"velox/go-master/internal/media/models"
)

// Codec defines a typed interface for encoding/decoding job payloads and results.
// Each job type should implement this interface to eliminate manual map[string]any
// conversions that are scattered across handlers.
//
// Example usage:
//
//	type ArtlistCodec struct{}
//	func (c *ArtlistCodec) JobType() models.JobType { return models.JobTypeArtlistRun }
//	func (c *ArtlistCodec) DecodePayload(raw json.RawMessage) (*RunTagRequest, error) { ... }
type Codec[T any, R any] interface {
	// JobType returns the job type this codec handles.
	JobType() models.JobType

	// EncodePayload converts a typed request to a map for JSON serialization.
	EncodePayload(req T) map[string]any

	// DecodePayload reconstructs a typed request from a JSON raw message.
	DecodePayload(raw json.RawMessage) (T, error)

	// EncodeResult converts a typed response to a map for JSON serialization.
	EncodeResult(resp R) map[string]any

	// DecodeResult reconstructs a typed response from a JSON raw message.
	DecodeResult(raw json.RawMessage) (R, error)
}

// TypedCodec is a generic helper that reduces boilerplate for simple codecs.
// It uses JSON marshal/unmarshal for conversion, which works for any type
// with json tags.
//
// Example:
//
//	type RunTagRequest struct { ... }
//	type RunTagResponse struct { ... }
//	artlistCodec := jobs.NewTypedCodec[*RunTagRequest, *RunTagResponse](models.JobTypeArtlistRun)
type TypedCodec[T any, R any] struct {
	jobType models.JobType
}

// NewTypedCodec creates a new typed codec that uses JSON serialization.
func NewTypedCodec[T any, R any](jobType models.JobType) *TypedCodec[T, R] {
	return &TypedCodec[T, R]{jobType: jobType}
}

// JobType returns the registered job type.
func (c *TypedCodec[T, R]) JobType() models.JobType { return c.jobType }

// EncodePayload marshals the request to a map.
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

// DecodePayload unmarshals a JSON raw message to the request type.
func (c *TypedCodec[T, R]) DecodePayload(raw json.RawMessage) (T, error) {
	var req T
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("failed to decode %T payload: %w", req, err)
	}
	return req, nil
}

// EncodeResult marshals the response to a map.
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

// DecodeResult unmarshals a JSON raw message to the response type.
func (c *TypedCodec[T, R]) DecodeResult(raw json.RawMessage) (R, error) {
	var resp R
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("failed to decode %T result: %w", resp, err)
	}
	return resp, nil
}
