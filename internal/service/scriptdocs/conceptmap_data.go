package scriptdocs

// ConceptMap defines the core categories. 
// Synonyms and variations are handled by the LLM Director and Semantic Scorer.
var conceptMap = []ClipConcept{
	{[]string{"technology"}, "technology", 0.80},
	{[]string{"business"}, "business", 0.75},
	{[]string{"gym"}, "gym", 0.70},
	{[]string{"boxing"}, "boxing", 0.80},
	{[]string{"nature"}, "nature", 0.82},
	{[]string{"mountain"}, "mountain", 0.80},
	{[]string{"city"}, "city", 0.85},
	{[]string{"ocean"}, "ocean", 0.83},
	{[]string{"people"}, "people", 0.50},
	{[]string{"science"}, "science", 0.78},
	{[]string{"car"}, "car", 0.75},
	{[]string{"cooking"}, "cooking", 0.70},
	{[]string{"travel"}, "travel", 0.70},
	{[]string{"shopping"}, "shopping", 0.65},
	{[]string{"nightlife"}, "nightlife", 0.60},
}
