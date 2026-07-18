package asset

import "strings"

// SoundEffectTaxonomy is the semantic routing metadata used to select an SFX
// for a scene. The legacy catalog only stored one broad family; these fields
// keep family and intent separate so selection does not depend on filenames.
type SoundEffectTaxonomy struct {
	Family  string
	Subtype string
	Mood    string
	Energy  string
	BestFor []string
	Tags    []string
}

// ClassifySoundEffect derives stable semantic labels from the curated family,
// filename and existing tags. It is deterministic and safe for old catalog
// rows that have no semantic metadata yet.
func ClassifySoundEffect(name, family string, tags []string) SoundEffectTaxonomy {
	text := strings.ToLower(strings.Join(append([]string{name, family}, tags...), " "))
	family = strings.TrimSpace(strings.ToLower(family))
	if family == "" || family == "file" {
		switch {
		case hasAny(text, "music", "track", "background"):
			family = "music"
		case hasAny(text, "whoosh", "woosh", "swoosh", "swish", "suction", "swing"):
			family = "whoosh"
		case hasAny(text, "transition", "rewind", "warp", "sweep", "zoom"):
			family = "transition"
		case hasAny(text, "impact", "hit", "boom", "drop", "thud", "crash", "slam"):
			family = "impact"
		case hasAny(text, "click", "ding", "beep", "notification", "iphone", "censor", "wrong", "cash"):
			family = "ui"
		case hasAny(text, "clock", "tick", "suspense", "tension"):
			family = "tension"
		case hasAny(text, "fart", "cartoon", "minecraft", "hurt"):
			family = "cartoon"
		case hasAny(text, "keyboard", "writing", "camera", "shutter", "paper", "gun", "bike"):
			family = "foley"
		default:
			family = "misc"
		}
	}

	t := SoundEffectTaxonomy{Family: family, Energy: "medium"}
	switch {
	case hasAny(text, "suction", "reverse", "rewind"):
		t.Subtype, t.Mood, t.Energy = "suction_reverse", "anticipation", "medium"
	case hasAny(text, "fast", "arrow", "swish", "swing", "swoosh") && (family == "whoosh" || family == "transition"):
		t.Subtype, t.Mood, t.Energy = "fast_swipe", "action", "high"
	case hasAny(text, "watery", "water", "underwater", "puddle", "ocean", "wet") && family == "impact":
		t.Subtype, t.Mood, t.Energy = "watery_sub_drop", "dark", "high"
	case hasAny(text, "bass", "boom", "grand", "explosion", "sub") && family == "impact":
		t.Subtype, t.Mood, t.Energy = "cinematic_boom", "dramatic", "high"
	case hasAny(text, "zoom", "sweep", "lens", "camera") && family == "transition":
		t.Subtype, t.Mood, t.Energy = "camera_sweep", "motion", "medium"
	case hasAny(text, "metal", "slam", "mechanical"):
		t.Subtype, t.Mood, t.Energy = "metal_mechanical", "industrial", "high"
	case hasAny(text, "click", "beep", "ding", "notification", "cash", "iphone"):
		t.Subtype, t.Mood, t.Energy = "notification_click", "clean", "low"
	case hasAny(text, "pop", "bubble"):
		t.Subtype, t.Mood, t.Energy = "pop_bubble", "playful", "low"
	case hasAny(text, "clock", "tick"):
		t.Subtype, t.Mood, t.Energy = "countdown_tick", "suspense", "medium"
	case hasAny(text, "fart", "cartoon", "minecraft", "hurt"):
		t.Subtype, t.Mood, t.Energy = "comic_reaction", "comedic", "medium"
	case hasAny(text, "camera", "shutter"):
		t.Subtype, t.Mood, t.Energy = "camera_shutter", "documentary", "low"
	case hasAny(text, "keyboard", "writing", "paper"):
		t.Subtype, t.Mood, t.Energy = "handling_typing", "neutral", "low"
	case family == "glitch":
		t.Subtype, t.Mood, t.Energy = "digital_glitch", "technical", "medium"
	case family == "gaming":
		t.Subtype, t.Mood, t.Energy = "game_ui", "playful", "low"
	case family == "industrial" || family == "mechanical":
		t.Subtype, t.Mood, t.Energy = "industrial_machine", "industrial", "medium"
	default:
		t.Subtype, t.Mood = family, "cinematic"
	}

	switch family {
	case "whoosh", "transition":
		t.BestFor = []string{"cut", "reveal", "rank_change", "motion"}
	case "impact":
		t.BestFor = []string{"reveal", "predator", "dramatic_hit", "rank_change"}
	case "ui":
		t.BestFor = []string{"label", "counter", "notification", "micro_accent"}
	case "tension", "riser", "ambient":
		t.BestFor = []string{"build_up", "suspense", "countdown"}
	case "cartoon":
		t.BestFor = []string{"comedy", "reaction", "meme"}
	case "foley":
		t.BestFor = []string{"action_match", "realism", "texture"}
	case "music", "background_music":
		t.BestFor = []string{"background", "montage", "mood"}
	default:
		t.BestFor = []string{"accent", "transition"}
	}
	t.Tags = uniqueStrings(append(append([]string{}, tags...), t.Family, t.Subtype, t.Mood, t.Energy))
	return t
}

func hasAny(text string, terms ...string) bool {
	text = strings.ToLower(text)
	text = strings.NewReplacer("_", " ", "-", " ", ".", " ", "/", " ").Replace(text)
	words := make(map[string]struct{})
	for _, word := range strings.Fields(text) {
		words[word] = struct{}{}
	}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if strings.Contains(term, " ") && strings.Contains(text, term) {
			return true
		}
		if _, ok := words[term]; ok {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
