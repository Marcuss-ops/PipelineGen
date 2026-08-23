package script

import "encoding/json"

// DecodeEnvelopeV2 unmarshals raw JSON into a GenerationEnvelopeV2
// and validates it. Returns the decoded envelope on success, or an
// error wrapping ErrInvalidPayload (malformed JSON) or ErrPlanInvalid
// (structural validation failed).
func DecodeEnvelopeV2(raw json.RawMessage) (*GenerationEnvelopeV2, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidPayload
	}
	var env GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	return &env, nil
}
