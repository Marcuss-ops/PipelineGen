package script

import (
	"sort"
	"strings"
)

// BuiltInAudioKind classifies a built-in editorial audio alias accepted on the
// wire by GenerationItemV2.Audio.
type BuiltInAudioKind string

const (
	// BuiltInAudioBackgroundMusic is a built-in alias for
	// Audio.BackgroundMusic.
	BuiltInAudioBackgroundMusic BuiltInAudioKind = "background_music"
	// BuiltInAudioSoundEffect is a built-in one-shot alias (or resolver
	// directive) for Audio.SoundEffects.
	BuiltInAudioSoundEffect BuiltInAudioKind = "sound_effect"
)

// ── The built-in audio vocabulary ─────────────────────────────────────────
//
// This is the ONE declaration of "which audio asset ids are built-in". It
// exists because that question used to be answered three different ways: the
// predicates that lived in audio_spec.go derived it from string prefixes
// (`bgm` + a length/range check, the whole `whop*`/`whoop*` space, the whole
// `whoosh*` space), while the catalog that actually binds an alias to a Drive
// identity is
// internal/capabilities/mediaregistry (EditorialAudioAssets). The two disagreed
// as soon as the catalog retired the `whoop*` aliases: the prefix predicates
// kept granting the built-in gain default to aliases that no longer resolved
// to anything.
//
// The vocabulary is declared in kernel because that is the only layer that may
// own it: percheck_kernel_boundary forbids kernel -> capabilities, so this
// package cannot read the catalog, and the capability that binds the names may
// not invent built-in names of its own. The drift gate that keeps the two in
// step in BOTH directions lives with the catalog
// (internal/capabilities/mediaregistry/builtin_audio_vocabulary_contract_test.go).
//
// The lists are explicit rather than generated from a count on purpose: a
// closed wire vocabulary must be reviewable as a diff.

// builtInCanonicalAliases are the aliases the canonical editorial catalog
// binds to a Drive identity and editorial metadata
// (internal/capabilities/mediaregistry/editorial_catalog.go). Only these may be
// presented to a caller as editorial BGM/SFX assets.
var builtInCanonicalAliases = map[string]BuiltInAudioKind{
	"bgm1": BuiltInAudioBackgroundMusic,
	"bgm2": BuiltInAudioBackgroundMusic,
	"bgm3": BuiltInAudioBackgroundMusic,
	"bgm4": BuiltInAudioBackgroundMusic,
	"bgm5": BuiltInAudioBackgroundMusic,
	"bgm6": BuiltInAudioBackgroundMusic,

	"whop1": BuiltInAudioSoundEffect,
	"whop2": BuiltInAudioSoundEffect,
	"whop3": BuiltInAudioSoundEffect,
	"whop4": BuiltInAudioSoundEffect,
	"whop5": BuiltInAudioSoundEffect,
	"whop6": BuiltInAudioSoundEffect,
}

// builtInCompatAliases are historical aliases that are deliberately NOT in the
// canonical catalog but must keep decoding as built-in, so the safe default
// (a one-shot effect is attenuated unless the caller says otherwise) is not
// silently dropped for payloads written against the earlier vocabulary.
//
//   - whoop1..whoop4 were ambiguous: the same Drive identity was addressable as
//     a BGM and as a "whoop", so the canonical catalog retired them.
//   - whoosh1..whoosh9 are the family the random_whoosh directive selects from
//     and remain resolvable through the media registry.
//   - random_whoosh is a resolver directive, not an asset id: it is expanded to
//     one of the whoosh aliases before resolution.
//   - whop/whoop/whoosh (bare) are the family names the retired prefix rule
//     also accepted; they are enumerated here instead of being re-matched by
//     prefix, so a caller-supplied registry id such as `whoosh_custom_hit`
//     stops silently inheriting the built-in attenuation.
var builtInCompatAliases = map[string]BuiltInAudioKind{
	"whoop1": BuiltInAudioSoundEffect,
	"whoop2": BuiltInAudioSoundEffect,
	"whoop3": BuiltInAudioSoundEffect,
	"whoop4": BuiltInAudioSoundEffect,

	"whop":   BuiltInAudioSoundEffect,
	"whoop":  BuiltInAudioSoundEffect,
	"whoosh": BuiltInAudioSoundEffect,

	"whoosh1": BuiltInAudioSoundEffect,
	"whoosh2": BuiltInAudioSoundEffect,
	"whoosh3": BuiltInAudioSoundEffect,
	"whoosh4": BuiltInAudioSoundEffect,
	"whoosh5": BuiltInAudioSoundEffect,
	"whoosh6": BuiltInAudioSoundEffect,
	"whoosh7": BuiltInAudioSoundEffect,
	"whoosh8": BuiltInAudioSoundEffect,
	"whoosh9": BuiltInAudioSoundEffect,

	"random_whoosh": BuiltInAudioSoundEffect,
}

// RandomWhooshDirective is the SFX asset_id a caller writes to let the
// pipeline pick a whoosh deterministically.
const RandomWhooshDirective = "random_whoosh"

// BuiltInAudioAliasKind reports whether id is a built-in editorial audio alias
// and of which kind. Matching is case-insensitive and whitespace-tolerant, the
// same normalization the wire decoder applied before this table existed.
// Unknown ids are not built-in: they are either caller-supplied registry ids
// or an error for the layer that resolves them.
func BuiltInAudioAliasKind(id string) (BuiltInAudioKind, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	if kind, ok := builtInCanonicalAliases[id]; ok {
		return kind, true
	}
	kind, ok := builtInCompatAliases[id]
	return kind, ok
}

// BuiltInAudioCanonicalAliases returns the sorted-free, declaration-ordered
// list of aliases the canonical editorial catalog binds. The drift gate in
// internal/capabilities/mediaregistry compares this set with the catalog.
func BuiltInAudioCanonicalAliases() []string {
	out := make([]string, 0, len(builtInCanonicalAliases))
	for alias := range builtInCanonicalAliases {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

// BuiltInWhooshAliases returns the whoosh family in index order. The
// random_whoosh selection uses it, so the size of the family is declared once
// here instead of being re-typed as a modulo operand next to the hash that
// consumes it.
func BuiltInWhooshAliases() []string {
	return []string{
		"whoosh1", "whoosh2", "whoosh3", "whoosh4", "whoosh5",
		"whoosh6", "whoosh7", "whoosh8", "whoosh9",
	}
}
