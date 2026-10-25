// Package delivery — retired destination migration surface.
//
// Destination keys are PERSISTED: they land in the transactional outbox,
// in queued job payloads and in API responses callers replay. A key
// therefore outlives the code that introduced it, so retiring a
// destination means removing its Drive folder + storage path while
// keeping the historical rows routable. Deleting the literal key without
// a successor would make every legacy row fail closed at publish time and
// orphan the asset it points at.
//
// This file is the single canonical owner of the retirement map
// (godlike/06 one-owner-per-fact):
//
//	retired "artlist" → DestinationStock
//
// The Artlist provider is retired, its dedicated Drive root folder and
// its `media/artlist` local storage path are gone, and the assets that
// lived there are migrated to the canonical stock destination
// (`cmd/admin migrate-artlist-assets`). A persisted artlist destination
// therefore resolves to DestinationStock instead of being rejected.
//
// A retired key is NOT re-registered in the DestinationRegistry: the
// registry stays the single owner of "what destinations exist", and
// CanonicalizeDestination folds a retired key into one that IS
// registered. Resolution happens at the registry boundary, so legacy
// rows publish through the successor's root folder, path builder and
// conflict policy with no special-casing downstream.
//
// Local bytes are deliberately NOT relocated: the retired
// `media/artlist` directory held downloaded files that persisted
// clip.local_path values point at, so moving them would invalidate every
// stored path. Removing the path SURFACE (the config accessor and the
// startup mkdir) leaves those files readable exactly where the rows say
// they are.
package delivery

// retiredDestinationAliases maps every retired destination key to the
// canonical destination that now owns its assets. The keys are the
// literal wire values carried by older rows — never reuse one of them
// for a new destination, and never let one grow a registry policy again.
var retiredDestinationAliases = map[DestinationKey]DestinationKey{
	// Artlist was a stock-footage provider: its curated clips belong to
	// the stock footage destination now.
	"artlist": DestinationStock,
}

// CanonicalizeDestination folds a retired destination key into its
// canonical successor and returns every other key unchanged.
//
// Callers need it only where they must BRANCH on a key that may arrive
// from storage (outbox rows, queued jobs, replayed payloads): the
// registry already applies the same folding inside Resolve/Has, so a
// Publish through the registry needs no call-site change.
func CanonicalizeDestination(key DestinationKey) DestinationKey {
	if canonical, ok := retiredDestinationAliases[key]; ok {
		return canonical
	}
	return key
}
