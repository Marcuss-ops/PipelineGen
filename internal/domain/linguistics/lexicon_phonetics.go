package linguistics

// lexicon_phonetics.go is the split-by-capability (Phase 8) CAPABILITY
// MARKER file for the "phonetics" lexical domain of the
// LexiconRegistry pipeline.
//
// The current implementation has NO phonetic data in LexiconProfile
// (no soundex index, no IPA mapping, no phonetic-similarity scorer).
// The LexiconProfile struct carries stopword sets, function-word sets,
// an entity blocklist, negative particles, visual verbs, verb
// suffixes and a phrase-extraction policy — none of which are
// phonetic. The user's 5-domain split (phonetics / morphology /
// stop-words / aliasing / scoring) reserves this slot for future
// phonetic capabilities so the architecture graph (godlike/07 SSOT
// for machine-readable ownership) surfaces the placeholder.
//
// This file is intentionally documentation-only — zero Go declarations
// — to mirror the Phase 7 capability-marker pattern (cached_search.go
// / retry_fallback.go in the artlist package) and the Phase 9
// cutter_audio.go marker. Future phonetic capability additions
// (soundex encoding, IPA mapping, phonetic-similarity scoring) should
// land in this file so the SSOT discoverability invariant is preserved.
