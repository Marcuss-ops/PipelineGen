// Package idempotency is the canonical owner of the 3 idempotency
// key shapes that gate every duplicate-write surface in PipelineGen
// (Fase 5, July 2026).
//
// The 3 key shapes:
//
//	AssetKey(provider, clipID, sourceVersion, sha256File) string
//	  → "provider:clipID:sourceVersion:sha256File"
//	  For media_assets row identity (the UNIQUE column id).
//	  Guarantees: discovery dedup, scraper dedup, file-content dedup.
//
//	JobKey(provider, clipID, sourceVersion) string
//	  → "provider:clipID:sourceVersion"
//	  For outbox event_key (the UNIQUE column event_key).
//	  Guarantees: replay dedup, crash-recovery dedup.
//
//	OutboxKey(eventType, provider, clipID, sourceVersion) string
//	  → "eventType:provider:clipID:sourceVersion"
//	  For Qdrant upsert outbox event_key.
//	  Distinct from JobKey because the event_type disambiguates
//	  the dispatch target (asset.index.requested.v1 vs
//	  asset.drive.delete_requested.v1).
//
// godlike/06 SSOT (Fase 5 user spec — Stabilisci la chiave
// canonica di idempotenza):
//
//	This package is the SOLE canonical owner of the 3 key
//	constructors. Other packages (artlist, outboxevents, drive,
//	clipindexer) MUST use these functions, not ad-hoc string
//	concatenations. A drift between the canonical key shape
//	and an ad-hoc producer surfaces as duplicate rows that the
//	UNIQUE constraint can't catch (the two producers hash to
//	different keys, so ON CONFLICT (event_key) DO NOTHING
//	doesn't fire).
//
// godlike/07 NO-FAKE-AVAILABILITY:
//
//	Every constructor rejects empty inputs with a typed error
//	(ErrEmptyProvider, ErrEmptyClipID, ErrEmptySourceVersion,
//	ErrEmptySHA256, ErrEmptyEventType). The dedup guarantees
//	depend on the keys being COMPLETE — a silently-hashed-empty
//	input is the silent-loss anti-pattern the gates exist to
//	prevent. The user spec literal "Niente dipendenza dal solo
//	titolo" is enforced by the function signature: none of the
//	3 constructors takes a title or name parameter.
package idempotency

import "errors"

// Typed sentinels for the fail-closed guards. Callers branch
// on errors.Is to surface a typed diagnostic in operator logs.
// All sentinels carry the canonical "no fake availability"
// reason text for grep-ability.
var (
	// ErrEmptyProvider — provider is required for every key.
	// A missing provider is the canonical "wiring bug" case
	// (the caller forgot to thread the provider through).
	ErrEmptyProvider = errors.New("idempotency: provider is required (godlike/07 — no fake availability)")
	// ErrEmptyClipID — clip_id is required for every key.
	// A missing clip_id is the canonical "no fake identity"
	// case (the caller would otherwise hash a phantom row).
	ErrEmptyClipID = errors.New("idempotency: clip_id is required (godlike/07 — no fake availability)")
	// ErrEmptySourceVersion — source_version is required for
	// every key. A missing source_version is the canonical
	// "stale CAS fence" case (the worker didn't read the
	// current source_version before enqueueing).
	ErrEmptySourceVersion = errors.New("idempotency: source_version is required (godlike/07 — no fake availability)")
	// ErrEmptySHA256 — sha256(file) is required for AssetKey
	// (the row identity). A missing sha256 is the canonical
	// "download not yet complete" case — the caller should
	// use JobKey until the file is downloaded and the
	// sha256 is computed.
	ErrEmptySHA256 = errors.New("idempotency: sha256 file digest is required for AssetKey (use JobKey until the file is downloaded)")
	// ErrEmptyEventType — event_type is required for OutboxKey
	// (the dispatch target disambiguator).
	ErrEmptyEventType = errors.New("idempotency: event_type is required for OutboxKey (godlike/07 — no fake availability)")
)

// AssetKey constructs the canonical media_assets row identity.
// The 4-tuple (provider, clipID, sourceVersion, sha256File) is
// the user-spec literal from Fase 5 (July 2026):
//
//	"provider+clip_id+source_version+sha256(file) per gli asset"
//
// The 4-tuple is delimited by ':' (the codebase convention from
// existing key shapes — see BuildIndexEventKey in
// domain/asset/clip_identity.go which uses ':' as the segment
// separator; see also ArtifactIdempotencyKey in
// domain/remote/idempotency.go which uses ':' in the same way).
//
// Empty inputs are rejected with a typed error so the gates
// downstream (UNIQUE on media_assets.id, scraper dedup, etc.)
// cannot be silently bypassed by a half-wired caller.
//
// Invariant: JobKey(p, c, s) is a strict prefix of
// AssetKey(p, c, s, h) when the sha256 segment is appended.
// This lets a writer construct JobKey first (at job creation
// time, before the file is downloaded) and then upgrade to
// AssetKey (once the sha256 is computed) without changing
// the first 3 segments. The invariant is verified by
// TestJobKey_IsPrefixOfAssetKey in keys_test.go.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty sha256File is NOT
// a sentinel for "first attempt" or "not yet downloaded" —
// the asset row identity requires the file fingerprint.
// A caller that doesn't have the sha256 yet must use JobKey
// (the 3-tuple, no file hash) until the file is downloaded
// and the sha256 is computed. The writer rejects AssetKey
// calls with an empty sha256File.
func AssetKey(provider, clipID, sourceVersion, sha256File string) (string, error) {
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	if sha256File == "" {
		return "", ErrEmptySHA256
	}
	return provider + ":" + clipID + ":" + sourceVersion + ":" + sha256File, nil
}

// JobKey constructs the canonical outbox event_key for a job
// (provider+clip_id+source_version, no file hash — the file
// may not exist yet at job-creation time). The 3-tuple is the
// user-spec literal from Fase 5:
//
//	"provider+clip_id+source_version per i job/outbox"
//
// Empty inputs are rejected with typed errors (same
// godlike/07 fail-closed discipline as AssetKey).
//
// Use JobKey for: outbox events that don't have a file
// fingerprint yet (e.g. the discovery event at job creation,
// the scraper-candidate event before download). Use AssetKey
// for: media_assets row identity + outbox events that have
// already computed the file sha256.
func JobKey(provider, clipID, sourceVersion string) (string, error) {
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	return provider + ":" + clipID + ":" + sourceVersion, nil
}

// OutboxKey constructs the canonical outbox event_key for a
// Qdrant upsert event. The 4-tuple (eventType, provider,
// clipID, sourceVersion) extends JobKey with an event_type
// prefix so the dispatch target (e.g. asset.index.requested.v1
// vs asset.drive.delete_requested.v1) is part of the dedup
// identity. The event_type also provides SSOT surface for
// the dispatcher routing — one event_key per event_type per
// asset; the same asset can have multiple event_keys for
// different event_types in the outbox.
//
// Empty event_type is rejected with ErrEmptyEventType (the
// other 3 empty-input guards mirror AssetKey / JobKey).
//
// OutboxKey is the canonical surface for the "Qdrant upsert
// outbox key" mentioned in the user spec. The existing
// BuildIndexEventKey in domain/asset/clip_identity.go
// (which produces "index:assetID:sha256-hex16:...") is a
// specialized variant that adds model + version + collection
// segments; OutboxKey is the GENERAL form that
// BuildIndexEventKey will eventually route through (per the
// domain/asset/clip_identity.go comment that flags the
// infra-layer indexEventKey as a future refactor target).
func OutboxKey(eventType, provider, clipID, sourceVersion string) (string, error) {
	if eventType == "" {
		return "", ErrEmptyEventType
	}
	if provider == "" {
		return "", ErrEmptyProvider
	}
	if clipID == "" {
		return "", ErrEmptyClipID
	}
	if sourceVersion == "" {
		return "", ErrEmptySourceVersion
	}
	return eventType + ":" + provider + ":" + clipID + ":" + sourceVersion, nil
}
