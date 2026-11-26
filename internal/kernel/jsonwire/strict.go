// Package jsonwire owns the shared strict JSON decoding primitive for the
// kernel wire contracts.
//
// Why it exists. A field in a request body is a promise. encoding/json
// silently drops any key that a struct (or a type with a custom
// UnmarshalJSON) does not read, so a payload can look accepted while the
// operator's intent never reaches the pipeline — the `output.
// drive_subfolder_name` miscplacement survived 62 real job payloads that way.
// Every custom UnmarshalJSON hands its whole subtree to the decoder, which is
// also why DisallowUnknownFields on the OUTER decoder cannot reach inside it.
//
// DecodeStrict re-establishes the gate at each level. The error taxonomy is
// preserved (`json: unknown field "x"`) because HTTP boundaries classify
// errors by that message.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeStrict decodes exactly one JSON value into v, rejecting every key the
// destination does not declare and any trailing value in the same document.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// A second value after the first (`{...} {...}`) is never a valid
	// request: json.Unmarshal would reject it, and a lenient decoder would
	// ignore it. Accept only whitespace/EOF.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("json: unexpected data after the top-level value")
		}
		return err
	}
	return nil
}
