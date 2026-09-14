// Package texttracks — media_ports.go: the two narrow ports the
// materializer needs AFTER it has persisted the new translations.
//
// POSTGRES-MEDIA-CUTOVER follow-up (September 2026): the
// `asset.text.materialize` pipeline used to (a) emit its
// asset.index.requested event into the operational SQLite outbox and
// (b) request a reindex of a search_text that never contained the
// translations. Both were invisible to the PostgreSQL media index plane,
// which is the ONLY owner of media indexing:
//
//	SQLite outbox          → no media handler is registered there in ANY
//	                         mode (assertSingleMediaIndexOwner fails boot
//	                         closed on one), so the event dead-lettered.
//	media_assets.search_text → written at commit time from the YouTube
//	                         Step-9 metadata envelope only; the READY
//	                         transcripts (original + translations) never
//	                         entered it, so the E5 embedding the index
//	                         worker produced covered no translated text.
//
// These two ports are the canonical seam for fixing that: the
// materializer expresses intent, the media SSOT owns the writes.
//
// godlike/06 SSOT: the domain-feature packages must NOT reach into the
// PostgreSQL adapter; the ports live here and the composition root binds
// the production concretes (pgmedia.ReindexRequester +
// pgmedia.SearchTextRebuilder) exactly as it does for every other
// capability→platform edge.
package texttracks

import "context"

// IndexRequester is the narrow port the materializer uses to request a
// reindex of one asset on the canonical media index plane.
//
// Producer contract: the request MUST end up in the PostgreSQL media
// outbox so the single media index owner (PostgresIndexWorker) drains it.
// The production concrete is *pgmedia.ReindexRequester, which resolves
// the asset row, enforces the canonical registered-vs-searchable policy
// and writes the canonical asset.index.requested envelope inside a
// transaction on the media SSOT — including the terminal-conflict
// handling that forces a fresh event when an earlier request already
// completed.
//
// Callers MUST NOT implement this port against the operational SQLite
// outbox: media index events written there have no consumer and
// dead-letter.
type IndexRequester interface {
	// RequestIndex asks the canonical media index plane to (re)index the
	// asset. RequestIndex is idempotent at the outbox level: a duplicate
	// in-flight request coalesces, and a request whose predecessor already
	// reached a terminal state produces a fresh event.
	RequestIndex(ctx context.Context, assetID string) error
}

// SearchTextRebuilder recomposes media_assets.search_text from the
// asset's canonical metadata PLUS every READY transcript track — the
// original AND the translations the materializer just persisted.
//
// Why this step exists: the PostgreSQL index worker embeds
// media_assets.search_text verbatim (pgmedia.EmbedAssetTextAdapter). The
// YouTube commit-time envelope deliberately carries no transcript (Step 9
// contract), so without this rebuild the translated rows exist in
// asset_text_tracks but never reach the E5 multilingual embedding. The
// rebuild is the missing "translations → embedding input" edge.
//
// Contract:
//   - Rebuild is idempotent: running it twice with unchanged inputs
//     rewrites nothing and reports changed=false.
//   - changed=true means search_text was rewritten, so the caller should
//     request a reindex.
//   - An unknown asset is an error (fail closed); a missing or empty
//     composition is NOT an error (the asset simply has no indexable
//     text yet) and reports changed=false so the existing value is
//     preserved rather than blanked.
type SearchTextRebuilder interface {
	Rebuild(ctx context.Context, assetID string) (changed bool, err error)
}
