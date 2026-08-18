// Package scripts — identity_resolver.go provides data-driven subject
// identity resolution. It replaces the hardcoded diacritics switch in
// research_subject_filter.go with a registry of known identities, each
// carrying aliases, required context terms, and excluded terms for
// disambiguation (e.g. Floyd Mayweather vs George Floyd).
//
// correctness fix (August 2026): multi-provider research fallback requires
// a central identity resolver that all providers and the subject filter
// share. Registered in package_hotspots.json under the application
// use-case migration owner.
package usecase

import (
	"strings"
	"unicode"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"golang.org/x/text/unicode/norm"
)

// SubjectIdentityResolver resolves a candidate name to its canonical
// SubjectIdentity. Unknown names get an auto-generated identity with
// the candidate as canonical name and boxing-related required terms.
type SubjectIdentityResolver struct {
	identities []scriptpkg.SubjectIdentity
}

// NewSubjectIdentityResolver creates a resolver with the default
// boxing identity registry.
func NewSubjectIdentityResolver() *SubjectIdentityResolver {
	return &SubjectIdentityResolver{identities: defaultBoxerIdentities()}
}

// Resolve returns the canonical identity for a candidate name. If the
// name matches a known identity (by canonical name or alias), that
// identity is returned. Otherwise a fallback identity is generated with
// the input as canonical name.
func (r *SubjectIdentityResolver) Resolve(subject string) scriptpkg.SubjectIdentity {
	if r == nil {
		return fallbackIdentity(subject)
	}
	normalized := normalizeIdentityKey(subject)
	for _, id := range r.identities {
		if normalizeIdentityKey(id.CanonicalName) == normalized {
			return id
		}
		for _, alias := range id.Aliases {
			if normalizeIdentityKey(alias) == normalized {
				return id
			}
		}
	}
	return fallbackIdentity(subject)
}

func fallbackIdentity(subject string) scriptpkg.SubjectIdentity {
	canonical := strings.TrimSpace(subject)
	if canonical == "" {
		canonical = subject
	}
	return scriptpkg.SubjectIdentity{
		ID:            slugIdentityID(canonical),
		CanonicalName: canonical,
		RequiredTerms: []string{"boxing", "boxer", "fight"},
		SubjectType:   "person",
	}
}

func normalizeIdentityKey(s string) string {
	s = norm.NFD.String(strings.ToLower(strings.TrimSpace(s)))
	var result []rune
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			result = append(result, r)
		}
	}
	return strings.Join(strings.Fields(string(result)), " ")
}

func slugIdentityID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var result []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == ' ' {
			if r == ' ' {
				result = append(result, '-')
			} else {
				result = append(result, r)
			}
		}
	}
	return strings.TrimRight(string(result), "-")
}

// defaultBoxerIdentities returns the canonical identity registry for
// known boxing subjects. This is the SSOT for alias mapping — all
// hardcoded diacritics patches in research_subject_filter.go should
// migrate here.
func defaultBoxerIdentities() []scriptpkg.SubjectIdentity {
	return []scriptpkg.SubjectIdentity{
		{
			ID:            "floyd-mayweather-jr",
			CanonicalName: "Floyd Mayweather Jr.",
			Aliases:       []string{"Floyd Mayweather", "Floyd Joy Mayweather Jr.", "Money Mayweather", "Pretty Boy"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "mayweather"},
			ExcludedTerms: []string{"george floyd"},
			SubjectType:   "person",
		},
		{
			ID:            "canelo-alvarez",
			CanonicalName: "Canelo Álvarez",
			Aliases:       []string{"Canelo Alvarez", "Saul Alvarez", "Saul Álvarez", "Santos Saúl Álvarez Barragán"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "canelo", "alvarez"},
			SubjectType:   "person",
		},
		{
			ID:            "roberto-duran",
			CanonicalName: "Roberto Durán",
			Aliases:       []string{"Roberto Duran", "Manos de Piedra"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "duran", "panama"},
			SubjectType:   "person",
		},
		{
			ID:            "joe-frazier",
			CanonicalName: "Smokin' Joe Frazier",
			Aliases:       []string{"Joe Frazier", "Joseph Frazier"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "frazier"},
			SubjectType:   "person",
		},
		{
			ID:            "mike-tyson",
			CanonicalName: "Mike Tyson",
			Aliases:       []string{"Michael Tyson", "Iron Mike", "Kid Dynamite"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "tyson"},
			SubjectType:   "person",
		},
		{
			ID:            "manny-pacquiao",
			CanonicalName: "Manny Pacquiao",
			Aliases:       []string{"Emmanuel Pacquiao", "PacMan"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "pacquiao"},
			SubjectType:   "person",
		},
		{
			ID:            "sugar-ray-leonard",
			CanonicalName: "Sugar Ray Leonard",
			Aliases:       []string{"Ray Leonard"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "leonard"},
			SubjectType:   "person",
		},
		{
			ID:            "marvin-hagler",
			CanonicalName: "Marvin Hagler",
			Aliases:       []string{"Marvelous Marvin Hagler", "Marvelous Marvin Hagler"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "hagler"},
			SubjectType:   "person",
		},
		{
			ID:            "thomas-hearns",
			CanonicalName: "Thomas Hearns",
			Aliases:       []string{"Tommy Hearns", "The Hitman"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "hearns"},
			SubjectType:   "person",
		},
		{
			ID:            "oscar-de-la-hoya",
			CanonicalName: "Oscar De La Hoya",
			Aliases:       []string{"Oscar de la Hoya", "The Golden Boy"},
			RequiredTerms: []string{"boxing", "boxer", "fight", "hoya", "de la hoya"},
			SubjectType:   "person",
		},
	}
}
