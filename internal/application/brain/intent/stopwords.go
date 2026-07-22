package intent

// englishStopWords is the canonical stopword set for the English
// intent resolver. Keeping it unexported and language-scoped avoids
// the previous hardcoded combined map.
var englishStopWords = map[string]struct{}{
	// Articles
	"the": {}, "a": {}, "an": {},
	// Conjunctions / prepositions
	"and": {}, "or": {}, "but": {}, "for": {}, "with": {}, "from": {},
	"into": {}, "onto": {}, "about": {}, "above": {}, "across": {},
	"after": {}, "against": {}, "along": {}, "among": {}, "around": {},
	"at": {}, "before": {}, "behind": {}, "below": {}, "beneath": {},
	"beside": {}, "between": {}, "beyond": {}, "by": {}, "down": {},
	"during": {}, "except": {}, "inside": {}, "instead": {},
	"near": {}, "off": {}, "on": {}, "over": {}, "through": {},
	"to": {}, "toward": {}, "towards": {}, "under": {}, "until": {},
	"upon": {}, "within": {}, "without": {},
	// Pronouns / auxiliaries
	"i": {}, "you": {}, "he": {}, "she": {}, "it": {}, "we": {},
	"they": {}, "my": {}, "your": {}, "his": {}, "her": {},
	"its": {}, "our": {}, "their": {}, "this": {}, "that": {},
	"these": {}, "those": {}, "what": {}, "which": {}, "who": {},
	"whom": {}, "whose": {}, "where": {}, "when": {}, "why": {},
	"how": {}, "all": {}, "any": {}, "both": {}, "each": {},
	"few": {}, "more": {}, "most": {}, "other": {}, "some": {},
	"such": {}, "only": {}, "own": {},
	"same": {}, "so": {}, "than": {}, "too": {}, "very": {},
	"just": {}, "now": {}, "then": {}, "here": {},
	// Common verbs / auxiliaries
	"am": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "been": {}, "being": {}, "have": {}, "has": {},
	"had": {}, "do": {}, "does": {}, "did": {}, "will": {},
	"would": {}, "could": {}, "should": {}, "may": {}, "might": {},
	"must": {}, "shall": {}, "can": {}, "cannot": {},
	"get": {}, "got": {}, "gets": {}, "make": {}, "made": {},
	"say": {}, "said": {}, "says": {}, "go": {}, "went": {},
	"goes": {}, "going": {}, "come": {}, "came": {}, "comes": {},
	"know": {}, "knew": {}, "known": {}, "take": {}, "took": {},
	"taken": {}, "see": {}, "saw": {}, "seen": {}, "think": {},
	"thought": {}, "look": {}, "looked": {}, "looking": {},
	"use": {}, "used": {}, "using": {}, "find": {}, "found": {},
	"give": {}, "gave": {}, "given": {}, "tell": {}, "told": {},
	"ask": {}, "asked": {}, "asking": {}, "seem": {}, "seemed": {},
	"want": {}, "wanted": {}, "wanting": {}, "need": {}, "needed": {},
}

// italianStopWords is the canonical stopword set for the Italian
// intent resolver.
var italianStopWords = map[string]struct{}{
	// Articles
	"il": {}, "lo": {}, "la": {}, "i": {}, "gli": {}, "le": {},
	"un": {}, "uno": {}, "una": {}, "dei": {}, "degli": {},
	"della": {}, "delle": {}, "dello": {}, "dell": {},
	// Prepositions / articled prepositions
	"di": {}, "a": {}, "da": {}, "in": {}, "con": {}, "su": {},
	"per": {}, "tra": {}, "fra": {}, "al": {}, "allo": {}, "alla": {},
	"ai": {}, "agli": {}, "alle": {}, "dal": {}, "dallo": {}, "dalla": {},
	"dai": {}, "dagli": {}, "dalle": {}, "nel": {}, "nello": {},
	"nella": {}, "nei": {}, "negli": {}, "nelle": {}, "col": {},
	"coi": {}, "sul": {}, "sulla": {}, "sui": {}, "sulle": {},
	// Conjunctions / pronouns / adverbs
	"e": {}, "che": {}, "ma": {}, "se": {}, "come": {}, "anche": {},
	"più": {}, "meno": {}, "quando": {}, "dove": {}, "perché": {},
	"perche": {}, "perchè": {}, "chi": {}, "cosa": {}, "quale": {},
	"quali": {}, "tutto": {}, "tutti": {}, "ogni": {}, "qualche": {},
	"molto": {}, "poco": {}, "troppo": {}, "abbastanza": {},
	"qui": {}, "li": {}, "vi": {}, "ci": {},
	"si": {}, "mi": {}, "ti": {},
	// Common verbs / auxiliaries
	"essere": {}, "sono": {}, "sei": {}, "è": {},
	"era": {}, "erano": {}, "fui": {}, "fu": {}, "furono": {},
	"avere": {}, "ho": {}, "hai": {}, "ha": {}, "hanno": {},
	"avevo": {}, "aveva": {}, "avevano": {}, "avuto": {},
	"fare": {}, "faccio": {}, "fai": {}, "fa": {}, "fanno": {},
	"fatto": {}, "facevo": {}, "faceva": {},
	"potere": {}, "posso": {}, "puoi": {}, "può": {}, "possono": {},
	"volere": {}, "voglio": {}, "vuoi": {}, "vuole": {}, "vogliono": {},
	"dovere": {}, "devo": {}, "devi": {}, "deve": {}, "devono": {},
	"andare": {}, "vado": {}, "vai": {}, "va": {}, "vanno": {},
}

// fallbackStopWords is a tiny cross-linguistic stopword set used by
// the multilingual fallback resolver. It is intentionally small so
// it does not over-filter unknown languages.
var fallbackStopWords = map[string]struct{}{
	"the": {}, "and": {}, "a": {}, "an": {}, "of": {}, "in": {},
	"to": {}, "is": {}, "it": {}, "for": {}, "on": {}, "with": {},
	"at": {}, "by": {}, "from": {},
	"le": {}, "la": {}, "de": {}, "et": {}, "un": {}, "une": {},
	"el": {}, "los": {}, "y": {}, "o": {},
}
