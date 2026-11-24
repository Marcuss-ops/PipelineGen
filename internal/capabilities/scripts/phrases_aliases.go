// Package scriptgeneration — phrases_aliases.go is the facade over
// scripts/phrases, the leaf that owns the phrase-selection wire contract.
//
// The root keeps naming the request, the profile and the selector unqualified
// so the runner, the projection helpers and the tests stay unchanged; the
// definitions live in the leaf so vidrush can use them without importing the
// root.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"

type (
	// PhraseSelectionProfile is the lexicon data the Rust selector reads.
	PhraseSelectionProfile = phrases.PhraseSelectionProfile
	// PhraseSelectionRequest asks the Rust selector for grounded phrases.
	PhraseSelectionRequest = phrases.PhraseSelectionRequest
	// SelectedPhrase is one phrase returned by the Rust selector.
	SelectedPhrase = phrases.SelectedPhrase
	// PhraseSelector is the single production seam for phrase selection.
	PhraseSelector = phrases.PhraseSelector
)

var (
	// importantPhraseWord is the word tokenizer used to project phrases.
	importantPhraseWord = phrases.ImportantPhraseWord
	// NewPhraseSelectionProfile projects a lexicon profile onto the wire.
	NewPhraseSelectionProfile = phrases.NewPhraseSelectionProfile
	// phraseSelectionProfileForLanguage loads the configured lexicon profile.
	phraseSelectionProfileForLanguage = phrases.ProfileForLanguage
	// importantWordsFromPhrases projects the strongest words inside the
	// selected phrases.
	importantWordsFromPhrases = phrases.ImportantWordsFromPhrases
	// containsProperNamePair is the ingress guard for operator phrase hints.
	containsProperNamePair = phrases.ContainsProperNamePair
	// completeGroundedPhrase completes a selected phrase to its sentence.
	completeGroundedPhrase = phrases.CompleteGroundedPhrase
)
