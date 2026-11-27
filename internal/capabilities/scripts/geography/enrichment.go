// Package geography owns grounded place enrichment for script annotations.
// It accepts kernel annotations and the geocoding port, so orchestration
// remains independent of the scripts Runner and its execution lifecycle.
package geography

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	capabilitygeocoding "github.com/Marcuss-ops/PipelineGen/internal/capabilities/geocoding"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/entitytype"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type Options struct {
	SceneTexts map[int]string
	MapsOnly   bool
	// MapLevel is the cartographic level the run asked for. It is forwarded to
	// the provider so a country-level map never resolves a city, and it is
	// applied to places that arrive already geocoded by an earlier surface.
	// Empty keeps the level-agnostic lookup.
	MapLevel string
}

func IsPlaceEntityType(entityType string) bool { return entitytype.Is(entityType, entitytype.Place) }

func isPlaceEntityType(entityType string) bool { return IsPlaceEntityType(entityType) }

// enrichState is the run-scoped geography state: the positive answers already
// paid for and the lookups the requested cartographic level refused. A refusal
// is terminal for the run, so the retry frames never re-ask a provider for a
// place the map cannot use.
type enrichState struct {
	resolved map[string]capabilitygeocoding.Result
	refused_ map[string]struct{}
	// refusedNames holds the places already known to sit outside the requested
	// level (a place that arrived pre-geocoded at another scale). The level is
	// constant for the run and so is the scope of a name: neither the retry
	// frames nor the grounded-name pass may look it up again.
	refusedNames map[string]struct{}
}

// refusedName is the run-scoped identity of a name refused by the map level.
func refusedName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
}

func (s *enrichState) refuseName(name string) {
	key := refusedName(name)
	if s == nil || key == "" {
		return
	}
	if s.refusedNames == nil {
		s.refusedNames = make(map[string]struct{}, 1)
	}
	s.refusedNames[key] = struct{}{}
}

func (s *enrichState) nameRefused(name string) bool {
	if s == nil || len(s.refusedNames) == 0 {
		return false
	}
	_, ok := s.refusedNames[refusedName(name)]
	return ok
}

func (s *enrichState) refused(key string) bool {
	if s == nil || len(s.refused_) == 0 {
		return false
	}
	_, ok := s.refused_[key]
	return ok
}

func (s *enrichState) refuse(key string) {
	if s == nil {
		return
	}
	if s.refused_ == nil {
		s.refused_ = make(map[string]struct{}, 1)
	}
	s.refused_[key] = struct{}{}
}

// outOfMapLevel reports whether an already-grounded place belongs to a
// different cartographic level than the requested map. Such a place is dropped
// before the lookup: a country pin on a city frame is not a candidate the
// geocoder could improve.
func outOfMapLevel(geo *scriptpkg.GeoCoordinate, level string) bool {
	if geo == nil || level == "" {
		return false
	}
	return !capabilitygeocoding.ScopeFitsLevel(geo.Scope, level)
}
func StripPersonNameMapCandidates(annotation *scriptpkg.SceneAnnotations, sceneText string) {
	stripPersonNameMapCandidates(annotation, sceneText)
}

// geocodePlaceAnnotations enriches the prepare branch's grounded scene
// annotations with validated WGS84 coordinates after explicit or configured
// automatic activation. Stable scene and
// annotation order makes request ordering deterministic; duplicate place
// spellings in a run share one lookup and the adapter's durable cache shares
// results across jobs. An enabled policy without an adapter, or an invalid
// provider result, fails closed rather than emitting a map from guessed data.
func Enrich(ctx context.Context, geocoder capabilitygeocoding.Geocoder, annotations map[int]*scriptpkg.SceneAnnotations, language string, options ...Options) error {
	if geocoder == nil {
		return fmt.Errorf("geocoding is enabled but no geocoder adapter is wired")
	}
	indices := sortedSceneIndexes(annotations)
	request := enrichRequest{geocoder: geocoder, language: language, personTokens: personNameTokens(annotations)}
	if len(options) > 0 {
		constraint, err := capabilitygeocoding.ConstraintLevel(options[0].MapLevel)
		if err != nil {
			return fmt.Errorf("map level: %w", err)
		}
		request.level = constraint
		request.mapsOnly = options[0].MapsOnly
		request.sceneTexts = options[0].SceneTexts
		if request.mapsOnly {
			request.exclusions, err = MapCandidateExclusionsFor(linguistics.DefaultProfilesOrNil(), language)
			if err != nil {
				return fmt.Errorf("load map candidate exclusion lexicon: %w", err)
			}
		}
	}
	state := &enrichState{resolved: make(map[string]capabilitygeocoding.Result)}
	for _, index := range indices {
		annotation := annotations[index]
		if annotation == nil {
			continue
		}
		policy := request.policy(state, annotation, index)
		policy.prefilterOutOfLevel(annotation)
		// The scene's own already-grounded places are the lookup context: a
		// homonymous name is disambiguated by the country/region the scene has
		// already certified, never by invented geography. The context grows as
		// coarse places resolve, so a later city lookup sees them.
		geoContext := sceneGeoContext(annotation)
		if err := request.enrichEntities(ctx, state, policy, annotation.PrimaryEntities, index, &geoContext); err != nil {
			return err
		}
		if err := request.enrichEntities(ctx, state, policy, annotation.SecondaryEntities, index, &geoContext); err != nil {
			return err
		}
		// A scene that grounds more than one country has no single correct
		// frame: "Roma, Torino e Parigi ... Italia ... Francia" resolves Italy
		// first, so the retry of Paris under countrycodes=it fails while Rome
		// is fine under it and lost under an unconstrained lookup. The "latest
		// country" is just the last mention, not a disambiguator: retry every
		// unresolved place against each country the scene already grounded,
		// then once without a constraint. A single-country scene keeps its one
		// frame, which is the disambiguating case (Springfield under the USA).
		retryFrames := sceneLookupFrames(annotation, geoContext)
		// A city can appear before its country in a scene ("Lisbona, in
		// Portogallo"). Its first lookup has no grounded context and may
		// correctly return ErrNoResult. Retry unresolved place annotations
		// after the full pass has adopted any country/region coordinates.
		if err := request.retryEntities(ctx, state, policy, annotation.PrimaryEntities, index, retryFrames, &geoContext); err != nil {
			return err
		}
		if err := request.retryEntities(ctx, state, policy, annotation.SecondaryEntities, index, retryFrames, &geoContext); err != nil {
			return err
		}
		if request.mapsOnly {
			if err := geocodeGroundedPlaceNames(ctx, geocoder, state, annotation, request.sceneTexts[index], language, request.level, index, &geoContext, request.personTokens); err != nil {
				return err
			}
		}
	}
	return nil
}

// sceneLookupFrames lists the grounded lookup frames a retry may use, in order.
// An empty result means "keep the historical single lookup": a scene with one
// grounded country (or none) has nothing to choose between.
func sceneLookupFrames(annotation *scriptpkg.SceneAnnotations, current capabilitygeocoding.Context) []capabilitygeocoding.Context {
	codes := sceneCountryCodes(annotation)
	if len(codes) <= 1 {
		return nil
	}
	frames := make([]capabilitygeocoding.Context, 0, len(codes)+1)
	for _, code := range codes {
		frame := capabilitygeocoding.Context{}
		if len(code) == 2 {
			frame.CountryCode = code
		} else {
			frame.Country = code
		}
		if frame == current {
			continue
		}
		frames = append(frames, frame)
	}
	// Last resort: no frame at all, which is the only lookup that can resolve a
	// place whose country the scene never grounded.
	frames = append(frames, capabilitygeocoding.Context{})
	return frames
}

// geocodeWithSceneFrames retries one unresolved place through the scene's
// grounded frames and stops at the first resolution.
func geocodeWithSceneFrames(ctx context.Context, geocoder capabilitygeocoding.Geocoder, state *enrichState, entity *scriptpkg.AnnotatedEntity, sceneIndex int, language, level string, frames []capabilitygeocoding.Context, geoContext *capabilitygeocoding.Context) error {
	if len(frames) == 0 {
		return geocodeAnnotatedEntity(ctx, geocoder, state, entity, sceneIndex, language, level, geoContext)
	}
	for _, frame := range frames {
		attempt := frame
		if err := geocodeAnnotatedEntity(ctx, geocoder, state, entity, sceneIndex, language, level, &attempt); err != nil {
			return err
		}
		if entity.Geo != nil {
			AdoptGeoContext(geoContext, entity)
			return nil
		}
	}
	return nil
}

// sceneCountryCodes returns the distinct countries the scene has already
// grounded. Codes are preferred because they are language independent; a hit
// without an ISO code still counts through its country name so a provider that
// omits the code does not hide a multi-country scene.
func sceneCountryCodes(annotation *scriptpkg.SceneAnnotations) []string {
	if annotation == nil {
		return nil
	}
	seen := make(map[string]struct{})
	codes := make([]string, 0, 2)
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if entity.Geo == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(entity.Geo.CountryCode))
		if key == "" {
			key = strings.ToLower(strings.Join(strings.Fields(entity.Geo.Country), " "))
		}
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		codes = append(codes, key)
	}
	return codes
}

// personNameTokens captures grounded people across the whole script. A
// location tag for a surname in a later scene ("Washington filed a
// complaint") must not become Washington, DC when the person was identified
// as Desiree Washington in an earlier scene.
func personNameTokens(annotations map[int]*scriptpkg.SceneAnnotations) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, annotation := range annotations {
		if annotation == nil {
			continue
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
			if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
				continue
			}
			for _, part := range strings.Fields(strings.ToLower(strings.TrimSpace(entity.CanonicalName))) {
				if part != "" {
					tokens[part] = struct{}{}
				}
			}
		}
	}
	return tokens
}

func personNameToken(tokens map[string]struct{}, candidate string) bool {
	name := strings.ToLower(strings.TrimSpace(candidate))
	if name == "" || strings.Contains(name, " ") {
		return false
	}
	_, ok := tokens[name]
	return ok
}

// sceneGeoContext derives the lookup context from the scene's own grounded
// places: the country and region names already carrying certified coordinates
// in this annotation. Nothing is inferred or invented — an entity without Geo
// contributes no hint.
func sceneGeoContext(annotation *scriptpkg.SceneAnnotations) capabilitygeocoding.Context {
	var ctx capabilitygeocoding.Context
	if annotation == nil {
		return ctx
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if entity.Geo == nil {
			continue
		}
		// A grounded city also grounds its country: use the provider's
		// addressed country as context for the namesakes that follow, even
		// when the scene never spelled the country out.
		if ctx.CountryCode == "" && strings.TrimSpace(entity.Geo.CountryCode) != "" {
			ctx.CountryCode = strings.ToLower(strings.TrimSpace(entity.Geo.CountryCode))
		}
		if ctx.Country == "" && strings.TrimSpace(entity.Geo.Country) != "" {
			ctx.Country = strings.TrimSpace(entity.Geo.Country)
		}
		if ctx.Region == "" && strings.TrimSpace(entity.Geo.AdminRegion) != "" {
			ctx.Region = strings.TrimSpace(entity.Geo.AdminRegion)
		}
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(entity.Geo.Scope)) {
		case "country":
			if strings.TrimSpace(ctx.Country) == "" {
				ctx.Country = name
			}
		case "region":
			if strings.TrimSpace(ctx.Region) == "" {
				ctx.Region = name
			}
		}
	}
	return ctx
}

// adoptGeoContext lets a freshly resolved coarse place (country/region)
// disambiguate the finer lookups that follow it in the same scene.
func AdoptGeoContext(ctx *capabilitygeocoding.Context, entity *scriptpkg.AnnotatedEntity) {
	if ctx == nil || entity == nil || entity.Geo == nil {
		return
	}
	// Any resolved place carries a grounded country. Adopt the LATEST one:
	// a scene can name several countries ("Italia, poi la Francia"), and
	// keeping the first would constrain every later lookup to the wrong
	// country. A country change also invalidates the previous region.
	country := strings.TrimSpace(entity.Geo.Country)
	countryCode := strings.ToLower(strings.TrimSpace(entity.Geo.CountryCode))
	countryChanged := country != "" && ctx.Country != "" && !strings.EqualFold(strings.TrimSpace(ctx.Country), country)
	if ctx.CountryCode != "" && countryCode != "" && ctx.CountryCode != countryCode {
		countryChanged = true
	}
	if countryChanged {
		ctx.Region = ""
	}
	if country != "" {
		ctx.Country = country
	}
	if countryCode != "" {
		ctx.CountryCode = countryCode
	}
	if region := strings.TrimSpace(entity.Geo.AdminRegion); region != "" {
		ctx.Region = region
	}
	name := strings.TrimSpace(entity.CanonicalName)
	if name == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(entity.Geo.Scope)) {
	case "country":
		// A country result is the frame for the finer names that follow.
		if country == "" {
			ctx.Country = name
		}
		ctx.Region = ""
	case "region":
		ctx.Region = name
	}
}

// geocodeCacheKey identifies one lookup INCLUDING its grounded context: the
// same name under two different contexts must never share one cached answer.
func GeocodeCacheKey(language, name string, geoContext capabilitygeocoding.Context) string {
	key := strings.ToLower(strings.TrimSpace(language)) + "\x00" + strings.ToLower(name)
	if !geoContext.IsZero() {
		key += "\x00" + geoContext.Key()
	}
	return key
}

// capitalizedNameFragment rejects a one-token location annotation that is
// actually one part of a grounded multi-token proper name in the scene. It
// covers cases where entity extraction omitted the corresponding PERSON row.
func capitalizedNameFragment(sceneText, candidate string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" || strings.Contains(candidate, " ") {
		return false
	}
	for _, span := range capitalizedNameRE.FindAllString(sceneText, -1) {
		parts := strings.Fields(strings.ToLower(strings.TrimSpace(span)))
		// A leading article is not part of a proper name. Without this,
		// "La Toscana" looks like a two-token name and the correct single-token
		// annotation "Toscana" is discarded as if it were a person-name
		// fragment (e.g. "Chiara" from "Chiara Poggi").
		if len(parts) >= 2 && isCapitalizedNameArticle(parts[0]) {
			parts = parts[1:]
		}
		if len(parts) < 2 {
			continue
		}
		for _, part := range parts {
			if part == candidate {
				return true
			}
		}
	}
	return false
}

func isCapitalizedNameArticle(part string) bool {
	switch part {
	case "il", "lo", "la", "i", "gli", "le", "l'", "l’", "un", "uno", "una", "un'", "un’",
		"the", "a", "an", "el", "los", "las", "les", "der", "die", "das":
		return true
	default:
		return false
	}
}

func stripPersonNameMapCandidates(annotation *scriptpkg.SceneAnnotations, sceneText string) {
	if annotation == nil {
		return
	}
	strip := func(entities []scriptpkg.AnnotatedEntity) {
		for index := range entities {
			entity := &entities[index]
			if !isPlaceEntityType(entity.Type) {
				continue
			}
			name := strings.TrimSpace(entity.CanonicalName)
			if personNameFragment(annotation, name) || capitalizedNameFragment(sceneText, name) || (len(name) > 1 && name == strings.ToUpper(name)) {
				entity.Geo = nil
			}
		}
	}
	strip(annotation.PrimaryEntities)
	strip(annotation.SecondaryEntities)
}

// personNameFragment rejects only single-token candidate places that are
// already grounded as part of a multi-token PERSON name in this scene.
func personNameFragment(annotation *scriptpkg.SceneAnnotations, candidate string) bool {
	if annotation == nil {
		return false
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	}
	name := normalize(candidate)
	if name == "" || strings.Contains(name, " ") {
		return false
	}
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		if !strings.EqualFold(strings.TrimSpace(entity.Type), "PERSON") {
			continue
		}
		for _, part := range strings.Fields(normalize(entity.CanonicalName)) {
			if part == name {
				return true
			}
		}
	}
	return false
}

// containsGroundedPlaceName prevents a semantic annotation or a geocoder
// homonym from creating a map stop for a name that the scene never says.
// Compare Unicode letters/digits while treating punctuation and spacing as
// separators, so possessives and sentence punctuation remain harmless.
func containsGroundedPlaceName(sceneText, candidate string) bool {
	normalize := func(s string) string {
		var b strings.Builder
		space := true
		for _, r := range s {
			r = unicode.ToLower(r)
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 'à' && r <= 'ö') || (r >= 'ø' && r <= 'ÿ') {
				b.WriteRune(r)
				space = false
			} else if !space {
				b.WriteByte(' ')
				space = true
			}
		}
		return strings.TrimSpace(b.String())
	}
	needle := normalize(candidate)
	if needle == "" {
		return false
	}
	text := " " + normalize(sceneText) + " "
	if !strings.Contains(text, " "+needle+" ") {
		return false
	}
	// Proper place names are case-sensitive evidence here: a lowercase verb
	// such as "vigevano" must not become the city Vigevano by homonym lookup.
	if !strings.Contains(sceneText, candidate) {
		return false
	}
	// These temporal words are frequently mislabeled as Italian place names;
	// Nominatim can return tiny homonymous localities for them.
	if strings.EqualFold(needle, "anno") || strings.EqualFold(needle, "anni") {
		return false
	}
	return true
}

var capitalizedNameRE = regexp.MustCompile(`(?:[A-ZÀ-ÖØ-Þ][\p{L}\p{M}'’’-]*)(?:[ \t]+[A-ZÀ-ÖØ-Þ][\p{L}\p{M}'’’-]*){0,3}`)

// geocodeGroundedPlaceNames recovers exact proper-name spans that the semantic
// extractor can mislabel as people or omit (for example, a town named in a
// journey narration). It runs only for an explicit maps-only request and only
// accepts names that the configured geocoder classifies at a geographic level.
func GeocodeGroundedPlaceNames(ctx context.Context, geocoder capabilitygeocoding.Geocoder, resolved map[string]capabilitygeocoding.Result, annotation *scriptpkg.SceneAnnotations, text, language string, sceneIndex int, geoContext *capabilitygeocoding.Context) error {
	return geocodeGroundedPlaceNames(ctx, geocoder, &enrichState{resolved: resolved}, annotation, text, language, "", sceneIndex, geoContext, nil)
}

func geocodeGroundedPlaceNames(ctx context.Context, geocoder capabilitygeocoding.Geocoder, state *enrichState, annotation *scriptpkg.SceneAnnotations, text, language, level string, sceneIndex int, geoContext *capabilitygeocoding.Context, personTokens map[string]struct{}) error {
	if annotation == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	exclusions, err := MapCandidateExclusionsFor(linguistics.DefaultProfilesOrNil(), language)
	if err != nil {
		return fmt.Errorf("load map candidate exclusion lexicon: %w", err)
	}
	ignored := exclusions.Words
	seen := make(map[string]struct{})
	entityIndexes := make(map[string]int)
	for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), annotation.PrimaryEntities...), annotation.SecondaryEntities...) {
		canonical := strings.ToLower(strings.TrimSpace(entity.CanonicalName))
		// Keep unresolved named places eligible for a context-aware retry below:
		// their first annotation lookup may have run before the country entity.
		if entity.Geo != nil || !isPlaceEntityType(entity.Type) || isGenericPlaceLabel(entity.CanonicalName, language) {
			seen[canonical] = struct{}{}
			seen[strings.ToLower(normalizeGeocodePlaceQuery(entity.CanonicalName, language))] = struct{}{}
		}
		if entity.Geo != nil && (entity.Geo.Scope == "continent" || entity.Geo.Scope == "country" || entity.Geo.Scope == "region" || entity.Geo.Scope == "city") {
			seen[canonical] = struct{}{}
		}
	}
	for index, entity := range annotation.PrimaryEntities {
		entityIndexes[strings.ToLower(normalizeGeocodePlaceQuery(entity.CanonicalName, language))] = index
	}
	queries := make([]string, 0, 8)
	for _, span := range capitalizedNameRE.FindAllStringIndex(text, -1) {
		match := text[span[0]:span[1]]
		name := normalizeGeocodePlaceQuery(match, language)
		key := strings.ToLower(name)
		if name == "" || isGenericPlaceLabel(name, language) {
			continue
		}
		if state.nameRefused(name) {
			continue
		}
		if _, ok := ignored[key]; ok {
			continue
		}
		// Sentence-start capitalized prose is not a place name. Multiword
		// proper names and single words after geographic cues remain eligible.
		if len(strings.Fields(name)) == 1 && len(name) < 4 {
			continue
		}
		// Capitalization alone is weak evidence: sentence-initial adverbs
		// ("Infine") and uppercase organizations ("ANSA") must not become
		// map pins merely because a geocoder happens to know a homonymous town.
		if len(name) > 1 && name == strings.ToUpper(name) {
			continue
		}
		if IgnoredMapCandidate(exclusions, name, language) || groundedPersonName(annotation, name) || personNameToken(personTokens, name) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, name)
		if len(queries) >= 8 {
			break
		}
	}
	for _, name := range queries {
		lookupContext := placeLookupContext(name, language, geoContext)
		cacheKey := GeocodeCacheKey(language, name, lookupContext)
		if state.refused(cacheKey) {
			continue
		}
		result, ok := state.resolved[cacheKey]
		if !ok {
			var err error
			result, err = geocoder.Geocode(ctx, capabilitygeocoding.Request{Query: name, Language: language, Context: lookupContext, Level: level})
			if errors.Is(err, capabilitygeocoding.ErrNoResult) {
				// The same normalized span appears in every scene that names the
				// place; refuse the key for the rest of the run instead of asking
				// the provider once per scene.
				state.refuse(cacheKey)
				continue
			}
			if err != nil {
				return fmt.Errorf("geocode named map candidate %q in scene %d: %w", name, sceneIndex, err)
			}
			if err := result.Validate(); err != nil {
				return fmt.Errorf("geocode named map candidate %q returned invalid coordinates: %w", name, err)
			}
			state.resolved[cacheKey] = result
		}
		if result.Scope != "continent" && result.Scope != "country" && result.Scope != "region" && result.Scope != "city" {
			continue
		}
		// The requested level decides what this map may pin. A provider that
		// ignores the hint (or a cached answer from before the level existed)
		// must not slip a city onto a country-level plate here either.
		if !capabilitygeocoding.ScopeFitsLevel(result.Scope, level) {
			state.refuse(cacheKey)
			continue
		}
		geo := &scriptpkg.GeoCoordinate{
			Latitude: result.Latitude, Longitude: result.Longitude,
			DisplayName: result.DisplayName, Scope: result.Scope,
			Country: result.Country, CountryCode: result.CountryCode,
			AdminRegion: result.AdminRegion,
		}
		if index, exists := entityIndexes[strings.ToLower(name)]; exists {
			annotation.PrimaryEntities[index].Geo = geo
			annotation.PrimaryEntities[index].CanonicalName = name
			annotation.PrimaryEntities[index].Text = name
			annotation.PrimaryEntities[index].Type = "LOCATION"
			continue
		}
		annotation.PrimaryEntities = append(annotation.PrimaryEntities, scriptpkg.AnnotatedEntity{
			ID:   "map-place-" + strings.ToLower(strings.ReplaceAll(strings.Fields(name)[0], "'", "")),
			Text: name, CanonicalName: name, Type: "LOCATION", Confidence: 0.85,
			Geo: geo,
		})
	}
	// Keep every grounded scope. A city, its region and its country are
	// distinct map candidates; collapsing them here silently lost regional
	// maps such as "La Toscana" whenever the same scene named a city. The
	// planner groups candidates by certified plate and the run budget bounds
	// how many maps are admitted.
	return nil
}
