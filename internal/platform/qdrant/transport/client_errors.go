// client_errors.go — wire-level error helpers + operation-label constants.
//
// PR2 mechanical split (June 2026): relocated from client.go without
// signature or behaviour changes. The op* constants below are the
// canonical labels passed to Client.parseErrorWith by the methods
// that need an operation label (so the resulting *APIError.Operation
// field carries the originating method name into log lines and the
// jobs.Service retry-decision path). They live next to parseError /
// parseErrorWith because that is their only consumer; placing them
// inside client_errors.go (rather than client_request.go or
// client.go proper) keeps the surface-area rule "operation labels
// are inputs to parseError" honest.
package transport

import (
	"fmt"
	"net/http"
)

// Operation names used as APIError.Operation discriminator.
// Only the call sites that actually pass a label are kept; the
// retired labels were removed as dead code. The labels flow into
// log lines (`qdrant.GetCollection: HTTP 404: …`) so per-method
// operators can grep them without parsing the underlying message.
const (
	opGetCollection = "GetCollection"
	opCountPoints   = "CountPoints"
)

// parseError converts a non-2xx Qdrant response into a typed *APIError.
//
// PR1 — fix/qdrant-wire-contracts: this is the single canonical entry
// for every wire-level error the client surfaces. Callers MUST use
// the typed value (errors.As) rather than parsing the message string
// downstream. Use parseErrorWith when the call site knows which method
// the request came from so the operation label is meaningful in logs.
func (c *Client) parseError(resp *http.Response) error {
	return c.parseErrorWith("qdrant", resp)
}

// parseErrorWith is parseError + an explicit operation label. All
// Client public methods that issue HTTP requests SHOULD pass their
// op* constant so the labelled error indicates which endpoint
// failed.
func (c *Client) parseErrorWith(op string, resp *http.Response) error {
	if resp == nil {
		return &APIError{
			Operation: op,
			Status:    0,
			Message:   "nil response",
			Retryable: true,
		}
	}
	body := readAPIBody(resp.Body)
	return &APIError{
		Operation: op,
		Status:    resp.StatusCode,
		Message:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, body),
		Body:      body,
		Retryable: classifyRetryability(resp.StatusCode),
	}
}
