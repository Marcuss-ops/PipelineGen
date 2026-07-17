package main

// additionalSoundEffects assembles all semantic catalog sections in a stable order.
func additionalSoundEffects() []additionalSoundEffect {
	sections := [][]additionalSoundEffect{
		soundEffectsUICatalog(),
		soundEffectsFoleyCatalog(),
		soundEffectsImpactCatalog(),
		soundEffectsWhooshCatalog(),
		soundEffectsGlitchCatalog(),
		soundEffectsMusicCatalog(),
		soundEffectsAtmosphereCatalog(),
		soundEffectsMiscCatalog(),
	}
	var result []additionalSoundEffect
	for _, section := range sections {
		result = append(result, section...)
	}
	return append(result, soundEffectsBatch162181()...)
}
