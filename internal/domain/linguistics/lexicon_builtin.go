package linguistics

// Built-in language profiles (Phase 8 split). These provide the
// zero-disk fallback data used when no config/lexicons directory is
// present at bootstrap — the registry falls back to a cross-language
// union so tests pass without files on disk and the default lexicon
// stays functional before the composition root installs a file-
// backed registry.
//
// addAll is the small helper that bulk-inserts words into a set;
// placed here (next to the only callers — builtInStopWords +
// builtInFunctionWords + builtInEntityBlocklist) so the data and
// the population helper stay co-located.

// addAll is a helper that inserts every word into the set.
func addAll(m map[string]struct{}, words ...string) {
	for _, w := range words {
		m[w] = struct{}{}
	}
}

// builtInFallbackProfile returns a comprehensive built-in lexicon
// that works across English, Italian, Spanish, French, German and
// Portuguese. It is used only when no "fallback" directory is present
// under config/lexicons/ and ensures tests pass without files on disk.
func builtInFallbackProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords:         builtInStopWords(),
		FunctionWords:     builtInFunctionWords(),
		EntityBlocklist:   builtInEntityBlocklist(),
		NegativeParticles: map[string]struct{}{"not": {}, "no": {}, "never": {}, "neither": {}, "non": {}, "mai": {}, "ne": {}},
		VisualVerbs:       map[string]struct{}{},
		VerbSuffixes:      []string{"ing", "ed", "are", "ere", "ire", "ando", "endo", "ato", "uto", "ito"},
		PhrasePolicy:      DefaultPhraseExtractionPolicy(),
	}
}

func builtInStopWords() map[string]struct{} {
	m := make(map[string]struct{}, 500)
	addAll(m,
		// English
		"the", "a", "an", "and", "or", "but", "for", "with", "from",
		"into", "onto", "about", "above", "across", "after", "against",
		"along", "among", "around", "at", "before", "behind", "below",
		"beneath", "beside", "between", "beyond", "by", "down", "during",
		"except", "inside", "instead", "near", "off", "on", "over",
		"through", "to", "toward", "towards", "under", "until", "upon",
		"within", "without", "you", "he", "she", "we", "they", "my",
		"your", "his", "her", "its", "our", "their", "this", "that",
		"these", "those", "what", "which", "who", "whom", "whose",
		"where", "when", "why", "how", "all", "any", "both", "each",
		"few", "more", "most", "other", "some", "such", "only", "own",
		"same", "so", "than", "too", "very", "just", "now", "then",
		"here", "am", "is", "are", "was", "were", "be", "been", "being",
		"have", "has", "had", "do", "does", "did", "will", "would",
		"could", "should", "may", "might", "must", "shall", "can",
		"cannot", "get", "got", "gets", "make", "made", "say", "said",
		"says", "go", "went", "goes", "going", "come", "came", "comes",
		"know", "knew", "known", "take", "took", "taken", "see", "saw",
		"seen", "think", "thought", "look", "looked", "looking", "use",
		"used", "using", "find", "found", "give", "gave", "given",
		"tell", "told", "ask", "asked", "asking", "seem", "seemed",
		"want", "wanted", "wanting", "need", "needed", "not", "no",
		"as", "if", "it", "i",
		// Italian
		"il", "lo", "la", "gli", "le", "un", "uno", "una", "dei",
		"degli", "della", "delle", "dello", "dell", "di", "da", "in",
		"con", "su", "per", "tra", "fra", "al", "allo", "alla", "ai",
		"agli", "alle", "dal", "dallo", "dalla", "dai", "dagli",
		"dalle", "nel", "nello", "nella", "nei", "negli", "nelle",
		"col", "coi", "sul", "sulla", "sui", "sulle", "e", "che",
		"ma", "se", "anche", "più", "meno", "quando", "dove", "perché",
		"chi", "cosa", "quale", "quali", "tutto", "tutti", "ogni",
		"qualche", "molto", "poco", "troppo", "abbastanza", "qui",
		"vi", "ci", "si", "mi", "ti", "sono", "sei", "è", "era",
		"erano", "fui", "fu", "furono", "avere", "ho", "hai", "ha",
		"hanno", "avevo", "aveva", "avevano", "avuto", "fare", "faccio",
		"fai", "fa", "fanno", "fatto", "potere", "posso", "puoi",
		"può", "possono", "volere", "voglio", "vuoi", "vuole",
		"vogliono", "dovere", "devo", "devi", "deve", "devono",
		"andare", "vado", "vai", "va", "vanno", "non", "del", "c",
		// Spanish
		"el", "los", "las", "unos", "unas", "de", "en", "y", "o",
		"que", "es", "son", "por", "para", "como", "pero", "sus",
		"me", "te", "nos", "os", "les", "tu", "ella", "nosotros",
		"vosotros", "ellos", "ellas",
		// French
		"une", "des", "du", "à", "par", "sur", "et", "ou", "mais",
		"comme", "ce", "cette", "sont", "est", "dans", "avec", "sans",
		"pas", "ne", "plus", "au", "aux", "ses", "nous", "vous",
		"ils", "elles",
		// German
		"der", "die", "das", "ein", "eine", "einen", "von", "zu",
		"mit", "für", "auf", "und", "oder", "aber", "wenn", "dass",
		"wie", "dieser", "diese", "sind", "ist", "nicht", "den", "dem",
		"einer", "eines", "einem", "nach", "aus", "bei", "vor",
		"durch", "über", "unter", "zwischen", "um", "gegen", "ohne",
		"bis", "zum", "zur", "beim", "vom", "als", "nur", "noch",
		"schon", "auch", "immer", "wieder", "etwas", "man", "ich",
		"er", "sie", "wir", "ihr",
		// Portuguese
		"os", "as", "do", "da", "dos", "das", "em", "na", "nas",
		"com", "sem", "ao", "aos", "não", "foi", "era", "ser",
		"mas", "pela", "pelo", "pelos", "pelas", "num", "numa",
		"dum", "duma", "este", "esta", "estes", "estas", "isso",
		"aquilo",
	)
	return m
}

func builtInFunctionWords() map[string]struct{} {
	m := make(map[string]struct{}, 200)
	addAll(m,
		"the", "a", "an", "of", "in", "on", "at", "to", "for", "with",
		"by", "from", "this", "that", "these", "those", "what", "which",
		"who", "whom", "whose", "where", "when", "why", "how", "all",
		"any", "both", "each", "every", "few", "more", "most", "some",
		"such", "only", "own", "same", "than", "too", "very", "just",
		"now", "then", "here", "there", "yet", "still", "subsequent",
		"subsequently", "previous", "previously", "following", "next",
		"before", "after", "during", "mount", "mountains", "river",
		"ocean", "sea", "august", "september", "october", "november",
		"december", "january", "february", "march", "april", "june",
		"july", "monday", "tuesday", "wednesday", "thursday", "friday",
		"saturday", "sunday", "north", "south", "east", "west", "first",
		"second", "third", "last", "one", "two", "three", "four", "five",
		"many", "other", "another", "behind", "beneath", "beyond",
		"above", "below", "within", "outside", "despite", "although",
		"because", "since", "unless", "while", "through", "across",
		"around", "along", "between", "again", "also", "perhaps",
		"possibly", "indeed", "though", "however", "moreover",
		"furthermore", "nevertheless", "meanwhile", "otherwise",
		"instead", "thus", "therefore", "hence", "consequently",
		"accordingly", "similarly", "likewise", "notably", "importantly",
		"surprisingly", "finally", "ultimately", "eventually",
		"gradually", "essentially", "basically", "primarily", "mainly",
		"particularly", "especially", "specifically", "generally",
		"typically", "usually", "normally", "commonly", "widely",
		"deeply", "greatly", "strongly", "highly", "western", "eastern",
		"northern", "southern", "ancient", "modern", "new", "old",
		"young", "great", "small", "large", "long", "short", "so",
		"law", "laws", "legal", "religion", "religions", "culture",
		"cultures", "society", "societies", "empire", "kingdom",
		"republic", "civilization", "people", "nations", "peoples",
		"world", "history",
		// Italian
		"il", "lo", "la", "gli", "le", "un", "uno", "una", "del",
		"dello", "della", "dei", "degli", "delle", "al", "allo",
		"alla", "ai", "agli", "alle", "dal", "dallo", "dalla", "dai",
		"dagli", "dalle", "nel", "nello", "nella", "nei", "negli",
		"nelle", "sul", "sullo", "sulla", "sui", "sugli", "sulle",
		"col", "collo", "colla", "coi", "cogli", "colle", "su", "per",
		"con", "tra", "fra", "di", "da", "che", "chi", "questo",
		"questa", "questi", "queste", "quello", "quella", "quelli",
		"quelle", "suo", "sua", "suoi", "sue", "mio", "mia", "tuo",
		"tua", "nostro", "nostra",
	)
	return m
}

func builtInEntityBlocklist() map[string]struct{} {
	return map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "for": {},
		"nor": {}, "yet": {}, "so": {}, "in": {}, "on": {}, "at": {}, "to": {},
		"by": {}, "with": {}, "from": {}, "of": {}, "is": {}, "are": {},
		"was": {}, "were": {}, "be": {}, "been": {}, "being": {}, "have": {},
		"has": {}, "had": {}, "do": {}, "does": {}, "did": {}, "will": {},
		"would": {}, "could": {}, "should": {}, "may": {}, "might": {}, "must": {},
		"shall": {}, "can": {}, "not": {}, "no": {}, "this": {}, "that": {},
		"these": {}, "those": {}, "what": {}, "which": {}, "who": {}, "whom": {},
		"whose": {}, "where": {}, "when": {}, "why": {}, "how": {}, "all": {},
		"any": {}, "both": {}, "each": {}, "every": {}, "few": {}, "more": {},
		"most": {}, "some": {}, "such": {}, "only": {}, "own": {}, "same": {},
		"than": {}, "too": {}, "very": {}, "just": {}, "now": {}, "then": {},
		"there": {}, "here": {}, "about": {}, "above": {}, "across": {}, "after": {},
		"again": {}, "against": {}, "along": {}, "already": {}, "also": {},
		"although": {}, "among": {}, "around": {}, "because": {}, "before": {},
		"behind": {}, "below": {}, "beneath": {}, "beside": {}, "between": {},
		"beyond": {}, "down": {}, "during": {}, "except": {}, "inside": {},
		"into": {}, "near": {}, "off": {}, "onto": {}, "outside": {}, "over": {},
		"since": {}, "through": {}, "throughout": {}, "toward": {}, "towards": {},
		"under": {}, "unless": {}, "until": {}, "upon": {}, "within": {}, "without": {},
	}
}

// newBuiltInLexicon creates a registry pre-populated with per-language
// profiles derived from the old hardcoded quality_gate.go maps. Each
// profile has language-specific stop words so detectLanguage works
// correctly. The fallback profile contains the union for
// language-agnostic filtering (e.g. textutil.IsStopWord).
func newBuiltInLexicon() *LexiconRegistry {
	reg := &LexiconRegistry{
		profiles: map[string]*LexiconProfile{
			"en": englishBuiltInProfile(),
			"it": italianBuiltInProfile(),
			"es": spanishBuiltInProfile(),
			"fr": frenchBuiltInProfile(),
			"de": germanBuiltInProfile(),
		},
		fallback: builtInFallbackProfile(),
	}
	reg.version = fingerprintLexiconRegistry(reg.profiles, reg.fallback)
	return reg
}

func englishBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
			"be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
			"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {},
			"should": {}, "of": {}, "in": {}, "on": {}, "at": {}, "to": {}, "for": {},
			"and": {}, "or": {}, "but": {}, "with": {}, "from": {}, "this": {},
			"not": {}, "no": {}, "it": {}, "that": {}, "i": {}, "you": {}, "he": {},
			"she": {}, "we": {}, "they": {}, "my": {}, "your": {}, "his": {}, "her": {},
			"its": {}, "our": {}, "their": {}, "what": {}, "which": {}, "who": {},
			"where": {}, "when": {}, "why": {}, "how": {}, "all": {}, "any": {},
			"both": {}, "each": {}, "few": {}, "more": {}, "most": {}, "other": {},
			"some": {}, "such": {}, "only": {}, "own": {}, "same": {}, "so": {},
			"than": {}, "too": {}, "very": {}, "just": {}, "now": {}, "then": {},
			"here": {}, "there": {}, "as": {}, "if": {},
		},
		// FunctionWords for the English profile use the comprehensive
		// built-in set (which covers sentence-start stop words, month
		// names, day names, adverbs, etc.) so entity filters like
		// isSentenceStartCapitalizedOnly work correctly.
		FunctionWords:     builtInFunctionWords(),
		EntityBlocklist:   builtInEntityBlocklist(),
		NegativeParticles: map[string]struct{}{"not": {}, "no": {}, "never": {}, "neither": {}},
		VisualVerbs: map[string]struct{}{
			"watch": {}, "observe": {}, "look": {}, "see": {}, "study": {},
			"build": {}, "construct": {}, "walk": {}, "run": {}, "jump": {},
			"move": {}, "push": {}, "pull": {}, "enter": {}, "leave": {},
		},
		VerbSuffixes: []string{"ing", "ed", "en", "es"},
		PhrasePolicy: DefaultPhraseExtractionPolicy(),
	}
}

func italianBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"il": {}, "la": {}, "lo": {}, "i": {}, "le": {}, "un": {}, "una": {},
			"di": {}, "a": {}, "da": {}, "in": {}, "con": {}, "su": {}, "per": {},
			"tra": {}, "fra": {}, "e": {}, "o": {}, "ma": {}, "se": {}, "che": {},
			"come": {}, "questo": {}, "questa": {}, "sono": {}, "è": {}, "del": {},
			"non": {}, "gli": {}, "dei": {}, "degli": {}, "della": {}, "delle": {},
			"dello": {}, "al": {}, "allo": {}, "alla": {}, "ai": {}, "agli": {}, "alle": {},
			"dal": {}, "dallo": {}, "dalla": {}, "dai": {}, "dagli": {}, "dalle": {},
			"nel": {}, "nello": {}, "nella": {}, "nei": {}, "negli": {}, "nelle": {},
			"col": {}, "coi": {}, "sul": {}, "sulla": {}, "sui": {}, "sulle": {},
			"chi": {}, "cosa": {}, "quale": {}, "quali": {}, "tutto": {}, "tutti": {},
			"ogni": {}, "qualche": {}, "molto": {}, "poco": {}, "troppo": {}, "anche": {},
			"più": {}, "meno": {}, "quando": {}, "dove": {}, "perché": {}, "si": {},
		},
		// Italian function words are also covered by the comprehensive
		// builtInFunctionWords() set, which includes both English and
		// Italian grammatical words.
		FunctionWords:     builtInFunctionWords(),
		EntityBlocklist:   map[string]struct{}{},
		NegativeParticles: map[string]struct{}{"non": {}, "no": {}, "mai": {}, "ne": {}},
		VisualVerbs: map[string]struct{}{
			"guardare": {}, "osservare": {}, "vedere": {}, "studiare": {},
			"costruire": {}, "camminare": {}, "correre": {}, "saltare": {},
			"muovere": {}, "spingere": {}, "tirare": {}, "entrare": {}, "uscire": {},
		},
		VerbSuffixes: []string{"are", "ere", "ire", "ando", "endo", "ato", "uto", "ito"},
		PhrasePolicy: DefaultPhraseExtractionPolicy(),
	}
}

func spanishBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"el": {}, "la": {}, "los": {}, "las": {}, "un": {}, "una": {}, "unos": {},
			"unas": {}, "de": {}, "a": {}, "en": {}, "con": {}, "por": {}, "para": {},
			"sobre": {}, "y": {}, "o": {}, "pero": {}, "si": {}, "que": {}, "como": {},
			"este": {}, "esta": {}, "son": {}, "es": {}, "del": {},
			"no": {}, "su": {}, "sus": {}, "me": {}, "te": {}, "se": {}, "nos": {}, "os": {},
			"le": {}, "les": {}, "lo": {}, "mi": {}, "tu": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"ar", "er", "ir", "ando", "endo"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func frenchBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"le": {}, "la": {}, "les": {}, "un": {}, "une": {}, "des": {}, "du": {},
			"de": {}, "à": {}, "en": {}, "par": {}, "pour": {}, "sur": {}, "et": {},
			"ou": {}, "mais": {}, "si": {}, "que": {}, "comme": {}, "ce": {}, "cette": {},
			"sont": {}, "est": {}, "dans": {}, "avec": {}, "sans": {}, "pas": {}, "ne": {}, "plus": {},
			"au": {}, "aux": {}, "ses": {}, "son": {}, "nous": {}, "vous": {}, "ils": {}, "elles": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"er", "ir", "re", "ant", "é", "ée"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}

func germanBuiltInProfile() *LexiconProfile {
	return &LexiconProfile{
		StopWords: map[string]struct{}{
			"der": {}, "die": {}, "das": {}, "ein": {}, "eine": {}, "einen": {},
			"von": {}, "zu": {}, "in": {}, "mit": {}, "für": {}, "auf": {}, "und": {},
			"oder": {}, "aber": {}, "wenn": {}, "dass": {}, "wie": {}, "dieser": {}, "diese": {},
			"sind": {}, "ist": {}, "nicht": {}, "dem": {}, "den": {}, "des": {},
			"einer": {}, "eines": {}, "einem": {}, "nach": {}, "aus": {}, "bei": {}, "vor": {},
			"durch": {}, "über": {}, "unter": {}, "zwischen": {}, "um": {}, "gegen": {},
			"ohne": {}, "bis": {}, "zum": {}, "zur": {}, "beim": {}, "vom": {},
		},
		FunctionWords:   nil,
		EntityBlocklist: nil,
		VerbSuffixes:    []string{"en", "t", "e", "st"},
		PhrasePolicy:    DefaultPhraseExtractionPolicy(),
	}
}
