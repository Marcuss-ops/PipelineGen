// Package outbox — typed validation errors for the informational
// asset.published consumer.
package jobs

import (
	"errors"
	"fmt"
)

// ErrAssetPublishedTerminalEnvelope aggregates every payload-validation
// failure. Retry cannot repair a missing or malformed required field.
var ErrAssetPublishedTerminalEnvelope = errors.New("asset.published: terminal envelope error")

// ErrAssetPublishedPayloadParse fires when the JSON body is malformed.
var ErrAssetPublishedPayloadParse = fmt.Errorf("%w: payload parse failed", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedSchemaVersionMismatch fires when schema_version does not
// match AssetPublishedSchemaVersion.
var ErrAssetPublishedSchemaVersionMismatch = fmt.Errorf("%w: schema version mismatch", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedAssetIDMissing fires when asset_id is empty.
var ErrAssetPublishedAssetIDMissing = fmt.Errorf("%w: asset_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedDestinationMissing fires when destination is empty.
var ErrAssetPublishedDestinationMissing = fmt.Errorf("%w: destination is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedEventIDMissing fires when event_id is empty.
var ErrAssetPublishedEventIDMissing = fmt.Errorf("%w: event_id is required", ErrAssetPublishedTerminalEnvelope)

// ErrAssetPublishedIdempotencyKeyMissing fires when idempotency_key is empty.
var ErrAssetPublishedIdempotencyKeyMissing = fmt.Errorf("%w: idempotency_key is required", ErrAssetPublishedTerminalEnvelope)
