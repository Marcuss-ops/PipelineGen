package cliprender

import (
	"encoding/json"
	"fmt"
)

// DecodeContinuation reads a continuation out of a settle-phase payload.
func DecodeContinuation(raw json.RawMessage) (Continuation, error) {
	var c Continuation
	if len(raw) == 0 {
		return c, fmt.Errorf("%w: settle phase requires a continuation payload", ErrInvalidJobPayload)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%w: decode continuation: %v", ErrInvalidJobPayload, err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
