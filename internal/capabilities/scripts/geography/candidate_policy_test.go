package geography

import "testing"

func TestNormalizeGeocodePlaceQueryStripsOnlyKnownItalianRegionArticles(t *testing.T) {
	cases := []struct {
		in, language, want string
	}{
		{"La Toscana", "it", "Toscana"},
		{"O Brasil", "pt", "Brasil"},
		{"O Porto", "pt", "O Porto"},
		{"La Lombardia", "it", "Lombardia"},
		{"Il Lazio", "it", "Lazio"},
		{"Le Marche", "it", "Marche"},
		{"L'Umbria", "it", "Umbria"},
		{"La Valle d'Aosta", "it", "Valle d'Aosta"},
		{"La Spezia", "it", "La Spezia"},
		{"L'Aquila", "it", "L'Aquila"},
		{"La Toscana", "en", "La Toscana"},
	}
	for _, tc := range cases {
		if got := normalizeGeocodePlaceQuery(tc.in, tc.language); got != tc.want {
			t.Fatalf("normalizeGeocodePlaceQuery(%q,%q) = %q, want %q", tc.in, tc.language, got, tc.want)
		}
	}
}
