package asset

import "errors"

// ErrContentAddressMismatch marks a deterministic producer-contract violation:
// the bytes offered for a content address do not hash to that address. It is
// NOT transient. Every boundary that stages or relays assets must preserve it
// so retry policy and durable error codes can tell "the producer lied about its
// own content" apart from a network or capacity failure.
//
// The object-store staging bridge (internal/platform/renderinggen) wraps it on
// an HTTP 422 from the store's content-address verification; the render runner
// maps it to ASSET_CONTENT_MISMATCH, and RenderingGen fails the item
// permanently instead of burning its retry budget.
var ErrContentAddressMismatch = errors.New("asset content address mismatch")
