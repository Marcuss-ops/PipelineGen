package cliprender

import "encoding/json"

func encodeContinuationPayload(c Continuation) (json.RawMessage, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(continuationPayload{RenderPhase: RenderPhaseSettle, Continuation: &c})
}
