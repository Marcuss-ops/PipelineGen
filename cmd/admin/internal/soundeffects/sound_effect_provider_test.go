package soundeffects

// sound_effect_provider_test.go pins WHICH canonical asset kind each provided
// sound effect is indexed as.
//
// The defect this closes: the indexer decided `bgm` vs `sound_effect` from
// `spec.family`, but every track in this catalog carries `family: "music"`
// (`sound_effect` is the fallback), so the condition could never be true and
// every background track was indexed with the `sound_effect` provider — which
// `mediaregistry.defaultAssetKind` resolves to AssetSFX. The assets were still
// resolvable by the audio renderer (that path only gates on the media type),
// so nothing failed loudly; the damage was a WRONG canonical kind in the media
// SSOT, which is exactly what the comment on the derivation warned about.
//
// The assertion is made through the canonical taxonomy owner
// (mediaregistry.ResolveTaxonomy) rather than by comparing provider strings, so
// the test fails if the provider → asset_kind mapping itself changes.

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

func TestSoundEffectProviderClassifiesBackgroundMusicAsBgm(t *testing.T) {
	const (
		backgroundMusicSubtype = "background_music"
		bgmProvider            = "bgm"
		sfxProvider            = "sound_effect"
	)

	bgmCount, sfxCount := 0, 0
	for _, spec := range providedSoundEffects {
		provider := soundEffectProvider(spec)
		taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
			AssetID:   spec.driveID,
			Provider:  provider,
			MediaType: mediaregistry.MediaAudio,
		})
		if err != nil {
			t.Fatalf("%s (subtype=%s): resolve taxonomy: %v", spec.filename, spec.subtype, err)
		}
		if spec.subtype == backgroundMusicSubtype {
			bgmCount++
			if provider != bgmProvider {
				t.Errorf("%s is background music but its provider is %q, want %q", spec.filename, provider, bgmProvider)
			}
			if taxonomy.AssetKind != mediaregistry.AssetBGM {
				t.Errorf("%s resolves to asset_kind %q, want %q", spec.filename, taxonomy.AssetKind, mediaregistry.AssetBGM)
			}
			continue
		}
		sfxCount++
		if provider != sfxProvider {
			t.Errorf("%s is a one-shot effect but its provider is %q, want %q", spec.filename, provider, sfxProvider)
		}
		if taxonomy.AssetKind != mediaregistry.AssetSFX {
			t.Errorf("%s resolves to asset_kind %q, want %q", spec.filename, taxonomy.AssetKind, mediaregistry.AssetSFX)
		}
	}
	// Non-vacuous: the catalog must actually exercise BOTH branches, otherwise
	// a broken derivation could pass this test by classifying nothing at all.
	if bgmCount == 0 || sfxCount == 0 {
		t.Fatalf("vacuous coverage: bgm=%d sfx=%d", bgmCount, sfxCount)
	}
}

// TestSoundEffectProviderIgnoresTheEditorialFamily pins WHY the discriminator is
// the subtype: the `music` family is shared by the meme music themes
// (for example `sfx_music_*`), so family alone can never separate a background
// track from a one-shot cue.
func TestSoundEffectProviderIgnoresTheEditorialFamily(t *testing.T) {
	background := providedSoundEffect{driveID: "d", filename: "bgm1.mp3", family: "music", subtype: "background_music"}
	theme := providedSoundEffect{driveID: "d", filename: "sfx_music_meme.mp3", family: "music", subtype: "meme_theme"}

	if got := soundEffectProvider(background); got != "bgm" {
		t.Errorf("background music provider = %q, want bgm", got)
	}
	if got := soundEffectProvider(theme); got != "sound_effect" {
		t.Errorf("meme theme provider = %q, want sound_effect", got)
	}
}
