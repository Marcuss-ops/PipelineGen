package main

// additionalSoundEffect is the shared catalog record used by all semantic sections.
type additionalSoundEffect struct {
	OldName, NewName, Category, Description string
	Tags                                    []string
}

func soundEffectsMusicCatalog() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"mixkit-driving-ambition-32.mp3", "music_driving_ambition_01.mp3", "music", "Driving ambition background music", []string{"music", "driving", "ambition", "background"}},
		{"Podcast Background Music While Talking Interview - Free Music to use, No Copyright - TALK#1.mp3", "music_podcast_background_talk_01.mp3", "music", "Background music for podcast or talking interview", []string{"music", "podcast", "background", "talk", "interview"}},
		{"audio_mixkit_21_445_01.mp3", "music_mixkit_21_445_01.mp3", "background_music", "Relaxed pop and folktronica background track for lifestyle, emotional and reflective scenes", []string{"pop", "folktronica", "relaxed", "reflective", "lifestyle", "background"}},
		{"music_driving_ambition_01.mp3", "music_driving_ambition_01.mp3", "background_music", "Uplifting and hopeful cinematic background music for ambition, progress and motivational storytelling", []string{"motivational", "uplifting", "hopeful", "cinematic", "brass", "background"}},
		{"audio_mixkit_zay_zay_01.mp3", "music_mixkit_zay_zay_01.mp3", "background_music", "Relaxed contemporary R&B and chillout track for reflective, travel and lifestyle sequences", []string{"rnb", "chillout", "relaxed", "reflective", "travel", "background"}},
		{"music_podcast_background_talk_01.mp3", "music_podcast_background_talk_01.mp3", "background_music", "Subtle instrumental podcast bed designed to remain underneath interviews, narration and spoken dialogue", []string{"podcast", "interview", "talking", "narration", "instrumental", "background"}},
		{"mixkit-purple-js-453.mp3", "music_mixkit_purple_js_01.mp3", "music", "Chill, slow-tempo electronic lofi beat loop with warm Rhodes chords and smooth synth leads", []string{"lofi", "chill", "beat", "electronic", "loop", "synth"}},
	}
}

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

func soundEffectsAtmosphereCatalog() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"07 Subsonic A.wav", "sfx_subsonic_low_rumble_01.wav", "low_frequency", "Deep subsonic low-frequency rumble", []string{"subsonic", "low", "frequency", "rumble", "bass", "drone"}},
		{"08 Subsonic B.wav.wav", "sfx_subsonic_low_rumble_02.wav", "low_frequency", "Deep subsonic low-frequency rumble variation", []string{"subsonic", "low", "frequency", "rumble", "bass", "drone"}},
		{"BUILD-UP.mp3", "sfx_riser_cinematic_build_up_01.mp3", "riser", "Long cinematic tension riser and build-up crescendo", []string{"build-up", "riser", "crescendo", "tension", "transition"}},
		{"Clock Ticking Sound Effect(MP3_160K).mp3", "sfx_clock_ticking_countdown_01.mp3", "tension", "Steady clock ticking for countdowns, suspense and time-pressure moments", []string{"clock", "ticking", "countdown", "time", "suspense", "tension"}},
		{"Clock ticking fast.mp3", "sfx_clock_ticking_urgent_01.mp3", "tension", "Rapid clock ticking for urgent countdowns, deadlines and escalating suspense", []string{"clock", "fast", "ticking", "urgent", "countdown", "suspense"}},
		{"sfx_clock_ticking_fast_loop.mp3", "sfx_clock_ticking_fast_loop.mp3", "tension", "Steady clock ticking for countdowns, suspense and time-pressure moments", []string{"clock", "ticking", "countdown", "time", "suspense", "tension"}},
		{"Sudden suspense Sound effect.mp3", "sfx_suspense_sudden_01.mp3", "tension", "Sudden suspense accent for dramatic moments", []string{"suspense", "tension", "dramatic", "stinger"}},
		{"sfx_whoosh_spooky_wind_01.wav", "sfx_ambient_spooky_wind_01.wav", "ambient", "Eerie, low-frequency howling wind creating an ominous horror atmosphere", []string{"wind", "spooky", "horror", "ambient", "eerie", "drone"}},
		{"sfx_glitch_upset_pulses_01.wav", "sfx_ambient_upset_pulses_01.wav", "ambient", "Disturbing, pulsating dark synthesizer drone generating intense psychological tension", []string{"drone", "pulse", "dark", "tension", "ambient", "industrial"}},
		{"01 Beating.wav", "sfx_heartbeat_beating_01.wav", "ambient", "Rhythmic heartbeat beating sound", []string{"heartbeat", "beating", "pulse", "tension", "ambient"}},
		{"05 Spaceship Riser.wav", "sfx_riser_spaceship_engine_01.wav", "riser", "Sci-fi engine roar acceleration acting as a synthetic pitch riser", []string{"riser", "spaceship", "sci-fi", "engine", "acceleration"}},
		{"SOUND 2.mp3", "sfx_riser_reverse_wind_01.mp3", "riser", "Reversed atmospheric wind-texture riser that culminates in a clean cut-off", []string{"riser", "reverse", "atmospheric", "wind", "transition", "tension"}},
		{"04 Evolve_Boom Feedback E.wav", "sfx_ambient_evolve_feedback_01.wav", "ambient", "Evolving cinematic mid-range drone with subtle resonant feedback modulations", []string{"drone", "feedback", "evolve", "ambient", "cinematic", "resonance"}},
		{"sfx_sub_deep_rumble_01.wav", "sfx_ambient_sub_bass_drone_01.wav", "ambient", "Low-frequency sub-bass sub-aquatic drone or atmospheric rumble", []string{"deep", "sub", "bass", "drone", "ambient", "rumble"}},
		{"sfx_cinematic_brassy_drop_01.wav", "sfx_riser_brassy_sub_drop_01.wav", "riser", "Long evolving cinematic riser leading into a heavy brassy sub drop", []string{"riser", "brass", "drop", "evolving", "cinematic", "tension"}},
		{"sfx_riser_cinematic_build_up_01.mp3", "sfx_riser_cinematic_build_up_01.mp3", "riser", "Short cinematic tension riser and build-up crescendo", []string{"build-up", "riser", "crescendo", "tension", "transition"}},
	}
}

func soundEffectsGlitchCatalog() []additionalSoundEffect {
	return []additionalSoundEffect{
		{"18 Termainal Glitch.wav", "sfx_glitch_digital_error_04.wav", "glitch", "Digital glitch distortion effect with television static noise", []string{"glitch", "digital", "error", "noise", "static", "tv"}},
		{"02 Dial-up.wav", "sfx_glitch_dialup_digital_error_01.wav", "glitch", "Dial-up modem connection and digital interference", []string{"glitch", "dial-up", "modem", "digital", "noise"}},
		{"sfx_glitch_digital_error_04.wav", "sfx_glitch_digital_error_04.wav", "glitch", "Digital interference simulating a system error or malfunction", []string{"terminal", "glitch", "error", "cyber", "malfunction", "ui"}},
		{"sfx_ui_reboot_failure_01.wav", "sfx_glitch_reboot_failure_01.wav", "glitch", "Digital reboot malfunction and failure sequence for system errors, crashes and cyber scenes", []string{"reboot", "failure", "error", "glitch", "system", "cyber"}},
		{"audio_sound_4.mp3", "sfx_glitch_granular_stretch_01.mp3", "glitch", "Lo-fi granular digital stretch glitch artifact for system corruption or error design", []string{"glitch", "digital", "lo-fi", "corruption", "error"}},
		{"Upset Pulses.wav", "sfx_glitch_upset_pulses_01.wav", "glitch", "Unclassified pulsing electronic effect for tense or corrupted scenes", []string{"pulses", "electronic", "glitch", "tension"}},
		{"Sound Effect Glitch.mp3", "sfx_glitch_sound_effect_01.mp3", "glitch", "Digital glitch sound effect", []string{"glitch", "digital", "error", "corruption"}},
		{"sfx_glitch_sound_effect_01.mp3", "sfx_glitch_distortion_burst_01.mp3", "glitch", "High-frequency digital distortion burst simulating data corruption", []string{"glitch", "distortion", "digital", "noise", "error", "scifi"}},
		{"04 Glitch Short.wav", "sfx_glitch_electrical_short_01.wav", "glitch", "Short electrical failure burst with tactile static and digital clicking", []string{"glitch", "static", "short", "interference", "noise"}},
		{"06 Electronic Glitch.wav", "sfx_glitch_electronic_granular_01.wav", "glitch", "High-speed granular synthesis error sound for digital UI menus", []string{"glitch", "electronic", "ui", "granular", "error"}},
		{"Glitch Sound Effect.mp3", "sfx_glitch_signal_tear_01.mp3", "glitch", "Rhythmic, granular high-frequency signal tear simulating data stream corruption", []string{"glitch", "noise", "corruption", "digital", "interference"}},
		{"007.wav", "sfx_glitch_stutter_click_errors_01.wav", "glitch", "Short digital stutter loop with continuous micro-click errors and data scrambling", []string{"glitch", "stutter", "digital", "clicking", "error"}},
		{"008.wav", "sfx_glitch_mechanical_signal_tear_01.wav", "glitch", "Aggressive mechanical signal tear with high-frequency telemetry corruption noise", []string{"glitch", "noise", "corruption", "mechanical", "signal"}},
		{"009.wav", "sfx_glitch_granular_processing_failure_01.wav", "glitch", "Granular data processing failure burst with a continuous synthetic clicking timeline", []string{"glitch", "granular", "data", "processing", "error"}},
		{"Digital counting.mp3", "sfx_glitch_digital_counting_01.mp3", "glitch", "Fast-paced digital data stream, mimicking rapid calculations, UI scrolling, or telemetry processing errors", []string{"digital", "counting", "data", "processing", "glitch", "scrolling"}},
		{"04 Erased Data.wav", "sfx_glitch_erased_data_01.wav", "glitch", "Rhythmic low-fidelity digital stutter loop mimicking system wipe processes", []string{"data", "erased", "glitch", "stutter", "digital", "wipe"}},
		{"06 Portal Hop.wav", "sfx_glitch_portal_hop_01.wav", "glitch", "Granular sci-fi glitch burst simulating rapid teleportation or data hopping", []string{"portal", "hop", "glitch", "scifi", "granular", "teleport"}},
		{"07 Line Break.wav", "sfx_glitch_line_break_01.wav", "glitch", "Aggressive mechanical signal corruption noise representing physical line termination", []string{"line", "break", "glitch", "corruption", "noise", "signal"}},
		{"09 Data Transfer.wav", "sfx_glitch_data_transfer_01.wav", "glitch", "Short micro-glitch sequence suggesting high-speed network packet transmission", []string{"data", "transfer", "glitch", "network", "packet", "short"}},
		{"12 Intermodulation.wav", "sfx_glitch_intermodulation_01.wav", "glitch", "Complex signal intermodulation burst featuring granular distortion and wave scanning", []string{"intermodulation", "glitch", "granular", "distortion", "scifi", "signal"}},
		{"13 Restart Switch.wav", "sfx_glitch_restart_switch_01.wav", "glitch", "Aggressive electrical power cycle crackle simulating a system main switcher reset", []string{"switch", "restart", "glitch", "power", "electrical", "reset"}},
		{"17 Disconnected.wav", "sfx_glitch_disconnected_signal_01.wav", "glitch", "Unstable radio frequency signal break mimicking a network server disconnect alert", []string{"disconnected", "glitch", "signal", "network", "error", "interference"}},
		{"sfx_ui_rf_switcher_01.wav", "sfx_glitch_rf_switcher_01.wav", "glitch", "Radio frequency hardware switcher glitch with digital static bursts", []string{"rf", "switcher", "glitch", "static", "hardware", "noise"}},
		{"sfx_ui_processing_01.wav", "sfx_glitch_processing_burst_01.wav", "glitch", "High-speed digital data processing burst with granular corruption artifacts", []string{"processing", "data", "glitch", "digital", "burst", "noise"}},
		{"audio_010.wav", "sfx_glitch_digital_stutter_01.wav", "glitch", "Rhythmic digital stutter bursts for errors, cyber edits and rapid electronic transitions", []string{"digital", "glitch", "stutter", "pulse", "error", "transition"}},
		{"sfx_glitch_digital_error_04.wav", "sfx_glitch_terminal_malfunction_01.wav", "glitch", "Digital interference simulating a system error or malfunction", []string{"terminal", "glitch", "error", "cyber", "malfunction", "ui"}},
	}
}
