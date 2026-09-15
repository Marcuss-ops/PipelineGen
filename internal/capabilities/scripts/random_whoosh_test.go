// Package scriptgeneration — random_whoosh_test.go: pins the resolver
// directive contract for the built-in whoosh family.
package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestRandomWhooshDirectiveExpandsToADeclaredFamilyMember certifies the
// `random_whoosh` contract end to end at the intent boundary:
//
//   - the directive itself is never emitted as an asset id;
//   - it always expands to a member of the declared whoosh family, so the
//     wire vocabulary and the resolver can never disagree about the family;
//   - the expansion is deterministic for a given timeline + placement, so a
//     retry produces a byte-identical plan.
//
// Whether the selected alias then RESOLVES is deliberately not decided here:
// the whoosh aliases are compatibility vocabulary with no editorial binding,
// so the media registry lookup is the fail-closed gate (see the audio
// package's canonical-alias test and the resolver's UnknownAssetFailsClosed
// coverage).
func TestRandomWhooshDirectiveExpandsToADeclaredFamilyMember(t *testing.T) {
	family := make(map[string]struct{})
	for _, alias := range scriptpkg.BuiltInWhooshAliases() {
		family[alias] = struct{}{}
	}
	if len(family) == 0 {
		t.Fatal("whoosh family is empty; random_whoosh cannot expand")
	}

	timeline := sfxTestTimeline()
	intents := []scriptpkg.SoundEffectIntent{
		{AssetID: scriptpkg.RandomWhooshDirective, AtMS: 1000},
		{AssetID: scriptpkg.RandomWhooshDirective, SceneID: "scene_2", Anchor: scriptpkg.SFXAnchorEnd},
		{AssetID: scriptpkg.RandomWhooshDirective, AtMS: 25000},
	}

	out, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, intents)
	if err != nil {
		t.Fatalf("resolve random_whoosh: %v", err)
	}
	if len(out) != len(intents) {
		t.Fatalf("resolved %d placements, want %d", len(out), len(intents))
	}
	for i, sfx := range out {
		if sfx.AssetID == scriptpkg.RandomWhooshDirective {
			t.Fatalf("sfx %d still carries the directive %q", i, sfx.AssetID)
		}
		if _, ok := family[sfx.AssetID]; !ok {
			t.Fatalf("sfx %d expanded to %q, which is not a declared whoosh family member", i, sfx.AssetID)
		}
	}

	again, err := NewAudioIntentResolver().ResolveSoundEffects(timeline, intents)
	if err != nil {
		t.Fatalf("resolve random_whoosh (second run): %v", err)
	}
	for i := range out {
		if out[i].AssetID != again[i].AssetID || out[i].TimelineStartUS != again[i].TimelineStartUS {
			t.Fatalf("random_whoosh is not deterministic at %d: %q@%d vs %q@%d",
				i, out[i].AssetID, out[i].TimelineStartUS, again[i].AssetID, again[i].TimelineStartUS)
		}
	}
}
