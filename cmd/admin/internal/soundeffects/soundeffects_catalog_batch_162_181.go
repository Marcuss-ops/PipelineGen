package soundeffects

// Batch 162–181: newly curated effects grouped by semantic category.
func soundEffectsBatch162181() []additionalSoundEffect {
	result := append(soundEffectsBatch162181Transition(), soundEffectsBatch162181Foley()...)
	return append(result, soundEffectsBatch162181Other()...)
}

func soundEffectsBatch162181Transition() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"mixkit-warping-slide-1531.wav", "sfx_transition_warping_slide_01.wav", "transition", "Electronic warping slide effect, ideal for sci-fi UI menus or digital transitions", []string{"warping", "slide", "sci-fi", "transition", "synth"}},
		{"mixkit-cinematic-wind-swoosh-1471.wav", "sfx_whoosh_cinematic_wind_deep_01.wav", "transition", "Deep, atmospheric cinematic wind swoosh for theatrical trailer scene changes", []string{"wind", "swoosh", "whoosh", "cinematic", "transition", "atmosphere"}},
		{"mixkit-fast-tape-rewind-cinematic-transition-1092.wav", "sfx_transition_tape_rewind_fast_01.wav", "transition", "High-speed analog tape rewind effect used as a stylized cinematic transition", []string{"tape", "rewind", "analog", "transition", "fast", "cinematic"}},
		{"mixkit-in-and-out-zoom-sound-2622.wav", "sfx_transition_zoom_sweep_01.wav", "transition", "Quick electronic dynamic sweep simulating a sudden zoom-in or zoom-out camera motion", []string{"zoom", "sweep", "transition", "camera", "motion", "synth"}},
		{"SciFi Sound Effects.mp3", "sfx_transition_scifi_spacecraft_01.mp3", "transition", "A collection of synthesized spacecraft sound elements, including engine hums, sweeps, and futuristic flybys", []string{"sci-fi", "synthesizer", "sweep", "spacecraft", "transition", "teleport"}},
		{"Swish Swoosh Cutscene Sound Effect.mp3", "sfx_transition_cutscene_swishes_01.mp3", "transition", "A sequential series of dynamic organic whooshes and swishes, perfect for choreography or rapid cutscene transitions", []string{"swish", "swoosh", "whoosh", "transition", "motion", "cutscene"}},
	}
}

func soundEffectsBatch162181Foley() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"mixkit-dog-barking-twice-1.wav", "sfx_foley_dog_barking_twice_01.wav", "foley", "Two distinct and crisp alerts of a medium-sized domestic dog barking", []string{"dog", "bark", "animal", "foley", "alert"}},
		{"mixkit-gun-click-1123.wav", "sfx_foley_gun_chamber_click_01.wav", "foley", "Sharp, metallic mechanical double-click mimicking a weapon chambering or safety switch", []string{"gun", "click", "metallic", "mechanical", "foley", "weapon"}},
		{"mixkit-person-blows-2660.wav", "sfx_foley_party_horn_blow_01.wav", "foley", "Comedic festive whistle blow expanding into a high-pitched party horn sound effect", []string{"blow", "whistle", "party", "horn", "comedy", "foley"}},
		{"NO COPYRIGHT BOXING BELL SOUND EFFECT.mp3", "sfx_foley_boxing_bell_01.mp3", "foley", "Classic metallic ringing of a sports arena boxing bell, marking the start or end of a round", []string{"bell", "boxing", "ring", "metallic", "sports", "foley"}},
		{"Paper Flip Sound Effect.mp3", "sfx_foley_paper_flip_01.mp3", "foley", "Quick, textured rustle of a single notebook page being turned or flipped over", []string{"paper", "flip", "page", "turn", "foley", "handling"}},
		{"Paper Slide 03.wav", "sfx_foley_paper_slide_03.wav", "foley", "Short, frictional scraping noise of a sheet of paper sliding across a smooth tabletop surface", []string{"paper", "slide", "scrape", "friction", "foley", "handling"}},
		{"Paper Sound Effects.mp3", "sfx_foley_paper_interactions_01.mp3", "foley", "A varied collection of paper interactions, including crinkling, shuffling, and light tearing sounds", []string{"paper", "crinkle", "shuffle", "handling", "foley", "texture"}},
		{"party horn.mp3", "sfx_foley_party_blower_horn_01.mp3", "foley", "Classic festive party blower horn blare, perfect for celebrations or comedy scenes", []string{"horn", "party", "blower", "celebration", "foley", "comedy"}},
		{"Typewriter.mp3", "sfx_foley_typewriter_fast_01.mp3", "foley", "Rapid tactile clatter of a vintage mechanical typewriter being typed on quickly", []string{"typewriter", "typing", "mechanical", "keys", "foley"}},
		{"Shutter Click sound effect (no copyright).mp3", "sfx_foley_camera_shutter_dslr_01.mp3", "foley", "Mechanical mirror flip and shutter release of a professional DSLR photo camera", []string{"shutter", "click", "camera", "photo", "mechanical", "foley"}},
		{"Shutter Click sound effect no copyright.mp3", "sfx_foley_camera_shutter_slr_01.mp3", "foley", "Slightly deeper mechanical shutter click from an SLR camera, capturing a snapshot action", []string{"shutter", "click", "camera", "photo", "slr", "foley"}},
		{"smooth_pencil_sfx.MP3", "sfx_foley_pencil_writing_01.mp3", "foley", "The crisp friction noise of a graphite pencil writing or sketching on thick paper", []string{"pencil", "write", "sketch", "paper", "friction", "foley"}},
	}
}

func soundEffectsBatch162181Other() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"mixkit-electricity-static-power-up-2600.wav", "sfx_glitch_electricity_power_up_01.wav", "glitch", "Aggressive electrical static crackle paired with a textured energetic power-up sound", []string{"electricity", "static", "power-up", "glitch", "crackle", "energy"}},
		{"mixkit-hard-pop-click-2364.wav", "sfx_ui_hard_pop_click_01.wav", "ui", "Crisp and punchy acoustic mouth pop, perfect for minimalistic interface button clicks", []string{"pop", "click", "ui", "interface", "bubble", "minimal"}},
		{"mixkit-metal-hit-woosh-1485.wav", "sfx_impact_metal_hit_whoosh_01.wav", "impact", "Heavy iron metallic clash combined with a fast swoosh tail layer", []string{"metal", "hit", "impact", "whoosh", "swoosh", "cinematic"}},
		{"onlymp3.to - bass drop sound effect-H9CWQaMYXiI-192k-1688370057.mp3", "sfx_impact_bass_drop_vibrating_01.mp3", "impact", "Heavy cinematic sub-bass drop exploding outward with a continuous vibrating frequency tail", []string{"bass", "drop", "sub", "impact", "cinematic", "sub-woofer"}},
		{"Pop 9.mp3", "sfx_ui_pop_9_01.mp3", "ui", "Short, organic mouth pop sound with a soft acoustic echo, perfect for minimalist interface elements", []string{"pop", "click", "ui", "interface", "bubble", "minimal"}},
		{"Mountain Audio - New Idea Notification.mp3", "sfx_ui_new_idea_notification_01.mp3", "ui", "Bright and clean electronic chime for smart home or mobile application alerts", []string{"chime", "notification", "ui", "alert", "bell", "digital"}},
		{"Mouse Click.mp3", "sfx_ui_mouse_click_05.mp3", "ui", "Standard high-frequency click sound of a computer mouse button press", []string{"click", "mouse", "ui", "hardware", "desktop", "button"}},
		{"Pop 1.mp3", "sfx_ui_pop_bubble_snappy_01.mp3", "ui", "Very short, snappy wet bubble pop sound for rapid interactive button feedback", []string{"pop", "bubble", "snappy", "ui", "click", "feedback"}},
		{"Pop Bubble Sound Effect.mp3", "sfx_ui_bubble_pop_resonant_01.mp3", "ui", "Resonant and hollow bubble pop sound for cartoon physics or micro-interactions", []string{"bubble", "pop", "ui", "cartoon", "water", "click"}},
		{"Pop up Sound effect.mp3", "sfx_ui_popup_double_digital_01.mp3", "ui", "Double digital pop frequency for opening menus or interface modal windows", []string{"pop-up", "ui", "notification", "menu", "digital"}},
		{"punch.mp3", "sfx_impact_combat_punch_01.mp3", "impact", "Classic cinematic combat punch impact with a heavy, wet physical slap texture", []string{"punch", "impact", "hit", "combat", "slap", "fight"}},
		{"Sci Fi UI Sounds.mp3", "sfx_ui_scifi_interface_suite_01.mp3", "ui", "Suite of futuristic sci-fi interface chirps, data scans, and electronic confirmation feedback", []string{"sci-fi", "ui", "futuristic", "chirp", "scan", "cyberpunk"}},
	}
}
