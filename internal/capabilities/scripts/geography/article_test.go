package geography

import "testing"

func TestNormalizeGeocodePlaceQueryStripsKnownCountryArticle(t *testing.T) {
	cases := map[string]string{
		"L'Italia":    "Italia",
		"L’Italia":    "Italia",
		"La Francia":  "Francia",
		"La Germania": "Germania",
		"La Svizzera": "Svizzera",
		"La Toscana":  "Toscana",
	}
	for input, want := range cases {
		if got := normalizeGeocodePlaceQuery(input, "it"); got != want {
			t.Errorf("normalizeGeocodePlaceQuery(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeGeocodePlaceQueryKeepsOfficialArticleNames(t *testing.T) {
	for _, input := range []string{"L'Aquila", "La Spezia", "La Paz"} {
		if got := normalizeGeocodePlaceQuery(input, "it"); got != input {
			t.Errorf("normalizeGeocodePlaceQuery(%q) = %q, want unchanged", input, got)
		}
	}
}
