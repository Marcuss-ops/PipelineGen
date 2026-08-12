package scriptgeneration

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestResolvedScenesRoundTripPreservesCanonicalTimingAndAssets(t *testing.T) {
	scenes := make([]Scene, 13)
	clipIDs := []string{"11MRzjKA3o7OZGmYZGJMPTGxM_eNt6qAX", "11_5vtQgxOfFdBnsC8FQnCu0UQ2WpfO-c", "128AEiKFTwEZJO4dBUbhe4hu7bzbLAR-W", "1jDRQz8zDFjg86RpgSuxUIHuIE-DKtuET", "1HX3_LUx4Yg-mLaQkukKPrKTlEibuPz98", "1w_wGC43vY4wGtoBOnCErB_r-Hls_bdtO", "1dRv4WFUcXgLUf3QvqheZ9pCqvJY3KcFu", "1Pwng-iqQAVS5VZJmVNGHMBLQBFLsNyOU", "1xWrSFJSg5K5D_hZTxKILhnddvFytWaHI", "1qo69v-Kwuouxyr38-rdsqbS1G53exsFW", "1rsxgdS2fOnIfMAT9LaMpy7U6o07oHtPT", "1teqci7OK3ejVaY6wRGhoWAkDcvHAnIOr", "1tyLaLP_ARFfvaBkEhmNgtg8hLXoGHLsD"}
	for i, clipID := range clipIDs {
		scenes[i] = Scene{ID: "scene-" + string(rune('0'+i)), Index: i, DurationUS: int64(3+i) * 1_000_000, Clip: &ClipReference{ID: clipID, SourceInMS: int64(i+1) * 1000, SourceOutMS: int64(i+4) * 1000, AudioPath: "/media/" + clipID + ".m4a"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: clipID, SourceInUS: int64(i+1) * 1_000_000, SourceDurationUS: 3_000_000, UseOriginalAudio: true}, Voiceover: map[Language]AudioReference{"it": {ID: "vo-" + string(rune('a'+i)), FilePath: "/voiceover/" + clipID + ".m4a", Duration: float64(2 + i%2)}}}
	}
	before, err := ResolveScenes(scenes, "it")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after []ResolvedScene
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("resolved scenes changed after persistence\nbefore=%s\nafter=%s", raw, mustJSON(t, after))
	}
	for _, scene := range after {
		if scene.DurationUS <= 0 || scene.Video.SourceInUS <= 0 || scene.Video.SourceDurationUS <= 0 || scene.Voiceover == nil || scene.Voiceover.AssetID == "" || scene.Voiceover.DurationUS <= 0 {
			t.Fatalf("scene lost canonical fields: %+v", scene)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
