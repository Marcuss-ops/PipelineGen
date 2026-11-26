package script

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/jsonwire"

// decodeStrictJSON decodes ONE JSON value into v with the same fail-closed
// contract the HTTP boundary applies.
//
// encoding/json hands the WHOLE subtree to a type that implements
// UnmarshalJSON, so DisallowUnknownFields on the outer decoder stops at that
// type: every key the custom method does not read would be dropped in
// silence. That is exactly how `output.drive_subfolder_name` (a key that
// only exists at output.render.drive_subfolder_name) was discarded in 62/62
// real job payloads while the handler advertised fail-closed decoding.
//
// Every custom UnmarshalJSON in this package therefore decodes through here
// instead of json.Unmarshal. The primitive lives in kernel/jsonwire so
// kernel/media can apply the identical gate without importing this package.
func decodeStrictJSON(data []byte, v any) error {
	return jsonwire.DecodeStrict(data, v)
}

// DecodeGenerationEnvelopeStrict is the canonical strict decode of a
// script.generate request body. The HTTP handler and the offline payload
// linter (cmd/payloadlint) both decode through here, so a key the API rejects
// can never be accepted by tooling that "validates" a job file.
func DecodeGenerationEnvelopeStrict(data []byte) (*GenerationEnvelopeV2, error) {
	var env GenerationEnvelopeV2
	if err := decodeStrictJSON(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
