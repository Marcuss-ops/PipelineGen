package audio

import "strings"

// BuiltInAssetAliases are the stable asset names exposed by the generation
// payload. The values are the canonical asset IDs used by the media registry
// after `admin index-provided-sound-effects` has seeded the catalog.
//
// Keep these names independent from Drive filenames: callers should be able
// to select an audio cue without knowing where it is stored or how it was
// originally named.
var BuiltInAssetAliases = map[string]string{
	"whop1":   "1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX",
	"whop2":   "1hHMV6dc4yC2EsC5nTBg3mgqOtUAgw9t2",
	"whop3":   "1P1CbjRkOjPXxZR9reAwijtP-W9wXY5kC",
	"whop4":   "1rZmroLS1ec9A7xswJvQl8HnRhZfFbT_L",
	"whop5":   "127ZLnNn-4iL0TcDtjOVOWefJASUoqXfY",
	"whop6":   "1joPGUccrhAxJq1-LyFNp27xDuCjPwZhK",
	"whoop1":  "1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX",
	"whoop2":  "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq",
	"whoop3":  "1riijLdDzpL9yXhT-RX-OrRVD67jagq8D",
	"whoop4":  "1fi2huRNuHFzNyvie8SajoZMdw27wl5ke",
	"whoosh1": "1T7TJuqrwtvR3se1nlvY2k19lA5zAOODs",
	"whoosh2": "1NQyz3d5JPcLrA6NtM2TMIKdmlTKqepNg",
	"whoosh3": "1rNnmb3if98M3aSpj2O9EtuvSNJ4AdSen",
	"whoosh4": "1hHMV6dc4yC2EsC5nTBg3mgqOtUAgw9t2",
	"whoosh5": "1joPGUccrhAxJq1-LyFNp27xDuCjPwZhK",
	"whoosh6": "127ZLnNn-4iL0TcDtjOVOWefJASUoqXfY",
	"whoosh7": "1rZmroLS1ec9A7xswJvQl8HnRhZfFbT_L",
	"whoosh8": "1P1CbjRkOjPXxZR9reAwijtP-W9wXY5kC",
	"whoosh9": "1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX",
	"bgm1":    "1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX",
	"bgm2":    "1riijLdDzpL9yXhT-RX-OrRVD67jagq8D",
	"bgm3":    "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq",
	"bgm4":    "1fi2huRNuHFzNyvie8SajoZMdw27wl5ke",
	"bgm5":    "1lEqAxjNWFXe3UpKNOpJrA2EU9izLPML2",
	"bgm6":    "1OmVstjygP2SsX7748ylyzGDdmYxcrE8C",
}

// CanonicalAssetID resolves a public payload alias. Unknown IDs are already
// canonical registry IDs and pass through unchanged.
func CanonicalAssetID(id string) string {
	id = strings.TrimSpace(id)
	if canonical, ok := BuiltInAssetAliases[strings.ToLower(id)]; ok {
		return canonical
	}
	return id
}

func IsBuiltInBGM(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "bgm1", "bgm2", "bgm3", "bgm4", "bgm5", "bgm6":
		return true
	default:
		return false
	}
}

func IsBuiltInWhoop(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "whop1", "whop2", "whop3", "whop4", "whop5", "whop6":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "whoop1", "whoop2", "whoop3", "whoop4":
		return true
	default:
		return false
	}
}

func IsBuiltInWhoosh(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "random_whoosh" {
		return true
	}
	switch id {
	case "whoosh1", "whoosh2", "whoosh3", "whoosh4", "whoosh5", "whoosh6", "whoosh7", "whoosh8", "whoosh9":
		return true
	default:
		return false
	}
}
