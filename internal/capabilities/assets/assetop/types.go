package assetop

import "errors"

// ErrAssetStoreMissing is the fail-closed sentinel returned when the dedupe
// service was constructed without a record store. Reporting "no duplicate" for
// an unreadable catalog would be a successful no-op that silently duplicates
// an asset, so the check refuses instead.
var ErrAssetStoreMissing = errors.New("assetop: duplicate check requires an asset record store")
