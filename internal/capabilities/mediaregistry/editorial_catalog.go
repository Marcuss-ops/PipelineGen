package mediaregistry

import (
	"fmt"
	"strings"
)

// EditorialAudioAsset is the canonical identity and editorial metadata for a
// supplied BGM/SFX asset. Drive and job manifests are projections of this
// registry; they must not maintain a second alias-to-file mapping.
type EditorialAudioAsset struct {
	Alias       string
	DriveFileID string
	Name        string
	Filename    string
	Family      string
	Subtype     string
	Mood        string
	Energy      string
	BestFor     []string
	Tags        []string
}

var editorialAudioAssets = []EditorialAudioAsset{
	{Alias: "bgm1", DriveFileID: "1X4-wfIwrR51eDxIegciuBAJzKSdP3gcX", Name: "type beat", Filename: "bgm1.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm1", "bgm", "background_music"}},
	{Alias: "bgm2", DriveFileID: "1riijLdDzpL9yXhT-RX-OrRVD67jagq8D", Name: "Type Beat Rap 2", Filename: "bgm2.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm2", "bgm", "background_music"}},
	{Alias: "bgm3", DriveFileID: "1BiVWCTGOLnaeLmg8lTSSuDzo_gWWz0jq", Name: "HipHopSlowed", Filename: "bgm3.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm3", "bgm", "background_music"}},
	{Alias: "bgm4", DriveFileID: "1fi2huRNuHFzNyvie8SajoZMdw27wl5ke", Name: "Chilll Beat", Filename: "bgm4.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm4", "bgm", "background_music"}},
	{Alias: "bgm5", DriveFileID: "1lEqAxjNWFXe3UpKNOpJrA2EU9izLPML2", Name: "Type Beat Rap 3", Filename: "bgm5.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm5", "bgm", "background_music"}},
	{Alias: "bgm6", DriveFileID: "1OmVstjygP2SsX7748ylyzGDdmYxcrE8C", Name: "Chill Beat 2", Filename: "bgm6.mp3", Family: "music", Subtype: "background_music", Mood: "neutral", Energy: "medium", BestFor: []string{"background", "music", "loop"}, Tags: []string{"bgm6", "bgm", "background_music"}},
	{Alias: "whop1", DriveFileID: "1Fgr2jWQC1G6EHo-jhBAwjGtdcZo1PfaX", Name: "Whop 1", Filename: "whop1.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop1", "whop", "transition"}},
	{Alias: "whop2", DriveFileID: "1hHMV6dc4yC2EsC5nTBg3mgqOtUAgw9t2", Name: "Whop 2", Filename: "whop2.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop2", "whop", "transition"}},
	{Alias: "whop3", DriveFileID: "1P1CbjRkOjPXxZR9reAwijtP-W9wXY5kC", Name: "Whop 3", Filename: "whop3.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop3", "whop", "transition"}},
	{Alias: "whop4", DriveFileID: "1rZmroLS1ec9A7xswJvQl8HnRhZfFbT_L", Name: "Whop 4", Filename: "whop4.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop4", "whop", "transition"}},
	{Alias: "whop5", DriveFileID: "127ZLnNn-4iL0TcDtjOVOWefJASUoqXfY", Name: "Whop 5", Filename: "whop5.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop5", "whop", "transition"}},
	{Alias: "whop6", DriveFileID: "1joPGUccrhAxJq1-LyFNp27xDuCjPwZhK", Name: "Whop 6", Filename: "whop6.mp3", Family: "transition", Subtype: "whop", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whop"}, Tags: []string{"whop6", "whop", "transition"}},
	// whoosh1..whoosh3 are the BOUND members of the whoosh family. Binding
	// them here is what makes the `random_whoosh` directive resolvable: the
	// resolver expands the directive to a family alias, the alias maps to this
	// Drive identity, and the media registry holds the asset under that id.
	// whoosh4..whoosh9 have no recorded Drive identity and stay compatibility
	// vocabulary only (see kernel/script/builtin_audio_aliases.go).
	{Alias: "whoosh1", DriveFileID: "1T7TJuqrwtvR3se1nlvY2k19lA5zAOODs", Name: "Whoosh 1", Filename: "whoosh1.mp3", Family: "transition", Subtype: "whoosh", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whoosh"}, Tags: []string{"whoosh1", "whoosh", "transition"}},
	{Alias: "whoosh2", DriveFileID: "1NQyz3d5JPcLrA6NtM2TMIKdmlTKqepNg", Name: "Whoosh 2", Filename: "whoosh2.mp3", Family: "transition", Subtype: "whoosh", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whoosh"}, Tags: []string{"whoosh2", "whoosh", "transition"}},
	{Alias: "whoosh3", DriveFileID: "1rNnmb3if98M3aSpj2O9EtuvSNJ4AdSen", Name: "Whoosh 3", Filename: "whoosh3.mp3", Family: "transition", Subtype: "whoosh", Mood: "neutral", Energy: "medium", BestFor: []string{"transition", "motion", "whoosh"}, Tags: []string{"whoosh3", "whoosh", "transition"}},
}

// EditorialAudioAssets returns a defensive copy. Callers may adapt metadata
// for a wire format but cannot mutate the canonical registry.
func EditorialAudioAssets() []EditorialAudioAsset {
	out := make([]EditorialAudioAsset, len(editorialAudioAssets))
	for i, asset := range editorialAudioAssets {
		out[i] = asset
		out[i].BestFor = append([]string(nil), asset.BestFor...)
		out[i].Tags = append([]string(nil), asset.Tags...)
	}
	return out
}

// EditorialTransitionAliases returns the canonical short transition-effect
// aliases (subtype whop and whoosh) in declaration order. It is the single
// definition of "which editorial SFX a manifest may select as a transition".
func EditorialTransitionAliases() []string {
	out := make([]string, 0, len(editorialAudioAssets))
	for _, asset := range editorialAudioAssets {
		if asset.Family == "transition" {
			out = append(out, asset.Alias)
		}
	}
	return out
}

// EditorialAudioAliases returns the stable public alias → Drive identity map.
func EditorialAudioAliases() map[string]string {
	out := make(map[string]string, len(editorialAudioAssets))
	for _, asset := range editorialAudioAssets {
		out[asset.Alias] = asset.DriveFileID
	}
	return out
}

// ValidateEditorialAudioCatalog protects the cross-domain identity invariant:
// one public alias and one Drive identity may belong to one editorial role.
// In particular, a BGM must never also be addressable as an SFX alias.
func ValidateEditorialAudioCatalog() error {
	aliases := make(map[string]struct{}, len(editorialAudioAssets))
	identities := make(map[string]string, len(editorialAudioAssets))
	for _, asset := range editorialAudioAssets {
		if strings.TrimSpace(asset.Alias) == "" || strings.TrimSpace(asset.DriveFileID) == "" {
			return fmt.Errorf("editorial audio catalog: alias and Drive identity are required")
		}
		if _, exists := aliases[asset.Alias]; exists {
			return fmt.Errorf("editorial audio catalog: duplicate alias %q", asset.Alias)
		}
		aliases[asset.Alias] = struct{}{}
		if previous, exists := identities[asset.DriveFileID]; exists && previous != asset.Alias {
			return fmt.Errorf("editorial audio catalog: Drive identity %q is shared by %q and %q", asset.DriveFileID, previous, asset.Alias)
		}
		identities[asset.DriveFileID] = asset.Alias
	}
	return nil
}
